package grpcapi

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	verifoxxv1 "github.com/sebishogun/verifoxx/api/gen/verifoxx/v1"
	"github.com/sebishogun/verifoxx/internal/compile"
	coreservice "github.com/sebishogun/verifoxx/internal/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestUnaryPolicyRPCs(t *testing.T) {
	t.Parallel()

	policySource := []byte(`{"schema_version":1}`)
	requests := []byte(`{"pack":"requests"}`)
	evidence := []byte(`{"pack":"evidence"}`)
	hash := [32]byte{1, 2, 3, 4}
	metadata := testPolicyMetadata()
	api := &fakePolicyAPI{
		validate: func(_ context.Context, source []byte) (coreservice.Validation, error) {
			if string(source) != string(policySource) {
				t.Fatalf("ValidatePolicy source = %s", source)
			}
			return coreservice.Validation{Diagnostics: []compile.Diagnostic{{
				Code: compile.CodeInvalidDocument, Table: compile.TableDocument,
			}}}, nil
		},
		compile: func(_ context.Context, source []byte) (coreservice.PolicyMetadata, error) {
			if string(source) != string(policySource) {
				t.Fatalf("CompilePolicy source = %s", source)
			}
			return metadata, nil
		},
		evaluate: func(_ context.Context, request coreservice.EvaluationRequest, dst []byte) ([]byte, error) {
			if !request.ExplicitPolicy || request.PolicyHash != hash ||
				string(request.Requests) != string(requests) || string(request.Evidence) != string(evidence) {
				t.Fatalf("EvaluateBatch request = %+v", request)
			}
			return append(dst, `{"results":[]}`...), nil
		},
	}
	harness := newGRPCTestHarness(t, api, nil, Config{MaxMessageBytes: 1 << 20, RequestTimeout: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	validated, err := harness.client.ValidatePolicy(ctx, &verifoxxv1.ValidatePolicyRequest{SourceJson: policySource})
	if err != nil {
		t.Fatalf("ValidatePolicy() error = %v", err)
	}
	if validated.GetValid() || len(validated.GetDiagnostics()) != 1 ||
		validated.GetDiagnostics()[0].GetCode() != "invalid_document" ||
		validated.GetDiagnostics()[0].GetTable() != "document" {
		t.Fatalf("ValidatePolicy() = %+v", validated)
	}

	compiled, err := harness.client.CompilePolicy(ctx, &verifoxxv1.CompilePolicyRequest{SourceJson: policySource})
	if err != nil {
		t.Fatalf("CompilePolicy() error = %v", err)
	}
	if compiled.GetPolicy().GetName() != "policy-a" || compiled.GetPolicy().GetVersion() != "1.2.3" ||
		string(compiled.GetPolicy().GetSha256()) != string(metadata.ContentHash[:]) {
		t.Fatalf("CompilePolicy() = %+v", compiled)
	}

	evaluated, err := harness.client.EvaluateBatch(ctx, &verifoxxv1.EvaluateBatchRequest{
		RequestsJson: requests,
		EvidenceJson: evidence,
		PolicySha256: hash[:],
	})
	if err != nil {
		t.Fatalf("EvaluateBatch() error = %v", err)
	}
	if string(evaluated.GetResultJson()) != `{"results":[]}` {
		t.Fatalf("EvaluateBatch result = %s", evaluated.GetResultJson())
	}
}

func TestEvaluateStreamPreservesOrder(t *testing.T) {
	t.Parallel()

	api := &fakePolicyAPI{
		evaluate: func(_ context.Context, request coreservice.EvaluationRequest, dst []byte) ([]byte, error) {
			return append(dst, request.Requests...), nil
		},
	}
	harness := newGRPCTestHarness(t, api, nil, Config{MaxMessageBytes: 1 << 20, RequestTimeout: time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stream, err := harness.client.EvaluateStream(ctx)
	if err != nil {
		t.Fatalf("EvaluateStream() error = %v", err)
	}

	requests := [][]byte{[]byte(`{"sequence":1}`), []byte(`{"sequence":2}`), []byte(`{"sequence":3}`)}
	for _, request := range requests {
		if err := stream.Send(&verifoxxv1.EvaluateStreamRequest{RequestsJson: request, EvidenceJson: []byte(`{}`)}); err != nil {
			t.Fatalf("EvaluateStream.Send() error = %v", err)
		}
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("EvaluateStream.CloseSend() error = %v", err)
	}
	for index, want := range requests {
		response, err := stream.Recv()
		if err != nil {
			t.Fatalf("EvaluateStream.Recv(%d) error = %v", index, err)
		}
		if string(response.GetResultJson()) != string(want) {
			t.Fatalf("EvaluateStream.Recv(%d) = %s, want %s", index, response.GetResultJson(), want)
		}
	}
	if response, err := stream.Recv(); !errors.Is(err, io.EOF) || response != nil {
		t.Fatalf("EvaluateStream final Recv() = (%v, %v), want nil EOF", response, err)
	}
}

func TestGRPCLimitsDeadlinesCancellationAndStatusMapping(t *testing.T) {
	t.Parallel()

	t.Run("transport limit", func(t *testing.T) {
		harness := newGRPCTestHarness(t, &fakePolicyAPI{}, nil, Config{MaxMessageBytes: 128, RequestTimeout: time.Second})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := harness.client.ValidatePolicy(ctx, &verifoxxv1.ValidatePolicyRequest{SourceJson: make([]byte, 256)})
		assertGRPCCode(t, err, codes.ResourceExhausted)
	})

	t.Run("invalid evaluation", func(t *testing.T) {
		harness := newGRPCTestHarness(t, &fakePolicyAPI{}, nil, Config{MaxMessageBytes: 1 << 20, RequestTimeout: time.Second})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := harness.client.EvaluateBatch(ctx, &verifoxxv1.EvaluateBatchRequest{
			RequestsJson: []byte(`{}`), EvidenceJson: []byte(`{}`), PolicySha256: []byte{1},
		})
		assertGRPCCode(t, err, codes.InvalidArgument)
	})

	t.Run("empty output", func(t *testing.T) {
		api := &fakePolicyAPI{evaluate: func(context.Context, coreservice.EvaluationRequest, []byte) ([]byte, error) {
			return nil, nil
		}}
		harness := newGRPCTestHarness(t, api, nil, Config{MaxMessageBytes: 1 << 20, RequestTimeout: time.Second})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := harness.client.EvaluateBatch(ctx, validEvaluationRequest())
		assertGRPCCode(t, err, codes.Internal)
	})

	t.Run("output limit", func(t *testing.T) {
		api := &fakePolicyAPI{evaluate: func(context.Context, coreservice.EvaluationRequest, []byte) ([]byte, error) {
			return make([]byte, 129), nil
		}}
		harness := newGRPCTestHarness(t, api, nil, Config{MaxMessageBytes: 128, RequestTimeout: time.Second})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := harness.client.EvaluateBatch(ctx, validEvaluationRequest())
		assertGRPCCode(t, err, codes.ResourceExhausted)
	})

	t.Run("server deadline", func(t *testing.T) {
		api := &fakePolicyAPI{evaluate: func(ctx context.Context, _ coreservice.EvaluationRequest, _ []byte) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		harness := newGRPCTestHarness(t, api, nil, Config{MaxMessageBytes: 1 << 20, RequestTimeout: 10 * time.Millisecond})
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, err := harness.client.EvaluateBatch(ctx, validEvaluationRequest())
		assertGRPCCode(t, err, codes.DeadlineExceeded)
	})

	t.Run("client cancellation", func(t *testing.T) {
		started := make(chan struct{})
		api := &fakePolicyAPI{evaluate: func(ctx context.Context, _ coreservice.EvaluationRequest, _ []byte) ([]byte, error) {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		harness := newGRPCTestHarness(t, api, nil, Config{MaxMessageBytes: 1 << 20, RequestTimeout: time.Second})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := harness.client.EvaluateBatch(ctx, validEvaluationRequest())
			done <- err
		}()
		receiveSignal(t, started)
		cancel()
		assertGRPCCode(t, receiveError(t, done), codes.Canceled)
	})

	t.Run("service errors", func(t *testing.T) {
		api := &fakePolicyAPI{evaluate: func(_ context.Context, request coreservice.EvaluationRequest, _ []byte) ([]byte, error) {
			switch string(request.Requests) {
			case `{"invalid":true}`:
				return nil, coreservice.ErrInvalidRequest
			case `{"missing":true}`:
				return nil, coreservice.ErrPolicyNotFound
			case `{"audit":true}`:
				return nil, coreservice.ErrAuditUnavailable
			default:
				return nil, errors.New("unexpected service failure")
			}
		}}
		harness := newGRPCTestHarness(t, api, nil, Config{MaxMessageBytes: 1 << 20, RequestTimeout: time.Second})
		tests := []struct {
			requests []byte
			want     codes.Code
		}{
			{requests: []byte(`{"invalid":true}`), want: codes.InvalidArgument},
			{requests: []byte(`{"missing":true}`), want: codes.NotFound},
			{requests: []byte(`{"audit":true}`), want: codes.Unavailable},
			{requests: []byte(`{}`), want: codes.Internal},
		}
		for _, test := range tests {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			_, err := harness.client.EvaluateBatch(ctx, &verifoxxv1.EvaluateBatchRequest{
				RequestsJson: test.requests, EvidenceJson: []byte(`{}`),
			})
			cancel()
			assertGRPCCode(t, err, test.want)
		}
	})

	t.Run("stream cancellation", func(t *testing.T) {
		harness := newGRPCTestHarness(t, &fakePolicyAPI{}, nil, Config{MaxMessageBytes: 1 << 20, RequestTimeout: time.Second})
		ctx, cancel := context.WithCancel(context.Background())
		stream, err := harness.client.EvaluateStream(ctx)
		if err != nil {
			t.Fatalf("EvaluateStream() error = %v", err)
		}
		cancel()
		_, err = stream.Recv()
		assertGRPCCode(t, err, codes.Canceled)
	})
}

func TestNewRejectsInvalidDependenciesAndConfiguration(t *testing.T) {
	t.Parallel()

	gate, err := coreservice.New(1)
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	valid := Config{MaxMessageBytes: 1 << 20, RequestTimeout: time.Second}
	tests := []struct {
		api       coreservice.PolicyAPI
		admission *coreservice.Service
		name      string
		config    Config
	}{
		{name: "nil API", admission: gate, config: valid},
		{name: "nil admission", api: &fakePolicyAPI{}, config: valid},
		{name: "oversized message", api: &fakePolicyAPI{}, admission: gate, config: Config{
			MaxMessageBytes: maxMessageBytes + 1, RequestTimeout: time.Second,
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, err := New(test.api, test.admission, test.config)
			if !errors.Is(err, ErrInvalidServer) || server != nil {
				t.Fatalf("New() = (%p, %v), want nil %v", server, err, ErrInvalidServer)
			}
		})
	}
}

func TestGRPCAdmissionPrecedesEvaluationJSONScan(t *testing.T) {
	t.Parallel()

	gate, err := coreservice.New(1)
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	active, err := gate.Admit(context.Background())
	if err != nil {
		t.Fatalf("Admit(active) error = %v", err)
	}
	queuedCtx, cancelQueued := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		_, queuedErr := gate.Admit(queuedCtx)
		queued <- queuedErr
	}()
	waitGRPC(t, func() bool { return gate.Stats().Queued == 1 })
	harness := newGRPCTestHarness(t, &fakePolicyAPI{}, gate, Config{
		MaxMessageBytes: 1 << 20, RequestTimeout: time.Second,
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_, err = harness.client.EvaluateBatch(ctx, &verifoxxv1.EvaluateBatchRequest{
		RequestsJson: []byte(`{"truncated":`), EvidenceJson: []byte(`{}`),
	})
	cancel()
	assertGRPCCode(t, err, codes.Unavailable)

	cancelQueued()
	if queuedErr := receiveError(t, queued); !errors.Is(queuedErr, context.Canceled) {
		t.Fatalf("queued Admit() error = %v, want context cancellation", queuedErr)
	}
	if err := gate.Release(&active); err != nil {
		t.Fatalf("Release(active) error = %v", err)
	}
}

type fakePolicyAPI struct {
	validate func(context.Context, []byte) (coreservice.Validation, error)
	compile  func(context.Context, []byte) (coreservice.PolicyMetadata, error)
	evaluate func(context.Context, coreservice.EvaluationRequest, []byte) ([]byte, error)
	lookup   func(context.Context, [32]byte) (coreservice.PolicyMetadata, error)
	health   func(context.Context) error
}

func (api *fakePolicyAPI) ValidatePolicy(ctx context.Context, source []byte) (coreservice.Validation, error) {
	if api.validate == nil {
		return coreservice.Validation{}, nil
	}
	return api.validate(ctx, source)
}

func (api *fakePolicyAPI) CompilePolicy(ctx context.Context, source []byte) (coreservice.PolicyMetadata, error) {
	if api.compile == nil {
		return testPolicyMetadata(), nil
	}
	return api.compile(ctx, source)
}

func (api *fakePolicyAPI) EvaluateBatch(ctx context.Context, request coreservice.EvaluationRequest, dst []byte) ([]byte, error) {
	if api.evaluate == nil {
		return append(dst, `{"results":[]}`...), nil
	}
	return api.evaluate(ctx, request, dst)
}

func (api *fakePolicyAPI) LookupPolicy(ctx context.Context, hash [32]byte) (coreservice.PolicyMetadata, error) {
	if api.lookup == nil {
		return coreservice.PolicyMetadata{}, coreservice.ErrPolicyNotFound
	}
	return api.lookup(ctx, hash)
}

func (api *fakePolicyAPI) Health(ctx context.Context) error {
	if api.health == nil {
		return nil
	}
	return api.health(ctx)
}

type grpcTestHarness struct {
	client     verifoxxv1.PolicyServiceClient
	connection *grpc.ClientConn
	server     *grpc.Server
	listener   *bufconn.Listener
	done       chan error
}

func newGRPCTestHarness(t *testing.T, api coreservice.PolicyAPI, admission *coreservice.Service, config Config) *grpcTestHarness {
	t.Helper()
	if admission == nil {
		var err error
		admission, err = coreservice.New(4)
		if err != nil {
			t.Fatalf("service.New() error = %v", err)
		}
	}
	server, err := New(api, admission, config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	listener := bufconn.Listen(1 << 20)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	connection, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		server.Stop()
		_ = listener.Close()
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	harness := &grpcTestHarness{
		client: verifoxxv1.NewPolicyServiceClient(connection), connection: connection,
		server: server, listener: listener, done: done,
	}
	t.Cleanup(func() {
		_ = harness.connection.Close()
		harness.server.Stop()
		_ = harness.listener.Close()
		select {
		case serveErr := <-harness.done:
			if serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
				t.Errorf("grpc.Server.Serve() error = %v", serveErr)
			}
		case <-time.After(time.Second):
			t.Error("grpc.Server.Serve() did not stop")
		}
	})
	return harness
}

func validEvaluationRequest() *verifoxxv1.EvaluateBatchRequest {
	return &verifoxxv1.EvaluateBatchRequest{RequestsJson: []byte(`{}`), EvidenceJson: []byte(`{}`)}
}

func testPolicyMetadata() coreservice.PolicyMetadata {
	return coreservice.PolicyMetadata{
		Name: []byte("policy-a"), Version: []byte("1.2.3"), ContentHash: [32]byte{1, 2, 3, 4},
		Instructions: 12, Requirements: 3, Clauses: 7,
	}
}

func assertGRPCCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	if got := status.Code(err); got != want {
		t.Fatalf("gRPC error = %v (code %s), want %s", err, got, want)
	}
}

func receiveSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for signal")
	}
}

func receiveError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for RPC")
		return nil
	}
}

func waitGRPC(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("timed out waiting for gRPC condition")
		}
	}
}
