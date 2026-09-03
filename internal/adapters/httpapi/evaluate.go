package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	coreservice "github.com/sebishogun/nornrune/internal/service"
	publictelemetry "github.com/sebishogun/nornrune/telemetry"
)

var (
	errInvalidJSON     = errors.New("httpapi: invalid JSON")
	errInvalidEnvelope = errors.New("httpapi: invalid evaluation envelope")
)

func (server *Server) handleEvaluate(response http.ResponseWriter, request *http.Request) {
	ctx, cancel, admission, err := server.admit(request)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	defer server.release(&admission, cancel)

	decodeContext, decodeSpan := server.config.Telemetry.Start(ctx, publictelemetry.OperationDecode, publictelemetry.TransportHTTP)
	body, err := readRequestBody(decodeContext, response, request, server.config.MaxBodyBytes)
	if err != nil {
		server.config.Telemetry.Finish(decodeSpan, traceStatus(err))
		writeBodyError(response, err)
		return
	}
	evaluation, err := decodeEvaluationRequest(body)
	if err != nil {
		server.config.Telemetry.Finish(decodeSpan, publictelemetry.SpanStatusInvalidArgument)
		if errors.Is(err, errInvalidJSON) {
			writeError(response, http.StatusBadRequest, "invalid_json", "request body is malformed JSON")
		} else {
			writeError(response, http.StatusBadRequest, "invalid_request", "evaluation envelope is invalid")
		}
		return
	}
	server.config.Telemetry.Finish(decodeSpan, publictelemetry.SpanStatusOK)
	evaluationContext, evaluationSpan := server.config.Telemetry.Start(ctx, publictelemetry.OperationEvaluation, publictelemetry.TransportHTTP)
	encoded, err := server.api.EvaluateBatch(evaluationContext, evaluation, body[:0])
	if err != nil {
		server.config.Telemetry.Finish(evaluationSpan, traceStatus(err))
		writeServiceError(response, err)
		return
	}
	if int64(len(encoded)) > server.config.MaxBodyBytes {
		server.config.Telemetry.Finish(evaluationSpan, publictelemetry.SpanStatusResourceExhausted)
		writeError(response, http.StatusRequestEntityTooLarge, "output_too_large", "evaluation output exceeds limit")
		return
	}
	if len(encoded) == 0 || !json.Valid(encoded) {
		server.config.Telemetry.Finish(evaluationSpan, publictelemetry.SpanStatusUnavailable)
		writeServiceError(response, coreservice.ErrUnavailable)
		return
	}
	server.config.Telemetry.Finish(evaluationSpan, publictelemetry.SpanStatusOK)
	_, encodeSpan := server.config.Telemetry.Start(ctx, publictelemetry.OperationResponseEncode, publictelemetry.TransportHTTP)
	writeBytes(response, http.StatusOK, encoded)
	server.config.Telemetry.Finish(encodeSpan, publictelemetry.SpanStatusOK)
}

func decodeEvaluationRequest(body []byte) (coreservice.EvaluationRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil {
		return coreservice.EvaluationRequest{}, errInvalidJSON
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return coreservice.EvaluationRequest{}, errInvalidEnvelope
	}
	var result coreservice.EvaluationRequest
	var seen uint8
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return coreservice.EvaluationRequest{}, errInvalidJSON
		}
		name, ok := token.(string)
		if !ok {
			return coreservice.EvaluationRequest{}, errInvalidEnvelope
		}
		var bit uint8
		switch name {
		case "requests":
			bit = 1
			if err := decoder.Decode((*json.RawMessage)(&result.Requests)); err != nil {
				return coreservice.EvaluationRequest{}, errInvalidJSON
			}
		case "evidence":
			bit = 2
			if err := decoder.Decode((*json.RawMessage)(&result.Evidence)); err != nil {
				return coreservice.EvaluationRequest{}, errInvalidJSON
			}
		case "policy_sha256":
			bit = 4
			var encoded string
			if err := decoder.Decode(&encoded); err != nil {
				return coreservice.EvaluationRequest{}, errInvalidEnvelope
			}
			if !decodeHash(&result.PolicyHash, encoded) {
				return coreservice.EvaluationRequest{}, errInvalidEnvelope
			}
			result.ExplicitPolicy = true
		default:
			return coreservice.EvaluationRequest{}, errInvalidEnvelope
		}
		if seen&bit != 0 {
			return coreservice.EvaluationRequest{}, errInvalidEnvelope
		}
		seen |= bit
	}
	if token, err = decoder.Token(); err != nil {
		return coreservice.EvaluationRequest{}, errInvalidJSON
	} else if delimiter, ok = token.(json.Delim); !ok || delimiter != '}' {
		return coreservice.EvaluationRequest{}, errInvalidEnvelope
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return coreservice.EvaluationRequest{}, errInvalidJSON
	}
	if seen&3 != 3 || !jsonObject(result.Requests) || !jsonObject(result.Evidence) {
		return coreservice.EvaluationRequest{}, errInvalidEnvelope
	}
	return result, nil
}

func jsonObject(value []byte) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}'
}

func writeBodyError(response http.ResponseWriter, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		writeServiceError(response, err)
		return
	}
	if errors.Is(err, errBodyTooLarge) {
		writeError(response, http.StatusRequestEntityTooLarge, "body_too_large", "request body exceeds limit")
		return
	}
	writeError(response, http.StatusBadRequest, "invalid_json", "request body is unreadable")
}
