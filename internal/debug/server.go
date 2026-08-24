package debug

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
)

// Server exposes one semantic Session to one local client at a time. A
// disconnected client may reconnect to the same retained session.
type Server struct {
	session *Session
	config  TransportConfig
}

// NewServer validates the bounded transport before accepting clients.
func NewServer(session *Session, config TransportConfig) (*Server, error) {
	if session == nil || !config.valid() {
		return nil, ErrInvalidTransportConfig
	}
	return &Server{session: session, config: config}, nil
}

// Serve accepts serial Unix-socket clients until context cancellation.
func (server *Server) Serve(ctx context.Context, listener net.Listener) error {
	if server == nil || server.session == nil || ctx == nil || listener == nil ||
		listener.Addr() == nil || listener.Addr().Network() != "unix" {
		return ErrInvalidTransportConfig
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-stop:
		}
	}()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		_ = server.serveConnection(ctx, connection)
	}
}

func (server *Server) serveConnection(parent context.Context, connection net.Conn) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer connection.Close()
	connectionDone := make(chan struct{})
	defer close(connectionDone)
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-connectionDone:
		}
	}()

	responses := make(chan Response, server.config.QueueDepth)
	writerDone := make(chan error, 1)
	go func() {
		for response := range responses {
			err := writeFrame(connection, server.config.MaxMessageBytes, response)
			if errors.Is(err, ErrFrameTooLarge) {
				err = writeFrame(connection, server.config.MaxMessageBytes, Response{
					ID: response.ID, Error: wireError(ErrFrameTooLarge),
				})
			}
			if err != nil {
				_ = connection.Close()
				writerDone <- err
				return
			}
		}
		writerDone <- nil
	}()

	var workers sync.WaitGroup
	slots := make(chan struct{}, server.config.MaxInFlight)
	requestCancels := make(map[uint64]context.CancelFunc, server.config.MaxInFlight)
	var requestMu sync.Mutex
	var reader frameReader
	for {
		var request Request
		err := reader.read(connection, server.config.MaxMessageBytes, &request)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				cancel()
				workers.Wait()
				close(responses)
				<-writerDone
				return err
			}
			break
		}
		if request.ID == 0 || !request.Operation.valid() {
			if !queueResponse(ctx, responses, Response{ID: request.ID, Error: wireError(ErrInvalidFrame)}) {
				break
			}
			continue
		}
		if request.Operation == OperationCancel {
			requestMu.Lock()
			cancelRequest := requestCancels[request.CancelID]
			requestMu.Unlock()
			if request.CancelID == 0 || cancelRequest == nil {
				if !queueResponse(ctx, responses, Response{ID: request.ID, Error: wireError(ErrInvalidFrame)}) {
					break
				}
				continue
			}
			cancelRequest()
			if !queueResponse(ctx, responses, Response{ID: request.ID}) {
				break
			}
			continue
		}
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			break
		}
		if ctx.Err() != nil {
			break
		}
		requestContext, cancelRequest := context.WithCancel(ctx)
		requestMu.Lock()
		_, duplicate := requestCancels[request.ID]
		if !duplicate {
			requestCancels[request.ID] = cancelRequest
		}
		requestMu.Unlock()
		if duplicate {
			cancelRequest()
			<-slots
			if !queueResponse(ctx, responses, Response{ID: request.ID, Error: wireError(ErrInvalidFrame)}) {
				break
			}
			continue
		}
		sessionReply, err := server.enqueue(requestContext, request)
		if err != nil {
			requestMu.Lock()
			delete(requestCancels, request.ID)
			requestMu.Unlock()
			cancelRequest()
			<-slots
			if !queueResponse(ctx, responses, Response{ID: request.ID, Error: wireError(err)}) {
				break
			}
			continue
		}
		workers.Add(1)
		go func(
			request Request,
			requestContext context.Context,
			cancelRequest context.CancelFunc,
			sessionReply <-chan response,
		) {
			defer workers.Done()
			defer func() { <-slots }()
			defer cancelRequest()
			defer func() {
				requestMu.Lock()
				delete(requestCancels, request.ID)
				requestMu.Unlock()
			}()
			var sessionResponse response
			select {
			case sessionResponse = <-sessionReply:
			case <-requestContext.Done():
				sessionResponse.err = requestContext.Err()
			}
			response := wireResponse(request, sessionResponse)
			select {
			case responses <- response:
			case <-ctx.Done():
			}
		}(request, requestContext, cancelRequest, sessionReply)
	}
	cancel()
	workers.Wait()
	close(responses)
	if err := <-writerDone; err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

func queueResponse(ctx context.Context, responses chan<- Response, response Response) bool {
	select {
	case responses <- response:
		return true
	case <-ctx.Done():
		return false
	}
}

func (server *Server) enqueue(ctx context.Context, request Request) (<-chan response, error) {
	requestCommand := command{}
	switch request.Operation {
	case OperationSnapshot:
		requestCommand.kind = commandSnapshot
	case OperationStepInstruction:
		requestCommand.kind = commandStepInstruction
	case OperationStepNode:
		requestCommand.kind = commandStepNode
	case OperationStepOver:
		requestCommand.kind = commandStepOver
	case OperationContinue:
		requestCommand.kind = commandContinue
	case OperationPause:
		requestCommand.kind = commandPause
	case OperationRestart:
		requestCommand.kind = commandRestart
	case OperationReplay:
		requestCommand.kind = commandReplay
		requestCommand.target = request.Instruction
	case OperationAddBreakpoint:
		requestCommand.kind = commandAddBreakpoint
		requestCommand.breakpoint = request.Breakpoint
	case OperationRemoveBreakpoint:
		requestCommand.kind = commandRemoveBreakpoint
		requestCommand.breakpointID = request.BreakpointID
	case OperationAddWatch:
		requestCommand.kind = commandAddWatch
		requestCommand.watch = request.Watch
	case OperationRemoveWatch:
		requestCommand.kind = commandRemoveWatch
		requestCommand.watchID = request.WatchID
	case OperationResult:
		requestCommand.kind = commandResult
	default:
		return nil, ErrInvalidFrame
	}
	return server.session.enqueue(ctx, requestCommand)
}

func wireResponse(request Request, sessionResponse response) Response {
	response := Response{ID: request.ID}
	if sessionResponse.err != nil {
		response.Error = wireError(sessionResponse.err)
		return response
	}
	switch request.Operation {
	case OperationSnapshot, OperationStepInstruction, OperationStepNode, OperationStepOver,
		OperationContinue, OperationPause, OperationRestart, OperationReplay:
		state := sessionResponse.state
		response.State = &state
	case OperationAddBreakpoint:
		response.BreakpointID = sessionResponse.breakpointID
	case OperationAddWatch:
		response.WatchID = sessionResponse.watchID
	case OperationResult:
		resultBatch := sessionResponse.result
		response.Result = &resultBatch
	}
	return response
}

func wireError(err error) *ProtocolError {
	if err == nil {
		return nil
	}
	protocolError := &ProtocolError{Code: ErrorSession, Message: err.Error()}
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		protocolError.Code = ErrorCanceled
	case errors.Is(err, ErrFrameTooLarge):
		protocolError.Code = ErrorFrameLimit
	case errors.Is(err, ErrInvalidFrame), errors.Is(err, ErrInvalidTransportConfig):
		protocolError.Code = ErrorInvalidRequest
	}
	return protocolError
}
