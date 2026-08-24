package debug

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/sebishogun/verifoxx/internal/schema"
)

func TestProtocolFrameRoundTrip(t *testing.T) {
	t.Parallel()

	want := Request{
		ID: 17, Operation: OperationReplay, Instruction: schema.InstructionID(9),
		Breakpoint: Breakpoint{Kind: BreakTruth, Truth: TruthBoth, Row: 3},
	}
	var wire bytes.Buffer
	if err := writeFrame(&wire, 1024, want); err != nil {
		t.Fatalf("writeFrame() error = %v", err)
	}
	var reader frameReader
	var got Request
	if err := reader.read(&wire, 1024, &got); err != nil {
		t.Fatalf("read() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestProtocolFrameBoundsAndStrictDecoding(t *testing.T) {
	t.Parallel()

	t.Run("write bound", func(t *testing.T) {
		var wire bytes.Buffer
		request := Request{ID: 1, Operation: OperationSnapshot}
		if err := writeFrame(&wire, 8, request); !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("writeFrame() error = %v, want ErrFrameTooLarge", err)
		}
	})

	t.Run("read bound", func(t *testing.T) {
		var wire bytes.Buffer
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], 65)
		wire.Write(prefix[:])
		var reader frameReader
		var request Request
		if err := reader.read(&wire, 64, &request); !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("read() error = %v, want ErrFrameTooLarge", err)
		}
	})

	t.Run("unknown field", func(t *testing.T) {
		wire := protocolTestFrame([]byte(`{"id":1,"operation":"snapshot","unexpected":true}`))
		var reader frameReader
		var request Request
		if err := reader.read(&wire, 1024, &request); !errors.Is(err, ErrInvalidFrame) {
			t.Fatalf("read() error = %v, want ErrInvalidFrame", err)
		}
	})

	t.Run("truncated", func(t *testing.T) {
		var wire bytes.Buffer
		var prefix [4]byte
		binary.BigEndian.PutUint32(prefix[:], 5)
		wire.Write(prefix[:])
		wire.WriteString("{}")
		var reader frameReader
		var request Request
		if err := reader.read(&wire, 1024, &request); !errors.Is(err, ErrInvalidFrame) {
			t.Fatalf("read() error = %v, want ErrInvalidFrame", err)
		}
	})
}

func TestTransportCommandsAndStateOverUnix(t *testing.T) {
	t.Parallel()

	p, batch, want := debugFixture(t)
	session := newDebugSession(t, p, batch, debugConfig())
	client := newTransportClient(t, session)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	initial, err := client.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if initial.Status != StatusPaused || initial.Cursor != 0 || initial.NextInstruction != 1 {
		t.Fatalf("initial state = %+v", initial)
	}
	watchID, err := client.AddWatch(ctx, Watch{Kind: WatchMask, Instruction: 1, Row: 0})
	if err != nil || watchID == 0 {
		t.Fatalf("AddWatch() = (%d, %v)", watchID, err)
	}
	breakpointID, err := client.AddBreakpoint(ctx, Breakpoint{Kind: BreakInstruction, Instruction: 2})
	if err != nil || breakpointID == 0 {
		t.Fatalf("AddBreakpoint() = (%d, %v)", breakpointID, err)
	}
	stepped, err := client.StepInstruction(ctx)
	if err != nil || stepped.Cursor != 1 || len(stepped.Watches) != 1 || !stepped.Watches[0].Ready {
		t.Fatalf("StepInstruction() = (%+v, %v)", stepped, err)
	}
	stopped, err := client.Continue(ctx)
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if stopped.Stop != StopBreakpoint || stopped.Breakpoint != breakpointID || stopped.Instruction != 2 {
		t.Fatalf("breakpoint state = %+v", stopped)
	}
	if err := client.RemoveBreakpoint(ctx, breakpointID); err != nil {
		t.Fatalf("RemoveBreakpoint() error = %v", err)
	}
	if err := client.RemoveWatch(ctx, watchID); err != nil {
		t.Fatalf("RemoveWatch() error = %v", err)
	}
	complete, err := client.Continue(ctx)
	if err != nil || complete.Status != StatusComplete || complete.Stop != StopComplete {
		t.Fatalf("Continue() = (%+v, %v)", complete, err)
	}
	got, err := client.Result(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Result() = (%+v, %v), want %+v", got, err, want)
	}
	replayed, err := client.Replay(ctx, schema.InstructionID(len(p.Opcodes)/2))
	if err != nil || replayed.Stop != StopReplay || replayed.Cursor != uint32(len(p.Opcodes)/2) {
		t.Fatalf("Replay() = (%+v, %v)", replayed, err)
	}
	restarted, err := client.Restart(ctx)
	if err != nil || restarted.Stop != StopRestart || restarted.Cursor != 0 {
		t.Fatalf("Restart() = (%+v, %v)", restarted, err)
	}
}

func TestTransportContinueAndPauseUseOneFramedWriter(t *testing.T) {
	t.Parallel()

	p, batch, want := debugFixture(t)
	extendDebugSchedule(p, 1<<16)
	session := newDebugSession(t, p, batch, debugConfig())
	client := newTransportClient(t, session)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	continued := make(chan State, 1)
	continueErr := make(chan error, 1)
	go func() {
		state, err := client.Continue(ctx)
		continued <- state
		continueErr <- err
	}()

	var running State
	for attempt := 0; attempt < 100; attempt++ {
		state, err := client.Snapshot(ctx)
		if err != nil {
			t.Fatalf("Snapshot() while running error = %v", err)
		}
		if state.Status == StatusRunning {
			running = state
			break
		}
		if state.Status == StatusComplete {
			t.Fatal("Continue() completed before running state was observed")
		}
	}
	if running.Status != StatusRunning {
		t.Fatal("transport did not return a running snapshot")
	}
	paused, err := client.Pause(ctx)
	if err != nil {
		t.Fatalf("Pause() error = %v", err)
	}
	continuedState := <-continued
	if err := <-continueErr; err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if paused.Status != StatusPaused || continuedState.Stop != StopPause || paused.Cursor != continuedState.Cursor {
		t.Fatalf("pause states = pause:%+v continue:%+v", paused, continuedState)
	}
	if _, err := client.Continue(ctx); err != nil {
		t.Fatalf("Continue() after pause error = %v", err)
	}
	got, err := client.Result(ctx)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Result() = (%+v, %v), want %+v", got, err, want)
	}
}

func TestTransportCancellationPausesContinue(t *testing.T) {
	t.Parallel()

	p, batch, _ := debugFixture(t)
	extendDebugSchedule(p, 1<<16)
	session := newDebugSession(t, p, batch, debugConfig())
	client := newTransportClient(t, session)
	controlContext, cancelControl := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelControl()
	runContext, cancelRun := context.WithCancel(controlContext)
	continueErr := make(chan error, 1)
	go func() {
		_, err := client.Continue(runContext)
		continueErr <- err
	}()

	var running State
	for attempt := 0; attempt < 100; attempt++ {
		state, err := client.Snapshot(controlContext)
		if err != nil {
			t.Fatalf("Snapshot() while running error = %v", err)
		}
		if state.Status == StatusRunning {
			running = state
			break
		}
	}
	if running.Status != StatusRunning {
		t.Fatal("transport did not return a running snapshot")
	}
	cancelRun()
	if err := <-continueErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("Continue() cancellation error = %v, want context.Canceled", err)
	}
	paused, err := client.Snapshot(controlContext)
	if err != nil {
		t.Fatalf("Snapshot() after cancellation error = %v", err)
	}
	if paused.Status != StatusPaused || paused.Stop != StopNone {
		t.Fatalf("state after cancellation = %+v", paused)
	}
}

func TestTransportDisconnectPausesAndAllowsReconnect(t *testing.T) {
	t.Parallel()

	p, batch, _ := debugFixture(t)
	extendDebugSchedule(p, 1<<16)
	session := newDebugSession(t, p, batch, debugConfig())
	path, config := newTransportServer(t, session)
	controlContext, cancelControl := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelControl()
	first, err := DialClient(controlContext, path, config)
	if err != nil {
		t.Fatalf("DialClient(first) error = %v", err)
	}
	continueErr := make(chan error, 1)
	go func() {
		_, err := first.Continue(controlContext)
		continueErr <- err
	}()

	var running State
	for attempt := 0; attempt < 100; attempt++ {
		state, err := first.Snapshot(controlContext)
		if err != nil {
			t.Fatalf("Snapshot() while running error = %v", err)
		}
		if state.Status == StatusRunning {
			running = state
			break
		}
	}
	if running.Status != StatusRunning {
		t.Fatal("transport did not return a running snapshot")
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first) error = %v", err)
	}
	if err := <-continueErr; !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("Continue() after disconnect error = %v, want ErrTransportClosed", err)
	}

	second, err := DialClient(controlContext, path, config)
	if err != nil {
		t.Fatalf("DialClient(second) error = %v", err)
	}
	defer second.Close()
	paused, err := second.Snapshot(controlContext)
	if err != nil {
		t.Fatalf("Snapshot(second) error = %v", err)
	}
	if paused.Status != StatusPaused || paused.Cursor < running.Cursor {
		t.Fatalf("reconnected state = %+v, previous running cursor %d", paused, running.Cursor)
	}
}

func TestTransportRejectsOversizedFrameAndAcceptsNextClient(t *testing.T) {
	t.Parallel()

	p, batch, _ := debugFixture(t)
	session := newDebugSession(t, p, batch, debugConfig())
	path, config := newTransportServer(t, session)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		t.Fatalf("DialContext(raw) error = %v", err)
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], config.MaxMessageBytes+1)
	if err := writeAll(connection, prefix[:]); err != nil {
		t.Fatalf("write oversized prefix error = %v", err)
	}
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	var one [1]byte
	if _, err := connection.Read(one[:]); err == nil {
		t.Fatal("oversized frame did not close the connection")
	}
	connection.Close()

	client, err := DialClient(ctx, path, config)
	if err != nil {
		t.Fatalf("DialClient() after malformed peer error = %v", err)
	}
	defer client.Close()
	state, err := client.Snapshot(ctx)
	if err != nil || state.Status != StatusPaused {
		t.Fatalf("Snapshot() after malformed peer = (%+v, %v)", state, err)
	}
}

func TestTransportServerCancellationClosesActiveClient(t *testing.T) {
	t.Parallel()

	p, batch, _ := debugFixture(t)
	session := newDebugSession(t, p, batch, debugConfig())
	config := DefaultTransportConfig()
	server, err := NewServer(session, config)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "debug.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	serveContext, cancelServe := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(serveContext, listener) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := DialClient(ctx, path, config)
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	defer client.Close()
	if _, err := client.Snapshot(ctx); err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	cancelServe()
	select {
	case err := <-serveErr:
		if err != nil {
			t.Fatalf("Server.Serve() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Server.Serve() did not stop with an active client")
	}
	if _, err := client.Snapshot(ctx); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("Snapshot() after server cancellation error = %v, want ErrTransportClosed", err)
	}
}

func TestTransportOversizedResponseReturnsBoundedError(t *testing.T) {
	t.Parallel()

	p, batch, _ := debugFixture(t)
	session := newDebugSession(t, p, batch, debugConfig())
	requestPayload, err := json.Marshal(Request{ID: 1, Operation: OperationSnapshot})
	if err != nil {
		t.Fatalf("json.Marshal(request) error = %v", err)
	}
	config := DefaultTransportConfig()
	config.MaxMessageBytes = uint32(len(requestPayload) + 16)
	path := newTransportServerWithConfig(t, session, config)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := DialClient(ctx, path, config)
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	defer client.Close()
	if _, err := client.Snapshot(ctx); err == nil {
		t.Fatal("Snapshot() oversized response error = nil")
	} else {
		var protocolError *ProtocolError
		if !errors.As(err, &protocolError) || protocolError.Code != ErrorFrameLimit {
			t.Fatalf("Snapshot() error = %v, want ErrorFrameLimit", err)
		}
	}
	if _, err := client.AddBreakpoint(ctx, Breakpoint{Kind: BreakInstruction, Instruction: 1}); err != nil {
		t.Fatalf("AddBreakpoint() after oversized response error = %v", err)
	}
}

func TestClientCancellationDoesNotBlockBehindFullWriter(t *testing.T) {
	t.Parallel()

	clientConnection, peerConnection := net.Pipe()
	defer clientConnection.Close()
	defer peerConnection.Close()
	client := &Client{
		connection: clientConnection,
		outbound:   make(chan outboundRequest, 1),
		done:       make(chan struct{}),
		slots:      make(chan struct{}, 2),
		pending:    make(map[uint64]pendingResponse),
	}
	client.nextID.Store(1)
	client.outbound <- outboundRequest{request: Request{ID: 1, Operation: OperationSnapshot}}
	returned := make(chan struct{})
	go func() {
		client.cancelRequest(1)
		close(returned)
	}()
	select {
	case <-returned:
		if !errors.Is(client.transportError(), ErrTransportClosed) {
			t.Fatalf("transport error = %v, want ErrTransportClosed", client.transportError())
		}
	case <-time.After(100 * time.Millisecond):
		close(client.done)
		<-returned
		t.Fatal("cancelRequest blocked behind a full outbound channel")
	}
}

func TestTransportPipelinedCommandsExecuteInWireOrder(t *testing.T) {
	t.Parallel()

	p, batch, _ := debugFixture(t)
	session := newDebugSession(t, p, batch, debugConfig())
	path, config := newTransportServer(t, session)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	defer connection.Close()
	if err := writeFrame(connection, config.MaxMessageBytes, Request{
		ID: 1, Operation: OperationAddBreakpoint,
		Breakpoint: Breakpoint{Kind: BreakInstruction, Instruction: 1},
	}); err != nil {
		t.Fatalf("write AddBreakpoint error = %v", err)
	}
	if err := writeFrame(connection, config.MaxMessageBytes, Request{ID: 2, Operation: OperationContinue}); err != nil {
		t.Fatalf("write Continue error = %v", err)
	}
	responses := make(map[uint64]Response, 2)
	var reader frameReader
	for range 2 {
		var response Response
		if err := reader.read(connection, config.MaxMessageBytes, &response); err != nil {
			t.Fatalf("read response error = %v", err)
		}
		responses[response.ID] = response
	}
	added := responses[1]
	continued := responses[2]
	if added.Error != nil || added.BreakpointID == 0 {
		t.Fatalf("AddBreakpoint response = %+v", added)
	}
	if continued.Error != nil || continued.State == nil || continued.State.Stop != StopBreakpoint ||
		continued.State.Breakpoint != added.BreakpointID || continued.State.Instruction != 1 {
		t.Fatalf("Continue response = %+v after AddBreakpoint %+v", continued, added)
	}
}

func protocolTestFrame(payload []byte) bytes.Buffer {
	var wire bytes.Buffer
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(payload)))
	wire.Write(prefix[:])
	wire.Write(payload)
	return wire
}

func newTransportClient(t testing.TB, session *Session) *Client {
	t.Helper()
	path, config := newTransportServer(t, session)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := DialClient(ctx, path, config)
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("Client.Close() error = %v", err)
		}
	})
	return client
}

func newTransportServer(t testing.TB, session *Session) (string, TransportConfig) {
	t.Helper()
	config := DefaultTransportConfig()
	return newTransportServerWithConfig(t, session, config), config
}

func newTransportServerWithConfig(t testing.TB, session *Session, config TransportConfig) string {
	t.Helper()
	server, err := NewServer(session, config)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "debug.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	serveContext, cancelServe := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(serveContext, listener) }()
	t.Cleanup(func() {
		cancelServe()
		listener.Close()
		select {
		case err := <-serveErr:
			if err != nil {
				t.Errorf("Server.Serve() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Error("Server.Serve() did not stop")
		}
	})
	return path
}
