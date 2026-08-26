// Package httpapi exposes the policy service through a bounded JSON HTTP API.
package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/sebishogun/nornrune/internal/observability"
	coreservice "github.com/sebishogun/nornrune/internal/service"
)

const policyPathPrefix = "/v1/policies/"

// Server routes requests over transport-independent service ports.
type Server struct {
	api       coreservice.PolicyAPI
	metrics   *observability.Metrics
	health    *observability.Health
	admission *coreservice.Service
	config    Config
}

// New validates dependencies without starting listeners or goroutines.
func New(
	api coreservice.PolicyAPI,
	admission *coreservice.Service,
	metrics *observability.Metrics,
	config Config,
) (*Server, error) {
	if api == nil || admission == nil || metrics == nil || admission.Stats().Limit == 0 || !config.valid() {
		return nil, errInvalidServerConfig
	}
	health, err := observability.NewHealth(api, admission)
	if err != nil {
		return nil, errInvalidServerConfig
	}
	return &Server{api: api, metrics: metrics, health: health, admission: admission, config: config}, nil
}

// ServeHTTP implements exact routing so all failures retain the JSON contract.
func (server *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if server == nil || server.api == nil || server.metrics == nil || server.health == nil || server.admission == nil ||
		response == nil || request == nil {
		return
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	response.Header().Set("Cache-Control", "no-store")
	if request.URL == nil || request.URL.RawQuery != "" {
		writeError(response, http.StatusBadRequest, "invalid_request", "request URL is invalid")
		return
	}

	switch request.URL.Path {
	case "/v1/policies/validate":
		if !requireMethod(response, request, http.MethodPost) || !requireJSON(response, request) {
			return
		}
		server.handleValidate(response, request)
	case "/v1/policies/compile":
		if !requireMethod(response, request, http.MethodPost) || !requireJSON(response, request) {
			return
		}
		server.handleCompile(response, request)
	case "/v1/evaluate":
		if !requireMethod(response, request, http.MethodPost) || !requireJSON(response, request) {
			return
		}
		server.handleEvaluate(response, request)
	case "/healthz":
		if !requireMethod(response, request, http.MethodGet) {
			return
		}
		server.handleReadiness(response, request)
	case "/readyz":
		if !requireMethod(response, request, http.MethodGet) {
			return
		}
		server.handleReadiness(response, request)
	case "/livez":
		if !requireMethod(response, request, http.MethodGet) {
			return
		}
		server.handleLiveness(response, request)
	case "/metrics":
		if !requireMethod(response, request, http.MethodGet) {
			return
		}
		response.Header().Del("Content-Type")
		server.metrics.ServeHTTP(response, request)
	default:
		if strings.HasPrefix(request.URL.Path, policyPathPrefix) {
			if !requireMethod(response, request, http.MethodGet) {
				return
			}
			server.handlePolicy(response, request, request.URL.Path[len(policyPathPrefix):])
			return
		}
		writeError(response, http.StatusNotFound, "not_found", "resource not found")
	}
}

func requireMethod(response http.ResponseWriter, request *http.Request, method string) bool {
	if request.Method == method {
		return true
	}
	response.Header().Set("Allow", method)
	writeError(response, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	return false
}

func requireJSON(response http.ResponseWriter, request *http.Request) bool {
	if requireJSONContentType(request) {
		return true
	}
	writeError(response, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
	return false
}

func (server *Server) admit(request *http.Request) (context.Context, context.CancelFunc, coreservice.Admission, error) {
	ctx, cancel := context.WithTimeout(request.Context(), server.config.RequestTimeout)
	admission, err := server.admission.Admit(ctx)
	if err != nil {
		cancel()
		return nil, nil, coreservice.Admission{}, err
	}
	return ctx, cancel, admission, nil
}

func (server *Server) release(admission *coreservice.Admission, cancel context.CancelFunc) {
	_ = server.admission.Release(admission)
	cancel()
}

func writeServiceError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		writeError(response, http.StatusGatewayTimeout, "deadline_exceeded", "request deadline exceeded")
	case errors.Is(err, context.Canceled):
		writeError(response, http.StatusRequestTimeout, "request_canceled", "request canceled")
	case errors.Is(err, coreservice.ErrInvalidRequest):
		writeError(response, http.StatusBadRequest, "invalid_request", "request is invalid")
	case errors.Is(err, coreservice.ErrInvalidPolicy):
		writeError(response, http.StatusUnprocessableEntity, "invalid_policy", "policy validation failed")
	case errors.Is(err, coreservice.ErrPolicyNotFound):
		writeError(response, http.StatusNotFound, "policy_not_found", "policy not found")
	case errors.Is(err, coreservice.ErrAuditUnavailable):
		response.Header().Set("Retry-After", "1")
		writeError(response, http.StatusServiceUnavailable, "audit_unavailable", "required audit is unavailable")
	case errors.Is(err, coreservice.ErrServiceBusy):
		response.Header().Set("Retry-After", "1")
		writeError(response, http.StatusServiceUnavailable, "service_busy", "service admission is full")
	case errors.Is(err, coreservice.ErrServiceStopping), errors.Is(err, coreservice.ErrUnavailable):
		response.Header().Set("Retry-After", "1")
		writeError(response, http.StatusServiceUnavailable, "service_unavailable", "service is unavailable")
	default:
		writeError(response, http.StatusInternalServerError, "internal_error", "internal service error")
	}
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	response.WriteHeader(status)
	_, _ = io.WriteString(response, `{"error":{"code":"`)
	_, _ = io.WriteString(response, code)
	_, _ = io.WriteString(response, `","message":"`)
	_, _ = io.WriteString(response, message)
	_, _ = io.WriteString(response, `"}}`+"\n")
}

func writeBytes(response http.ResponseWriter, status int, body []byte) {
	response.WriteHeader(status)
	_, _ = response.Write(body)
}
