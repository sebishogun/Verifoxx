package httpapi

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/sebishogun/verifoxx/internal/compile"
	coreservice "github.com/sebishogun/verifoxx/internal/service"
)

func (server *Server) handleValidate(response http.ResponseWriter, request *http.Request) {
	ctx, cancel, admission, err := server.admit(request)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	defer server.release(&admission, cancel)
	body, err := readPolicyBody(ctx, response, request, server.config.MaxBodyBytes)
	if err != nil {
		writePolicyBodyError(response, err)
		return
	}
	validation, err := server.api.ValidatePolicy(ctx, body)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	status := http.StatusOK
	if len(validation.Diagnostics) != 0 {
		status = http.StatusUnprocessableEntity
	}
	writeValidation(response, status, validation.Diagnostics)
}

func (server *Server) handleCompile(response http.ResponseWriter, request *http.Request) {
	ctx, cancel, admission, err := server.admit(request)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	defer server.release(&admission, cancel)
	body, err := readPolicyBody(ctx, response, request, server.config.MaxBodyBytes)
	if err != nil {
		writePolicyBodyError(response, err)
		return
	}
	metadata, err := server.api.CompilePolicy(ctx, body)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	writePolicyMetadata(response, metadata)
}

func (server *Server) handlePolicy(response http.ResponseWriter, request *http.Request, encodedHash string) {
	var hash [32]byte
	if !decodeHash(&hash, encodedHash) {
		writeError(response, http.StatusBadRequest, "invalid_request", "policy hash is invalid")
		return
	}
	ctx, cancel, admission, err := server.admit(request)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	defer server.release(&admission, cancel)
	metadata, err := server.api.LookupPolicy(ctx, hash)
	if err != nil {
		writeServiceError(response, err)
		return
	}
	writePolicyMetadata(response, metadata)
}

func (server *Server) handleHealth(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), server.config.RequestTimeout)
	defer cancel()
	if err := server.api.Health(ctx); err != nil {
		writeBytes(response, http.StatusServiceUnavailable, []byte("{\"status\":\"unavailable\"}\n"))
		return
	}
	writeBytes(response, http.StatusOK, []byte("{\"status\":\"ok\"}\n"))
}

func readPolicyBody(ctx context.Context, response http.ResponseWriter, request *http.Request, limit int64) ([]byte, error) {
	body, err := readRequestBody(ctx, response, request, limit)
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' || !json.Valid(trimmed) {
		return nil, errInvalidJSON
	}
	return body, nil
}

func writePolicyBodyError(response http.ResponseWriter, err error) {
	if err == errInvalidJSON || err == errBodyRead {
		writeError(response, http.StatusBadRequest, "invalid_json", "policy body is malformed JSON")
		return
	}
	writeBodyError(response, err)
}

type validationResponse struct {
	Diagnostics []diagnosticResponse `json:"diagnostics"`
	Valid       bool                 `json:"valid"`
}

type diagnosticResponse struct {
	Code   string         `json:"code"`
	Table  string         `json:"table"`
	Member string         `json:"member"`
	Row    uint32         `json:"row"`
	Span   diagnosticSpan `json:"span"`
	IDs    diagnosticIDs  `json:"ids"`
}

type diagnosticSpan struct {
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
}

type diagnosticIDs struct {
	Node          uint32 `json:"node,omitempty"`
	Clause        uint32 `json:"clause,omitempty"`
	Requirement   uint32 `json:"requirement,omitempty"`
	Field         uint32 `json:"field,omitempty"`
	Value         uint32 `json:"value,omitempty"`
	Outcome       uint32 `json:"outcome,omitempty"`
	Remediation   uint32 `json:"remediation,omitempty"`
	EvidenceKind  uint32 `json:"evidence_kind,omitempty"`
	EvidenceState uint32 `json:"evidence_state,omitempty"`
}

func writeValidation(response http.ResponseWriter, status int, diagnostics []compile.Diagnostic) {
	encoded := make([]diagnosticResponse, len(diagnostics))
	for index, diagnostic := range diagnostics {
		encoded[index] = diagnosticResponse{
			Code:   diagnostic.Code.String(),
			Table:  diagnostic.Table.String(),
			Member: diagnostic.Member.String(),
			Row:    diagnostic.Row,
			Span:   diagnosticSpan{Start: diagnostic.Span.Start, End: diagnostic.Span.End},
			IDs: diagnosticIDs{
				Node: uint32(diagnostic.Node), Clause: uint32(diagnostic.Clause),
				Requirement: uint32(diagnostic.Requirement), Field: uint32(diagnostic.Field),
				Value: uint32(diagnostic.Value), Outcome: uint32(diagnostic.Outcome),
				Remediation: uint32(diagnostic.Remediation), EvidenceKind: uint32(diagnostic.EvidenceKind),
				EvidenceState: uint32(diagnostic.EvidenceState),
			},
		}
	}
	if encoded == nil {
		encoded = []diagnosticResponse{}
	}
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(validationResponse{Valid: len(diagnostics) == 0, Diagnostics: encoded})
}

type policyResponse struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	SHA256       string `json:"sha256"`
	Instructions uint32 `json:"instructions"`
	Requirements uint32 `json:"requirements"`
	Clauses      uint32 `json:"clauses"`
}

func writePolicyMetadata(response http.ResponseWriter, metadata coreservice.PolicyMetadata) {
	if len(metadata.Name) == 0 || len(metadata.Version) == 0 || metadata.ContentHash == [32]byte{} {
		writeServiceError(response, coreservice.ErrUnavailable)
		return
	}
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(policyResponse{
		Name: string(metadata.Name), Version: string(metadata.Version),
		SHA256: hex.EncodeToString(metadata.ContentHash[:]), Instructions: metadata.Instructions,
		Requirements: metadata.Requirements, Clauses: metadata.Clauses,
	})
}

func decodeHash(destination *[32]byte, source string) bool {
	if destination == nil || len(source) != 64 {
		return false
	}
	for index := range destination {
		high, ok := decodeHex(source[index*2])
		if !ok {
			return false
		}
		low, ok := decodeHex(source[index*2+1])
		if !ok {
			return false
		}
		destination[index] = high<<4 | low
	}
	return true
}

func decodeHex(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}
