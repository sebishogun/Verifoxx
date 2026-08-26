package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sebishogun/nornrune/internal/security"
	coreservice "github.com/sebishogun/nornrune/internal/service"
)

func TestSecurityRejectsOversizedPolicyBeforeService(t *testing.T) {
	t.Parallel()

	api := &fakePolicyAPI{}
	handler := newHTTPTestHandler(t, api, int64(security.MaximumRequestBytes), time.Second)
	source := `{"padding":"` + strings.Repeat("x", security.MaximumPolicyBytes) + `"}`
	response := serveJSON(handler, http.MethodPost, "/v1/policies/validate", source)
	assertHTTPError(t, response, http.StatusRequestEntityTooLarge, "body_too_large")
	if api.validateCalls.Load() != 0 {
		t.Fatalf("ValidatePolicy calls = %d, oversized policy reached service", api.validateCalls.Load())
	}
}

func TestSecurityRejectsOversizedEvaluationOutput(t *testing.T) {
	t.Parallel()

	const limit = 256
	api := &fakePolicyAPI{evaluate: func(context.Context, coreservice.EvaluationRequest, []byte) ([]byte, error) {
		return []byte(`{"padding":"` + strings.Repeat("x", limit) + `"}`), nil
	}}
	handler := newHTTPTestHandler(t, api, limit, time.Second)
	response := serveJSON(handler, http.MethodPost, "/v1/evaluate", evaluationEnvelope("", `{}`, `{}`))
	assertHTTPError(t, response, http.StatusRequestEntityTooLarge, "output_too_large")
}

func TestSecurityRejectsCompressedJSONWithoutReadingIt(t *testing.T) {
	t.Parallel()

	api := &fakePolicyAPI{}
	handler := newHTTPTestHandler(t, api, 1<<20, time.Second)
	request := httptest.NewRequest(http.MethodPost, "/v1/evaluate", strings.NewReader("compressed-secret"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Content-Encoding", "gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertHTTPError(t, response, http.StatusUnsupportedMediaType, "unsupported_media_type")
	if api.evaluateCalls.Load() != 0 {
		t.Fatalf("EvaluateBatch calls = %d, compressed body reached service", api.evaluateCalls.Load())
	}
}
