package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sebishogun/verifoxx/internal/compile"
	"github.com/sebishogun/verifoxx/internal/fixtures"
	"github.com/sebishogun/verifoxx/internal/observability"
	coreservice "github.com/sebishogun/verifoxx/internal/service"
)

func TestPolicyValidateAndCompileHandlers(t *testing.T) {
	t.Parallel()

	policySource := []byte(`{"schema_version":1}`)
	api := &fakePolicyAPI{
		validate: func(_ context.Context, source []byte) (coreservice.Validation, error) {
			if string(source) != string(policySource) {
				t.Fatalf("ValidatePolicy source = %s", source)
			}
			return coreservice.Validation{}, nil
		},
		compile: func(_ context.Context, source []byte) (coreservice.PolicyMetadata, error) {
			if string(source) != string(policySource) {
				t.Fatalf("CompilePolicy source = %s", source)
			}
			return testPolicyMetadata(), nil
		},
	}
	handler := newHTTPTestHandler(t, api, 1<<20, time.Second)

	validated := serveJSON(handler, http.MethodPost, "/v1/policies/validate", string(policySource))
	if validated.Code != http.StatusOK || validated.Header().Get("Content-Type") != "application/json" ||
		validated.Body.String() != "{\"diagnostics\":[],\"valid\":true}\n" {
		t.Fatalf("validate response = %d %q %s", validated.Code, validated.Header(), validated.Body.String())
	}

	compiled := serveJSON(handler, http.MethodPost, "/v1/policies/compile", string(policySource))
	if compiled.Code != http.StatusOK || !strings.Contains(compiled.Body.String(), `"sha256":"0102030400000000000000000000000000000000000000000000000000000000"`) ||
		!strings.Contains(compiled.Body.String(), `"name":"policy-a"`) {
		t.Fatalf("compile response = %d %s", compiled.Code, compiled.Body.String())
	}
}

func TestPolicyValidationDiagnosticsAndFailures(t *testing.T) {
	t.Parallel()

	api := &fakePolicyAPI{
		validate: func(context.Context, []byte) (coreservice.Validation, error) {
			return coreservice.Validation{Diagnostics: []compile.Diagnostic{{
				Code:  compile.CodeInvalidDocument,
				Table: compile.TableDocument,
			}}}, nil
		},
		compile: func(context.Context, []byte) (coreservice.PolicyMetadata, error) {
			return coreservice.PolicyMetadata{}, coreservice.ErrInvalidPolicy
		},
	}
	handler := newHTTPTestHandler(t, api, 1<<20, time.Second)

	invalid := serveJSON(handler, http.MethodPost, "/v1/policies/validate", `{}`)
	if invalid.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(invalid.Body.String(), `"code":"invalid_document"`) ||
		!strings.Contains(invalid.Body.String(), `"table":"document"`) {
		t.Fatalf("invalid validation response = %d %s", invalid.Code, invalid.Body.String())
	}
	compileFailure := serveJSON(handler, http.MethodPost, "/v1/policies/compile", `{}`)
	assertHTTPError(t, compileFailure, http.StatusUnprocessableEntity, "invalid_policy")

	malformed := serveJSON(handler, http.MethodPost, "/v1/policies/validate", `{`)
	assertHTTPError(t, malformed, http.StatusBadRequest, "invalid_json")
	if api.validateCalls.Load() != 1 {
		t.Fatalf("ValidatePolicy calls = %d, malformed JSON reached backend", api.validateCalls.Load())
	}
}

func TestEvaluateHandler(t *testing.T) {
	t.Parallel()

	wantHash := [32]byte{0xaa, 0xbb, 0xcc}
	api := &fakePolicyAPI{
		evaluate: func(_ context.Context, request coreservice.EvaluationRequest, dst []byte) ([]byte, error) {
			if !request.ExplicitPolicy || request.PolicyHash != wantHash {
				t.Fatalf("EvaluateBatch policy = explicit:%v hash:%x", request.ExplicitPolicy, request.PolicyHash)
			}
			if len(dst) != 0 || cap(dst) == 0 {
				t.Fatalf("EvaluateBatch destination = len:%d cap:%d", len(dst), cap(dst))
			}
			encoded := append(dst, "{\"schema_version\":1,\"results\":[]}\n"...)
			if string(request.Requests) != fixtures.RequestsJSON() || string(request.Evidence) != fixtures.EvidenceJSON() {
				t.Fatal("output destination aliases evaluation input")
			}
			return encoded, nil
		},
	}
	handler := newHTTPTestHandler(t, api, 1<<20, time.Second)
	body := evaluationEnvelope(hexHash(wantHash), fixtures.RequestsJSON(), fixtures.EvidenceJSON())

	response := serveJSON(handler, http.MethodPost, "/v1/evaluate", body)
	if response.Code != http.StatusOK || response.Body.String() != "{\"schema_version\":1,\"results\":[]}\n" {
		t.Fatalf("evaluate response = %d %s", response.Code, response.Body.String())
	}
}

func TestEvaluateRejectsMalformedEnvelopeAndLimits(t *testing.T) {
	t.Parallel()

	api := &fakePolicyAPI{}
	handler := newHTTPTestHandler(t, api, 64, time.Second)
	tests := []struct {
		name   string
		body   string
		code   string
		status int
	}{
		{name: "truncated", body: `{"requests":`, status: http.StatusBadRequest, code: "invalid_json"},
		{name: "unknown", body: `{"requests":{},"evidence":{},"extra":0}`, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "duplicate", body: `{"requests":{},"requests":{},"evidence":{}}`, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "missing", body: `{"requests":{}}`, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "bad hash", body: `{"policy_sha256":"xyz","requests":{},"evidence":{}}`, status: http.StatusBadRequest, code: "invalid_request"},
		{name: "too large", body: strings.Repeat(" ", 65), status: http.StatusRequestEntityTooLarge, code: "body_too_large"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := serveJSON(handler, http.MethodPost, "/v1/evaluate", test.body)
			assertHTTPError(t, response, test.status, test.code)
		})
	}
	if api.evaluateCalls.Load() != 0 {
		t.Fatalf("EvaluateBatch calls = %d for rejected requests", api.evaluateCalls.Load())
	}
}

func TestEvaluateDeadlineAuditAndAdmissionFailures(t *testing.T) {
	t.Parallel()

	t.Run("deadline", func(t *testing.T) {
		api := &fakePolicyAPI{
			evaluate: func(ctx context.Context, _ coreservice.EvaluationRequest, _ []byte) ([]byte, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}
		handler := newHTTPTestHandler(t, api, 1<<20, 10*time.Millisecond)
		response := serveJSON(handler, http.MethodPost, "/v1/evaluate", evaluationEnvelope("", `{}`, `{}`))
		assertHTTPError(t, response, http.StatusGatewayTimeout, "deadline_exceeded")
	})

	t.Run("required audit", func(t *testing.T) {
		api := &fakePolicyAPI{
			evaluate: func(context.Context, coreservice.EvaluationRequest, []byte) ([]byte, error) {
				return nil, coreservice.ErrAuditUnavailable
			},
		}
		handler := newHTTPTestHandler(t, api, 1<<20, time.Second)
		response := serveJSON(handler, http.MethodPost, "/v1/evaluate", evaluationEnvelope("", `{}`, `{}`))
		assertHTTPError(t, response, http.StatusServiceUnavailable, "audit_unavailable")
	})

	t.Run("busy", func(t *testing.T) {
		gate, err := coreservice.New(1)
		if err != nil {
			t.Fatalf("service.New() error = %v", err)
		}
		active, err := gate.Admit(context.Background())
		if err != nil {
			t.Fatalf("Admit(active) error = %v", err)
		}
		queuedCtx, cancelQueued := context.WithCancel(context.Background())
		queuedDone := make(chan error, 1)
		go func() {
			_, queuedErr := gate.Admit(queuedCtx)
			queuedDone <- queuedErr
		}()
		waitHTTP(t, func() bool { return gate.Stats().Queued == 1 })
		server, err := New(&fakePolicyAPI{}, gate, newTestMetrics(t, gate), Config{
			MaxBodyBytes: 1 << 20, RequestTimeout: time.Second,
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		response := serveJSON(server, http.MethodPost, "/v1/evaluate", evaluationEnvelope("", `{}`, `{}`))
		assertHTTPError(t, response, http.StatusServiceUnavailable, "service_busy")
		cancelQueued()
		<-queuedDone
		if err := gate.Release(&active); err != nil {
			t.Fatalf("Release(active) error = %v", err)
		}
	})
}

func TestBodyReadUsesRequestDeadline(t *testing.T) {
	t.Parallel()

	handler := newHTTPTestHandler(t, &fakePolicyAPI{}, 1<<20, 10*time.Millisecond)
	body := &deadlineBody{}
	request := httptest.NewRequest(http.MethodPost, "/v1/evaluate", nil)
	request.Header.Set("Content-Type", "application/json")
	request.Body = body
	request.ContentLength = -1
	response := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder(), body: body}

	started := time.Now()
	handler.ServeHTTP(response, request)
	elapsed := time.Since(started)
	assertHTTPError(t, response.ResponseRecorder, http.StatusGatewayTimeout, "deadline_exceeded")
	if elapsed >= 80*time.Millisecond {
		t.Fatalf("body read elapsed = %s, want request deadline", elapsed)
	}
}

func TestPolicyLookupAndHealthHandlers(t *testing.T) {
	t.Parallel()

	metadata := testPolicyMetadata()
	api := &fakePolicyAPI{
		lookup: func(_ context.Context, hash [32]byte) (coreservice.PolicyMetadata, error) {
			if hash != metadata.ContentHash {
				return coreservice.PolicyMetadata{}, coreservice.ErrPolicyNotFound
			}
			return metadata, nil
		},
	}
	handler := newHTTPTestHandler(t, api, 1<<20, time.Second)

	found := serveRequest(handler, http.MethodGet, "/v1/policies/"+hexHash(metadata.ContentHash), "", "")
	if found.Code != http.StatusOK || !strings.Contains(found.Body.String(), `"name":"policy-a"`) {
		t.Fatalf("policy lookup response = %d %s", found.Code, found.Body.String())
	}
	missing := serveRequest(handler, http.MethodGet, "/v1/policies/"+strings.Repeat("0", 64), "", "")
	assertHTTPError(t, missing, http.StatusNotFound, "policy_not_found")
	malformed := serveRequest(handler, http.MethodGet, "/v1/policies/not-a-hash", "", "")
	assertHTTPError(t, malformed, http.StatusBadRequest, "invalid_request")

	healthy := serveRequest(handler, http.MethodGet, "/healthz", "", "")
	if healthy.Code != http.StatusOK || healthy.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("healthy response = %d %s", healthy.Code, healthy.Body.String())
	}
	api.health = func(context.Context) error { return coreservice.ErrUnavailable }
	unhealthy := serveRequest(handler, http.MethodGet, "/healthz", "", "")
	if unhealthy.Code != http.StatusServiceUnavailable || unhealthy.Body.String() != "{\"status\":\"unavailable\"}\n" {
		t.Fatalf("unhealthy response = %d %s", unhealthy.Code, unhealthy.Body.String())
	}
}

func TestMetricsReadinessAndLivenessHandlers(t *testing.T) {
	t.Parallel()

	gate, err := coreservice.New(2)
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	api := &fakePolicyAPI{}
	metrics := newTestMetrics(t, gate)
	server, err := New(api, gate, metrics, Config{MaxBodyBytes: 1 << 20, RequestTimeout: time.Second})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	scrape := serveRequest(server, http.MethodGet, "/metrics", "", "")
	if scrape.Code != http.StatusOK || !strings.HasPrefix(scrape.Header().Get("Content-Type"), "text/plain") ||
		!strings.Contains(scrape.Body.String(), "verifoxx_evaluation_workers 2\n") {
		t.Fatalf("metrics response = %d %q %s", scrape.Code, scrape.Header(), scrape.Body.String())
	}
	ready := serveRequest(server, http.MethodGet, "/readyz", "", "")
	if ready.Code != http.StatusOK || ready.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("ready response = %d %s", ready.Code, ready.Body.String())
	}
	api.health = func(context.Context) error { return coreservice.ErrUnavailable }
	unready := serveRequest(server, http.MethodGet, "/readyz", "", "")
	if unready.Code != http.StatusServiceUnavailable {
		t.Fatalf("unready response = %d %s", unready.Code, unready.Body.String())
	}
	live := serveRequest(server, http.MethodGet, "/livez", "", "")
	if live.Code != http.StatusOK || live.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Fatalf("live response = %d %s", live.Code, live.Body.String())
	}
	if err := gate.StopAdmission(); err != nil {
		t.Fatalf("StopAdmission() error = %v", err)
	}
	live = serveRequest(server, http.MethodGet, "/livez", "", "")
	if live.Code != http.StatusOK {
		t.Fatalf("live response after stop = %d %s", live.Code, live.Body.String())
	}
	unready = serveRequest(server, http.MethodGet, "/healthz", "", "")
	if unready.Code != http.StatusServiceUnavailable {
		t.Fatalf("health response after stop = %d %s", unready.Code, unready.Body.String())
	}
	method := serveRequest(server, http.MethodPost, "/metrics", "", "")
	assertHTTPError(t, method, http.StatusMethodNotAllowed, "method_not_allowed")
	for _, path := range []string{"/healthz", "/readyz", "/livez"} {
		method = serveRequest(server, http.MethodPost, path, "", "")
		assertHTTPError(t, method, http.StatusMethodNotAllowed, "method_not_allowed")
	}
}

func TestHTTPProtocolErrors(t *testing.T) {
	t.Parallel()

	handler := newHTTPTestHandler(t, &fakePolicyAPI{}, 1<<20, time.Second)
	method := serveRequest(handler, http.MethodGet, "/v1/evaluate", "", "")
	assertHTTPError(t, method, http.StatusMethodNotAllowed, "method_not_allowed")
	if method.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("Allow = %q, want POST", method.Header().Get("Allow"))
	}
	media := serveRequest(handler, http.MethodPost, "/v1/evaluate", `{}`, "text/plain")
	assertHTTPError(t, media, http.StatusUnsupportedMediaType, "unsupported_media_type")
	unknown := serveRequest(handler, http.MethodGet, "/missing", "", "")
	assertHTTPError(t, unknown, http.StatusNotFound, "not_found")
}

func TestNewRejectsUnboundedBodyConfiguration(t *testing.T) {
	t.Parallel()

	gate, err := coreservice.New(1)
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	server, err := New(&fakePolicyAPI{}, gate, newTestMetrics(t, gate), Config{
		MaxBodyBytes:   maxBodyBytes + 1,
		RequestTimeout: time.Second,
	})
	if err != errInvalidServerConfig || server != nil {
		t.Fatalf("New(oversized body limit) = (%p, %v), want nil %v", server, err, errInvalidServerConfig)
	}
	server, err = New(&fakePolicyAPI{}, gate, nil, Config{MaxBodyBytes: 1 << 20, RequestTimeout: time.Second})
	if err != errInvalidServerConfig || server != nil {
		t.Fatalf("New(nil metrics) = (%p, %v), want nil %v", server, err, errInvalidServerConfig)
	}
}

type fakePolicyAPI struct {
	validate      func(context.Context, []byte) (coreservice.Validation, error)
	compile       func(context.Context, []byte) (coreservice.PolicyMetadata, error)
	evaluate      func(context.Context, coreservice.EvaluationRequest, []byte) ([]byte, error)
	lookup        func(context.Context, [32]byte) (coreservice.PolicyMetadata, error)
	health        func(context.Context) error
	validateCalls atomic.Uint64
	compileCalls  atomic.Uint64
	evaluateCalls atomic.Uint64
	lookupCalls   atomic.Uint64
}

func (api *fakePolicyAPI) ValidatePolicy(ctx context.Context, source []byte) (coreservice.Validation, error) {
	api.validateCalls.Add(1)
	if api.validate == nil {
		return coreservice.Validation{}, nil
	}
	return api.validate(ctx, source)
}

func (api *fakePolicyAPI) CompilePolicy(ctx context.Context, source []byte) (coreservice.PolicyMetadata, error) {
	api.compileCalls.Add(1)
	if api.compile == nil {
		return coreservice.PolicyMetadata{}, nil
	}
	return api.compile(ctx, source)
}

func (api *fakePolicyAPI) EvaluateBatch(ctx context.Context, request coreservice.EvaluationRequest, dst []byte) ([]byte, error) {
	api.evaluateCalls.Add(1)
	if api.evaluate == nil {
		return append(dst, `{"results":[]}`...), nil
	}
	return api.evaluate(ctx, request, dst)
}

func (api *fakePolicyAPI) LookupPolicy(ctx context.Context, hash [32]byte) (coreservice.PolicyMetadata, error) {
	api.lookupCalls.Add(1)
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

func newHTTPTestHandler(t *testing.T, api coreservice.PolicyAPI, maxBody int64, timeout time.Duration) http.Handler {
	t.Helper()
	gate, err := coreservice.New(4)
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	server, err := New(api, gate, newTestMetrics(t, gate), Config{MaxBodyBytes: maxBody, RequestTimeout: timeout})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return server
}

func newTestMetrics(t *testing.T, gate *coreservice.Service) *observability.Metrics {
	t.Helper()
	metrics, err := observability.NewMetrics(observability.MetricsConfig{
		QueueDepth: func() uint64 {
			return uint64(gate.Stats().Queued)
		},
		JournalFailures: func() uint64 { return 0 },
		SIMDTier:        "test",
		Workers:         uint32(gate.Stats().Limit),
	})
	if err != nil {
		t.Fatalf("observability.NewMetrics() error = %v", err)
	}
	return metrics
}

func serveJSON(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	return serveRequest(handler, method, path, body, "application/json")
}

func serveRequest(handler http.Handler, method, path, body, contentType string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertHTTPError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status || !strings.Contains(response.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("HTTP error = %d %s, want status %d code %q", response.Code, response.Body.String(), status, code)
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
	}
}

func testPolicyMetadata() coreservice.PolicyMetadata {
	return coreservice.PolicyMetadata{
		Name:         []byte("policy-a"),
		Version:      []byte("1.2.3"),
		ContentHash:  [32]byte{1, 2, 3, 4},
		Instructions: 12,
		Requirements: 3,
		Clauses:      7,
	}
}

func evaluationEnvelope(hash, requests, evidence string) string {
	body := `{"requests":` + requests + `,"evidence":` + evidence
	if hash != "" {
		body += `,"policy_sha256":"` + hash + `"`
	}
	return body + `}`
}

func hexHash(hash [32]byte) string {
	const digits = "0123456789abcdef"
	encoded := make([]byte, 64)
	for index, value := range hash {
		encoded[index*2] = digits[value>>4]
		encoded[index*2+1] = digits[value&0xf]
	}
	return string(encoded)
}

func waitHTTP(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("timed out waiting for HTTP condition")
		}
	}
}

type deadlineBody struct {
	deadline time.Time
}

func (body *deadlineBody) Read([]byte) (int, error) {
	deadline := body.deadline
	if deadline.IsZero() {
		deadline = time.Now().Add(100 * time.Millisecond)
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	<-timer.C
	return 0, readDeadlineError{}
}

func (*deadlineBody) Close() error { return nil }

type readDeadlineError struct{}

func (readDeadlineError) Error() string   { return "read deadline exceeded" }
func (readDeadlineError) Timeout() bool   { return true }
func (readDeadlineError) Temporary() bool { return true }

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	body *deadlineBody
}

func (recorder *deadlineRecorder) SetReadDeadline(deadline time.Time) error {
	recorder.body.deadline = deadline
	return nil
}
