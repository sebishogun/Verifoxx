package debug

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
)

type outboundRequest struct {
	request Request
}

type pendingResponse struct {
	reply chan Response
}

// Client correlates concurrent semantic commands while one goroutine owns all
// writes and one goroutine owns all reads from the Unix connection.
type Client struct {
	connection net.Conn
	terminal   error
	outbound   chan outboundRequest
	done       chan struct{}
	slots      chan struct{}
	pending    map[uint64]pendingResponse
	config     TransportConfig
	loops      sync.WaitGroup
	nextID     atomic.Uint64
	closeOnce  sync.Once
	pendingMu  sync.Mutex
}

// DialClient connects to one local semantic debug socket.
func DialClient(ctx context.Context, path string, config TransportConfig) (*Client, error) {
	if ctx == nil || path == "" || !config.valid() {
		return nil, ErrInvalidTransportConfig
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	client := &Client{
		connection: connection,
		outbound:   make(chan outboundRequest, config.QueueDepth),
		done:       make(chan struct{}),
		slots:      make(chan struct{}, config.MaxInFlight),
		config:     config,
		pending:    make(map[uint64]pendingResponse, config.MaxInFlight),
	}
	client.loops.Add(2)
	go client.writeLoop()
	go client.readLoop()
	return client, nil
}

func (client *Client) Snapshot(ctx context.Context) (State, error) {
	response, err := client.request(ctx, Request{Operation: OperationSnapshot})
	return responseState(response, err)
}

func (client *Client) StepInstruction(ctx context.Context) (State, error) {
	response, err := client.request(ctx, Request{Operation: OperationStepInstruction})
	return responseState(response, err)
}

func (client *Client) StepNode(ctx context.Context) (State, error) {
	response, err := client.request(ctx, Request{Operation: OperationStepNode})
	return responseState(response, err)
}

func (client *Client) StepOver(ctx context.Context) (State, error) {
	response, err := client.request(ctx, Request{Operation: OperationStepOver})
	return responseState(response, err)
}

func (client *Client) Continue(ctx context.Context) (State, error) {
	response, err := client.request(ctx, Request{Operation: OperationContinue})
	return responseState(response, err)
}

func (client *Client) Pause(ctx context.Context) (State, error) {
	response, err := client.request(ctx, Request{Operation: OperationPause})
	return responseState(response, err)
}

func (client *Client) Restart(ctx context.Context) (State, error) {
	response, err := client.request(ctx, Request{Operation: OperationRestart})
	return responseState(response, err)
}

func (client *Client) Replay(ctx context.Context, instruction schema.InstructionID) (State, error) {
	response, err := client.request(ctx, Request{Operation: OperationReplay, Instruction: instruction})
	return responseState(response, err)
}

func (client *Client) AddBreakpoint(ctx context.Context, breakpoint Breakpoint) (BreakpointID, error) {
	response, err := client.request(ctx, Request{Operation: OperationAddBreakpoint, Breakpoint: breakpoint})
	if err != nil {
		return 0, err
	}
	return response.BreakpointID, nil
}

func (client *Client) RemoveBreakpoint(ctx context.Context, id BreakpointID) error {
	_, err := client.request(ctx, Request{Operation: OperationRemoveBreakpoint, BreakpointID: id})
	return err
}

func (client *Client) AddWatch(ctx context.Context, watch Watch) (WatchID, error) {
	response, err := client.request(ctx, Request{Operation: OperationAddWatch, Watch: watch})
	if err != nil {
		return 0, err
	}
	return response.WatchID, nil
}

func (client *Client) RemoveWatch(ctx context.Context, id WatchID) error {
	_, err := client.request(ctx, Request{Operation: OperationRemoveWatch, WatchID: id})
	return err
}

func (client *Client) Result(ctx context.Context) (result.Batch, error) {
	response, err := client.request(ctx, Request{Operation: OperationResult})
	if err != nil {
		return result.Batch{}, err
	}
	if response.Result == nil {
		return result.Batch{}, ErrInvalidFrame
	}
	return *response.Result, nil
}

// Close interrupts transport I/O. It does not close the retained debug session.
func (client *Client) Close() error {
	if client == nil {
		return ErrInvalidTransportConfig
	}
	client.fail(ErrTransportClosed)
	client.loops.Wait()
	return nil
}

func (client *Client) request(ctx context.Context, request Request) (Response, error) {
	if client == nil || ctx == nil {
		return Response{}, ErrInvalidTransportConfig
	}
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	select {
	case client.slots <- struct{}{}:
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case <-client.done:
		return Response{}, client.transportError()
	}
	id := client.nextID.Add(1)
	if id == 0 {
		<-client.slots
		return Response{}, ErrTransportClosed
	}
	request.ID = id
	reply := make(chan Response, 1)
	client.pendingMu.Lock()
	client.pending[id] = pendingResponse{reply: reply}
	client.pendingMu.Unlock()
	select {
	case client.outbound <- outboundRequest{request: request}:
	case <-ctx.Done():
		client.removePending(id)
		return Response{}, ctx.Err()
	case <-client.done:
		client.removePending(id)
		return Response{}, client.transportError()
	}
	select {
	case response := <-reply:
		if response.Error != nil {
			return Response{}, response.Error
		}
		return response, nil
	case <-ctx.Done():
		client.cancelRequest(id)
		client.removePending(id)
		return Response{}, ctx.Err()
	case <-client.done:
		client.removePending(id)
		return Response{}, client.transportError()
	}
}

func (client *Client) cancelRequest(id uint64) {
	cancelID := client.nextID.Add(1)
	if id == 0 || cancelID == 0 {
		return
	}
	request := outboundRequest{request: Request{ID: cancelID, Operation: OperationCancel, CancelID: id}}
	select {
	case client.outbound <- request:
	case <-client.done:
	default:
		client.fail(ErrTransportClosed)
	}
}

func (client *Client) writeLoop() {
	defer client.loops.Done()
	for {
		select {
		case outbound := <-client.outbound:
			if err := writeFrame(client.connection, client.config.MaxMessageBytes, outbound.request); err != nil {
				client.fail(err)
				return
			}
		case <-client.done:
			return
		}
	}
}

func (client *Client) readLoop() {
	defer client.loops.Done()
	var reader frameReader
	for {
		var response Response
		if err := reader.read(client.connection, client.config.MaxMessageBytes, &response); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				client.fail(ErrTransportClosed)
			} else {
				client.fail(err)
			}
			return
		}
		client.pendingMu.Lock()
		pending, ok := client.pending[response.ID]
		if ok {
			delete(client.pending, response.ID)
		}
		client.pendingMu.Unlock()
		if ok {
			<-client.slots
			pending.reply <- response
		}
	}
}

func (client *Client) removePending(id uint64) {
	client.pendingMu.Lock()
	_, ok := client.pending[id]
	if ok {
		delete(client.pending, id)
	}
	client.pendingMu.Unlock()
	if ok {
		<-client.slots
	}
}

func (client *Client) fail(cause error) {
	client.closeOnce.Do(func() {
		client.pendingMu.Lock()
		client.terminal = fmt.Errorf("%w: %v", ErrTransportClosed, cause)
		for id := range client.pending {
			delete(client.pending, id)
			<-client.slots
		}
		client.pendingMu.Unlock()
		close(client.done)
		_ = client.connection.Close()
	})
}

func (client *Client) transportError() error {
	client.pendingMu.Lock()
	err := client.terminal
	client.pendingMu.Unlock()
	if err == nil {
		return ErrTransportClosed
	}
	return err
}

func responseState(response Response, err error) (State, error) {
	if err != nil {
		return State{}, err
	}
	if response.State == nil {
		return State{}, ErrInvalidFrame
	}
	return *response.State, nil
}
