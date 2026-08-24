package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	verifoxxv1 "github.com/sebishogun/verifoxx/api/gen/verifoxx/v1"
	"github.com/sebishogun/verifoxx/internal/fixtures"
	"google.golang.org/grpc"
)

func TestPartitionRequestsIsExact(t *testing.T) {
	got := make([]uint64, 4)
	partitionRequests(got, 14)
	want := [...]uint64{4, 4, 3, 3}
	var total uint64
	for worker := range got {
		total += got[worker]
		if got[worker] != want[worker] {
			t.Fatalf("partitionRequests()[%d] = %d, want %d", worker, got[worker], want[worker])
		}
	}
	if total != 14 {
		t.Fatalf("partitionRequests() total = %d", total)
	}
}

func TestExecuteHTTPCompletesFixedBudget(t *testing.T) {
	var calls atomic.Uint64
	var active atomic.Int32
	var maximum atomic.Int32
	var invalid atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		updateMaximum(&maximum, current)
		body, err := io.ReadAll(request.Body)
		if err != nil || request.Method != http.MethodPost || request.URL.Path != "/v1/evaluate" ||
			request.Header.Get("Content-Type") != "application/json" ||
			!bytes.Contains(body, []byte(`"requests"`)) || !bytes.Contains(body, []byte(`"evidence"`)) {
			invalid.Store(true)
		}
		calls.Add(1)
		time.Sleep(time.Millisecond)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{}`))
	}))
	defer server.Close()

	report, err := executeForTest(t, "-protocol=http", "-target="+strings.TrimPrefix(server.URL, "http://"),
		"-requests=14", "-concurrency=4", "-timeout=1s")
	if err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if calls.Load() != 14 || invalid.Load() || maximum.Load() < 2 || maximum.Load() > 4 {
		t.Fatalf("HTTP calls = %d invalid=%v max-concurrency=%d", calls.Load(), invalid.Load(), maximum.Load())
	}
	assertReport(t, report, "http", 14, 14, 4)
}

func TestExecuteGRPCCompletesFixedBudget(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	service := &loadTestPolicyServer{}
	server := grpc.NewServer()
	verifoxxv1.RegisterPolicyServiceServer(server, service)
	serveDone := make(chan struct{})
	go func() {
		_ = server.Serve(listener)
		close(serveDone)
	}()
	t.Cleanup(func() {
		server.Stop()
		<-serveDone
	})

	report, err := executeForTest(t, "-protocol=grpc", "-target="+listener.Addr().String(),
		"-requests=12", "-concurrency=3", "-timeout=1s")
	if err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if service.calls.Load() != 12 || service.invalid.Load() {
		t.Fatalf("gRPC calls = %d invalid=%v", service.calls.Load(), service.invalid.Load())
	}
	assertReport(t, report, "grpc", 12, 12, 3)
}

func TestExecuteCancelsOnFirstResponseFailure(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "status", status: http.StatusServiceUnavailable, body: `{}`},
		{name: "malformed JSON", status: http.StatusOK, body: `{`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Uint64
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				response.WriteHeader(test.status)
				_, _ = response.Write([]byte(test.body))
			}))
			defer server.Close()

			report, err := executeForTest(t, "-protocol=http", "-target="+strings.TrimPrefix(server.URL, "http://"),
				"-requests=20", "-concurrency=1", "-timeout=1s")
			if err == nil {
				t.Fatal("execute() error = nil")
			}
			if calls.Load() != 1 || report.CompletedRequests != 0 {
				t.Fatalf("failed run = calls %d completed %d", calls.Load(), report.CompletedRequests)
			}
		})
	}
}

func TestExecuteTimeoutIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
		case <-time.After(100 * time.Millisecond):
		}
	}))
	defer server.Close()
	started := time.Now()
	report, err := executeForTest(t, "-protocol=http", "-target="+strings.TrimPrefix(server.URL, "http://"),
		"-requests=20", "-concurrency=2", "-timeout=20ms")
	if err == nil {
		t.Fatal("execute() error = nil")
	}
	if elapsed := time.Since(started); elapsed > 300*time.Millisecond {
		t.Fatalf("execute() elapsed = %s, want <= 300ms", elapsed)
	}
	if report.CompletedRequests != 0 {
		t.Fatalf("completed requests = %d", report.CompletedRequests)
	}
}

func TestExecuteRejectsInvalidOptions(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"protocol", []string{"-protocol=tcp"}},
		{"target URL", []string{"-target=http://127.0.0.1:8080"}},
		{"target port", []string{"-target=127.0.0.1:0"}},
		{"requests zero", []string{"-requests=0"}},
		{"requests too large", []string{"-requests=1000001"}},
		{"timeout zero", []string{"-timeout=0"}},
		{"timeout too large", []string{"-timeout=24h"}},
		{"concurrency zero", []string{"-concurrency=0"}},
		{"concurrency exceeds requests", []string{"-requests=2", "-concurrency=3"}},
		{"concurrency too large", []string{"-requests=1000", "-concurrency=257"}},
		{"positional argument", []string{"extra"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := execute(ctx, test.args, io.Discard, io.Discard); err == nil {
				t.Fatal("execute() error = nil")
			}
		})
	}
}

func executeForTest(t *testing.T, args ...string) (loadReport, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var output bytes.Buffer
	err := execute(ctx, args, &output, io.Discard)
	var report loadReport
	if decodeErr := json.Unmarshal(output.Bytes(), &report); decodeErr != nil {
		t.Fatalf("decode report %q: %v", output.String(), decodeErr)
	}
	return report, err
}

func assertReport(t *testing.T, report loadReport, protocol string, requested, completed uint64, concurrency int) {
	t.Helper()
	if report.Protocol != protocol || report.RequestedRequests != requested || report.CompletedRequests != completed ||
		report.Concurrency != concurrency || report.ElapsedNanoseconds <= 0 || report.RequestsPerSecond <= 0 {
		t.Fatalf("report = %+v", report)
	}
}

func updateMaximum(maximum *atomic.Int32, value int32) {
	for current := maximum.Load(); value > current && !maximum.CompareAndSwap(current, value); current = maximum.Load() {
	}
}

type loadTestPolicyServer struct {
	verifoxxv1.UnimplementedPolicyServiceServer
	calls   atomic.Uint64
	invalid atomic.Bool
}

func (server *loadTestPolicyServer) EvaluateBatch(
	_ context.Context,
	request *verifoxxv1.EvaluateBatchRequest,
) (*verifoxxv1.EvaluateBatchResponse, error) {
	server.calls.Add(1)
	if !bytes.Equal(request.GetRequestsJson(), []byte(fixtures.RequestsJSON())) ||
		!bytes.Equal(request.GetEvidenceJson(), []byte(fixtures.EvidenceJSON())) {
		server.invalid.Store(true)
	}
	return &verifoxxv1.EvaluateBatchResponse{ResultJson: []byte(`{}`)}, nil
}
