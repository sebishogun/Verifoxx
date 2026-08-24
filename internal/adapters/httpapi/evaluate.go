package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	coreservice "github.com/sebishogun/verifoxx/internal/service"
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

	body, err := readRequestBody(ctx, response, request, server.config.MaxBodyBytes)
	if err != nil {
		writeBodyError(response, err)
		return
	}
	evaluation, err := decodeEvaluationRequest(body)
	if err != nil {
		if errors.Is(err, errInvalidJSON) {
			writeError(response, http.StatusBadRequest, "invalid_json", "request body is malformed JSON")
		} else {
			writeError(response, http.StatusBadRequest, "invalid_request", "evaluation envelope is invalid")
		}
		return
	}
	encoded, err := server.api.EvaluateBatch(ctx, evaluation, body[:0])
	if err != nil {
		writeServiceError(response, err)
		return
	}
	if int64(len(encoded)) > server.config.MaxBodyBytes {
		writeError(response, http.StatusRequestEntityTooLarge, "output_too_large", "evaluation output exceeds limit")
		return
	}
	if len(encoded) == 0 || !json.Valid(encoded) {
		writeServiceError(response, coreservice.ErrUnavailable)
		return
	}
	writeBytes(response, http.StatusOK, encoded)
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
