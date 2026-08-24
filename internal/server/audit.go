package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/sebishogun/verifoxx/internal/persistence"
)

type auditInput struct {
	started         time.Time
	completed       time.Time
	engineVersion   string
	requests        []byte
	evidence        []byte
	results         []byte
	policyVersionID persistence.PolicyVersionID
	sequence        uint64
	policyHash      [sha256.Size]byte
}

type auditRequestPack struct {
	Requests []json.RawMessage `json:"requests"`
}

type auditEvidencePack struct {
	Evidence []json.RawMessage `json:"evidence"`
}

type auditObjectID struct {
	ID string `json:"id"`
}

type auditResultDocument struct {
	Results []auditResult `json:"results"`
}

type auditResult struct {
	RequestID           string          `json:"request_id"`
	Decision            string          `json:"decision"`
	Rationale           string          `json:"rationale"`
	Driver              auditDriver     `json:"driver"`
	AppliedRequirements json.RawMessage `json:"requirements_applied"`
	EvidenceUsed        []string        `json:"evidence_used"`
	MissingEvidence     json.RawMessage `json:"missing_or_conflicting_evidence"`
	Assumptions         json.RawMessage `json:"assumptions"`
	Uncertainty         json.RawMessage `json:"unresolved_uncertainty"`
	Remediation         json.RawMessage `json:"remediation"`
}

type auditDriver struct {
	RequirementID string `json:"requirement_id"`
	ClauseID      string `json:"clause_id"`
	Reason        string `json:"reason"`
}

func buildAuditBatch(batch *persistence.AuditBatch, input auditInput) error {
	if batch == nil || batch.Bytes == nil || input.policyVersionID <= 0 || input.policyHash == [sha256.Size]byte{} ||
		input.engineVersion == "" || input.sequence == 0 || input.started.IsZero() || input.completed.Before(input.started) {
		return persistence.ErrInvalidAuditBatch
	}
	var requests auditRequestPack
	var evidence auditEvidencePack
	var results auditResultDocument
	if json.Unmarshal(input.requests, &requests) != nil || json.Unmarshal(input.evidence, &evidence) != nil ||
		json.Unmarshal(input.results, &results) != nil || len(requests.Requests) != len(results.Results) ||
		len(requests.Requests) == 0 || len(requests.Requests) > cap(batch.Requests.Keys) ||
		len(evidence.Evidence) > cap(batch.Evidence.Keys) || len(results.Results) > cap(batch.Findings.Decisions) ||
		uint64(len(results.Results)) > uint64(^uint32(0)) {
		return persistence.ErrAuditBatchTooLarge
	}
	resetAuditBatch(batch)
	batch.PolicyVersionID = input.policyVersionID
	batch.StartedAt = input.started
	batch.CompletedAt = input.completed
	batch.Rows = uint32(len(results.Results))

	var identity [sha256.Size * 3]byte
	copy(identity[:sha256.Size], input.policyHash[:])
	requestHash := sha256.Sum256(input.requests)
	evidenceHash := sha256.Sum256(input.evidence)
	copy(identity[sha256.Size:sha256.Size*2], requestHash[:])
	copy(identity[sha256.Size*2:], evidenceHash[:])
	idempotencyHash := sha256.Sum256(identity[:])
	start := len(batch.Bytes)
	if cap(batch.Bytes)-start < hex.EncodedLen(len(idempotencyHash))+42 {
		return persistence.ErrAuditBatchTooLarge
	}
	batch.Bytes = hex.AppendEncode(batch.Bytes, idempotencyHash[:])
	batch.Bytes = append(batch.Bytes, '-')
	batch.Bytes = strconv.AppendInt(batch.Bytes, input.started.UnixNano(), 36)
	batch.Bytes = append(batch.Bytes, '-')
	batch.Bytes = strconv.AppendUint(batch.Bytes, input.sequence, 36)
	batch.IdempotencyKey = persistence.ByteRange{Start: uint32(start), End: uint32(len(batch.Bytes))}
	var err error
	if batch.EngineVersion, err = appendAuditRange(batch, []byte(input.engineVersion)); err != nil {
		return err
	}
	if batch.ExecutionMetadata, err = appendAuditRange(batch, []byte(`{"mode":"service"}`)); err != nil {
		return err
	}

	requestIDs := make([]string, len(requests.Requests))
	for row, payload := range requests.Requests {
		var metadata auditObjectID
		if json.Unmarshal(payload, &metadata) != nil || !validAuditID(metadata.ID, 'R') || containsString(requestIDs[:row], metadata.ID) {
			return persistence.ErrInvalidAuditBatch
		}
		requestIDs[row] = metadata.ID
		key, err := appendAuditRange(batch, []byte(metadata.ID))
		if err != nil {
			return err
		}
		encoded, err := appendAuditRange(batch, bytes.TrimSpace(payload))
		if err != nil {
			return err
		}
		batch.Requests.Keys = append(batch.Requests.Keys, key)
		batch.Requests.Payloads = append(batch.Requests.Payloads, encoded)
		batch.Requests.Hashes = append(batch.Requests.Hashes, sha256.Sum256(encoded.Bytes(batch.Bytes)))
		batch.Requests.CapturedAt = append(batch.Requests.CapturedAt, input.started)
	}

	evidenceIDs := make([]string, len(evidence.Evidence))
	for row, payload := range evidence.Evidence {
		var metadata auditObjectID
		if json.Unmarshal(payload, &metadata) != nil || !validAuditID(metadata.ID, 'E') || containsString(evidenceIDs[:row], metadata.ID) {
			return persistence.ErrInvalidAuditBatch
		}
		evidenceIDs[row] = metadata.ID
		key, err := appendAuditRange(batch, []byte(metadata.ID))
		if err != nil {
			return err
		}
		encoded, err := appendAuditRange(batch, bytes.TrimSpace(payload))
		if err != nil {
			return err
		}
		batch.Evidence.Keys = append(batch.Evidence.Keys, key)
		batch.Evidence.Payloads = append(batch.Evidence.Payloads, encoded)
		batch.Evidence.Hashes = append(batch.Evidence.Hashes, sha256.Sum256(encoded.Bytes(batch.Bytes)))
		batch.Evidence.CapturedAt = append(batch.Evidence.CapturedAt, input.started)
		batch.Evidence.ExpiresAt = append(batch.Evidence.ExpiresAt, time.Time{})
	}

	batch.Findings.EvidenceOffsets = append(batch.Findings.EvidenceOffsets, 0)
	seenRequests := make([]bool, len(requestIDs))
	for _, finding := range results.Results {
		requestRow := stringRow(requestIDs, finding.RequestID)
		decision, ok := auditDecision(finding.Decision)
		if requestRow < 0 || seenRequests[requestRow] || !ok || finding.Rationale == "" ||
			!validAuditID(finding.Driver.RequirementID, 'R') || !validAuditID(finding.Driver.ClauseID, 'C') ||
			finding.Driver.Reason == "" || !validJSONArray(finding.AppliedRequirements) ||
			!validJSONArray(finding.MissingEvidence) || !validJSONArray(finding.Assumptions) ||
			!validJSONArray(finding.Uncertainty) || !validJSONArray(finding.Remediation) {
			return persistence.ErrInvalidAuditBatch
		}
		seenRequests[requestRow] = true
		batch.Findings.RequestRows = append(batch.Findings.RequestRows, uint32(requestRow))
		batch.Findings.Decisions = append(batch.Findings.Decisions, decision)
		var err error
		batch.Findings.Rationales, err = appendAuditFinding(batch, batch.Findings.Rationales, []byte(finding.Rationale))
		if err != nil {
			return err
		}
		batch.Findings.DriverRequirementIDs, err = appendAuditFinding(batch, batch.Findings.DriverRequirementIDs, []byte(finding.Driver.RequirementID))
		if err != nil {
			return err
		}
		batch.Findings.DriverClauseIDs, err = appendAuditFinding(batch, batch.Findings.DriverClauseIDs, []byte(finding.Driver.ClauseID))
		if err != nil {
			return err
		}
		batch.Findings.DriverReasons, err = appendAuditFinding(batch, batch.Findings.DriverReasons, []byte(finding.Driver.Reason))
		if err != nil {
			return err
		}
		batch.Findings.AppliedRequirements, err = appendAuditFinding(batch, batch.Findings.AppliedRequirements, finding.AppliedRequirements)
		if err != nil {
			return err
		}
		batch.Findings.MissingEvidence, err = appendAuditFinding(batch, batch.Findings.MissingEvidence, finding.MissingEvidence)
		if err != nil {
			return err
		}
		batch.Findings.Assumptions, err = appendAuditFinding(batch, batch.Findings.Assumptions, finding.Assumptions)
		if err != nil {
			return err
		}
		batch.Findings.Uncertainty, err = appendAuditFinding(batch, batch.Findings.Uncertainty, finding.Uncertainty)
		if err != nil {
			return err
		}
		batch.Findings.Remediation, err = appendAuditFinding(batch, batch.Findings.Remediation, finding.Remediation)
		if err != nil {
			return err
		}
		for _, id := range finding.EvidenceUsed {
			evidenceRow := stringRow(evidenceIDs, id)
			if evidenceRow < 0 || len(batch.Findings.EvidenceRows) == cap(batch.Findings.EvidenceRows) {
				return persistence.ErrAuditBatchTooLarge
			}
			batch.Findings.EvidenceRows = append(batch.Findings.EvidenceRows, uint32(evidenceRow))
		}
		batch.Findings.EvidenceOffsets = append(batch.Findings.EvidenceOffsets, uint32(len(batch.Findings.EvidenceRows)))
	}
	if err := persistence.ValidateAuditBatch(batch); err != nil {
		return err
	}
	return nil
}

func resetAuditBatch(batch *persistence.AuditBatch) {
	batch.Bytes = batch.Bytes[:0]
	batch.Requests.Keys = batch.Requests.Keys[:0]
	batch.Requests.Payloads = batch.Requests.Payloads[:0]
	batch.Requests.Hashes = batch.Requests.Hashes[:0]
	batch.Requests.CapturedAt = batch.Requests.CapturedAt[:0]
	batch.Requests.ResolvedIDs = batch.Requests.ResolvedIDs[:0]
	batch.Evidence.Keys = batch.Evidence.Keys[:0]
	batch.Evidence.Payloads = batch.Evidence.Payloads[:0]
	batch.Evidence.Hashes = batch.Evidence.Hashes[:0]
	batch.Evidence.CapturedAt = batch.Evidence.CapturedAt[:0]
	batch.Evidence.ExpiresAt = batch.Evidence.ExpiresAt[:0]
	batch.Evidence.ResolvedIDs = batch.Evidence.ResolvedIDs[:0]
	batch.Findings.Rationales = batch.Findings.Rationales[:0]
	batch.Findings.DriverRequirementIDs = batch.Findings.DriverRequirementIDs[:0]
	batch.Findings.DriverClauseIDs = batch.Findings.DriverClauseIDs[:0]
	batch.Findings.DriverReasons = batch.Findings.DriverReasons[:0]
	batch.Findings.AppliedRequirements = batch.Findings.AppliedRequirements[:0]
	batch.Findings.MissingEvidence = batch.Findings.MissingEvidence[:0]
	batch.Findings.Assumptions = batch.Findings.Assumptions[:0]
	batch.Findings.Uncertainty = batch.Findings.Uncertainty[:0]
	batch.Findings.Remediation = batch.Findings.Remediation[:0]
	batch.Findings.RequestRows = batch.Findings.RequestRows[:0]
	batch.Findings.EvidenceOffsets = batch.Findings.EvidenceOffsets[:0]
	batch.Findings.EvidenceRows = batch.Findings.EvidenceRows[:0]
	batch.Findings.Decisions = batch.Findings.Decisions[:0]
}

func appendAuditRange(batch *persistence.AuditBatch, value []byte) (persistence.ByteRange, error) {
	if len(value) == 0 || len(value) > cap(batch.Bytes)-len(batch.Bytes) ||
		uint64(len(batch.Bytes))+uint64(len(value)) > uint64(^uint32(0)) {
		return persistence.ByteRange{}, persistence.ErrAuditBatchTooLarge
	}
	start := len(batch.Bytes)
	batch.Bytes = append(batch.Bytes, value...)
	return persistence.ByteRange{Start: uint32(start), End: uint32(len(batch.Bytes))}, nil
}

func appendAuditFinding(
	batch *persistence.AuditBatch,
	destination []persistence.ByteRange,
	value []byte,
) ([]persistence.ByteRange, error) {
	if len(destination) == cap(destination) {
		return destination, persistence.ErrAuditBatchTooLarge
	}
	encoded, err := appendAuditRange(batch, bytes.TrimSpace(value))
	if err != nil {
		return destination, err
	}
	return append(destination, encoded), nil
}

func validAuditID(value string, prefix byte) bool {
	if len(value) < 2 || value[0] != prefix || value[1] == '0' {
		return false
	}
	for index := 1; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func validJSONArray(value []byte) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) >= 2 && trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']' && json.Valid(trimmed)
}

func containsString(values []string, target string) bool {
	return stringRow(values, target) >= 0
}

func stringRow(values []string, target string) int {
	for row, value := range values {
		if value == target {
			return row
		}
	}
	return -1
}

func auditDecision(value string) (persistence.Decision, bool) {
	switch value {
	case "Approve":
		return persistence.DecisionApprove, true
	case "Reject":
		return persistence.DecisionReject, true
	case "Revise":
		return persistence.DecisionRevise, true
	case "Escalate":
		return persistence.DecisionEscalate, true
	default:
		return 0, false
	}
}
