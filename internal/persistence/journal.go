package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/sebishogun/nornrune/internal/security"
)

var (
	// ErrInvalidJournal reports an invalid journal, configuration, dependency, or call.
	ErrInvalidJournal = errors.New("persistence: invalid audit journal")
	// ErrInvalidAuditBatch reports malformed audit columns or references.
	ErrInvalidAuditBatch = errors.New("persistence: invalid audit batch")
	// ErrAuditBatchTooLarge reports input beyond a journal's fixed storage budget.
	ErrAuditBatchTooLarge = errors.New("persistence: audit batch too large")
	// ErrJournalQueueFull reports a dropped best-effort audit submission.
	ErrJournalQueueFull = errors.New("persistence: audit journal queue full")
	// ErrJournalClosed reports Submit after journal shutdown began.
	ErrJournalClosed = errors.New("persistence: audit journal closed")
)

// AuditMode fixes persistence behavior for one Journal.
type AuditMode uint8

const (
	AuditOff AuditMode = iota
	AuditBestEffort
	AuditRequired
)

// Valid reports whether mode is a supported audit contract.
func (mode AuditMode) Valid() bool {
	return mode <= AuditRequired
}

// Decision is the durable spelling of one evaluation outcome.
type Decision uint8

const (
	DecisionApprove Decision = iota + 1
	DecisionReject
	DecisionRevise
	DecisionEscalate
)

// Valid reports whether decision can be stored by the audit schema.
func (decision Decision) Valid() bool {
	return decision >= DecisionApprove && decision <= DecisionEscalate
}

// String returns the schema's exact decision spelling.
func (decision Decision) String() string {
	switch decision {
	case DecisionApprove:
		return "Approve"
	case DecisionReject:
		return "Reject"
	case DecisionRevise:
		return "Revise"
	case DecisionEscalate:
		return "Escalate"
	default:
		return ""
	}
}

// ByteRange indexes one value in AuditBatch.Bytes. An empty range represents
// a nullable text value where the schema permits null.
type ByteRange struct {
	Start uint32
	End   uint32
}

// Bytes returns the indexed value or nil for an invalid range.
func (value ByteRange) Bytes(data []byte) []byte {
	if value.Start > value.End || uint64(value.End) > uint64(len(data)) {
		return nil
	}
	return data[value.Start:value.End]
}

// RequestSnapshots stores unique immutable request metadata in SoA form.
type RequestSnapshots struct {
	Keys        []ByteRange
	Payloads    []ByteRange
	Hashes      [][sha256.Size]byte
	CapturedAt  []time.Time
	ResolvedIDs []int64
}

// EvidenceSnapshots stores unique immutable evidence/provenance in SoA form.
// A zero ExpiresAt value represents SQL NULL.
type EvidenceSnapshots struct {
	Keys        []ByteRange
	Payloads    []ByteRange
	Hashes      [][sha256.Size]byte
	CapturedAt  []time.Time
	ExpiresAt   []time.Time
	ResolvedIDs []int64
}

// AuditFindings stores one decision per request row plus CSR evidence links.
type AuditFindings struct {
	Rationales           []ByteRange
	DriverRequirementIDs []ByteRange
	DriverClauseIDs      []ByteRange
	DriverReasons        []ByteRange
	AppliedRequirements  []ByteRange
	MissingEvidence      []ByteRange
	Assumptions          []ByteRange
	Uncertainty          []ByteRange
	Remediation          []ByteRange
	RequestRows          []uint32
	EvidenceOffsets      []uint32
	EvidenceRows         []uint32
	Decisions            []Decision
}

// AuditBatch is a complete, database-ready audit record. All text and JSON
// columns index one byte slab; typed columns retain their natural layout.
type AuditBatch struct {
	StartedAt         time.Time
	CompletedAt       time.Time
	Findings          AuditFindings
	Evidence          EvidenceSnapshots
	Requests          RequestSnapshots
	Bytes             []byte
	PolicyVersionID   PolicyVersionID
	IdempotencyKey    ByteRange
	EngineVersion     ByteRange
	ExecutionMetadata ByteRange
	Rows              uint32
}

// AuditCapacity fixes all storage allocated for one reusable journal slot.
type AuditCapacity struct {
	Bytes         int
	Requests      int
	Evidence      int
	Rows          int
	EvidenceLinks int
}

// AuditStore atomically persists one complete slot-owned batch.
type AuditStore interface {
	Append(context.Context, *AuditBatch) error
}

// NewAuditBatch allocates one empty reusable batch with fixed capacity.
func NewAuditBatch(capacity AuditCapacity) (AuditBatch, error) {
	if !validAuditCapacity(capacity) {
		return AuditBatch{}, fmt.Errorf("%w: audit capacity", ErrInvalidJournal)
	}
	return AuditBatch{
		Bytes: make([]byte, 0, capacity.Bytes),
		Requests: RequestSnapshots{
			Keys:        make([]ByteRange, 0, capacity.Requests),
			Payloads:    make([]ByteRange, 0, capacity.Requests),
			Hashes:      make([][sha256.Size]byte, 0, capacity.Requests),
			CapturedAt:  make([]time.Time, 0, capacity.Requests),
			ResolvedIDs: make([]int64, 0, capacity.Requests),
		},
		Evidence: EvidenceSnapshots{
			Keys:        make([]ByteRange, 0, capacity.Evidence),
			Payloads:    make([]ByteRange, 0, capacity.Evidence),
			Hashes:      make([][sha256.Size]byte, 0, capacity.Evidence),
			CapturedAt:  make([]time.Time, 0, capacity.Evidence),
			ExpiresAt:   make([]time.Time, 0, capacity.Evidence),
			ResolvedIDs: make([]int64, 0, capacity.Evidence),
		},
		Findings: AuditFindings{
			Rationales:           make([]ByteRange, 0, capacity.Rows),
			DriverRequirementIDs: make([]ByteRange, 0, capacity.Rows),
			DriverClauseIDs:      make([]ByteRange, 0, capacity.Rows),
			DriverReasons:        make([]ByteRange, 0, capacity.Rows),
			AppliedRequirements:  make([]ByteRange, 0, capacity.Rows),
			MissingEvidence:      make([]ByteRange, 0, capacity.Rows),
			Assumptions:          make([]ByteRange, 0, capacity.Rows),
			Uncertainty:          make([]ByteRange, 0, capacity.Rows),
			Remediation:          make([]ByteRange, 0, capacity.Rows),
			RequestRows:          make([]uint32, 0, capacity.Rows),
			EvidenceOffsets:      make([]uint32, 0, capacity.Rows+1),
			EvidenceRows:         make([]uint32, 0, capacity.EvidenceLinks),
			Decisions:            make([]Decision, 0, capacity.Rows),
		},
	}, nil
}

func validAuditCapacity(capacity AuditCapacity) bool {
	if capacity.Bytes <= 0 || capacity.Requests <= 0 || capacity.Rows <= 0 ||
		capacity.Evidence < 0 || capacity.EvidenceLinks < 0 {
		return false
	}
	maxInt := uint64(^uint(0) >> 1)
	maxUint32 := uint64(^uint32(0))
	return uint64(capacity.Bytes) <= min(maxInt, maxUint32) &&
		uint64(capacity.Requests) <= min(maxInt/sha256.Size, maxUint32) &&
		uint64(capacity.Evidence) <= min(maxInt/sha256.Size, maxUint32) &&
		uint64(capacity.Rows) <= min(maxInt/8, maxUint32) &&
		uint64(capacity.EvidenceLinks) <= min(maxInt/4, maxUint32)
}

// CopyAuditBatch validates and copies source into preallocated destination
// storage. It never retains caller-owned slices.
func CopyAuditBatch(destination, source *AuditBatch) error {
	if destination == nil || source == nil {
		return fmt.Errorf("%w: nil batch", ErrInvalidAuditBatch)
	}
	if err := ValidateAuditBatch(source); err != nil {
		return err
	}
	if !auditBatchFits(destination, source) {
		return ErrAuditBatchTooLarge
	}

	destination.Bytes = append(destination.Bytes[:0], source.Bytes...)
	copyRequestSnapshots(&destination.Requests, &source.Requests)
	copyEvidenceSnapshots(&destination.Evidence, &source.Evidence)
	copyAuditFindings(&destination.Findings, &source.Findings)
	destination.StartedAt = source.StartedAt
	destination.CompletedAt = source.CompletedAt
	destination.IdempotencyKey = source.IdempotencyKey
	destination.EngineVersion = source.EngineVersion
	destination.ExecutionMetadata = source.ExecutionMetadata
	destination.PolicyVersionID = source.PolicyVersionID
	destination.Rows = source.Rows
	return nil
}

func auditBatchFits(destination, source *AuditBatch) bool {
	return len(source.Bytes) <= cap(destination.Bytes) &&
		len(source.Requests.Keys) <= cap(destination.Requests.Keys) &&
		len(source.Requests.Payloads) <= cap(destination.Requests.Payloads) &&
		len(source.Requests.Hashes) <= cap(destination.Requests.Hashes) &&
		len(source.Requests.CapturedAt) <= cap(destination.Requests.CapturedAt) &&
		len(source.Requests.Keys) <= cap(destination.Requests.ResolvedIDs) &&
		len(source.Evidence.Keys) <= cap(destination.Evidence.Keys) &&
		len(source.Evidence.Payloads) <= cap(destination.Evidence.Payloads) &&
		len(source.Evidence.Hashes) <= cap(destination.Evidence.Hashes) &&
		len(source.Evidence.CapturedAt) <= cap(destination.Evidence.CapturedAt) &&
		len(source.Evidence.ExpiresAt) <= cap(destination.Evidence.ExpiresAt) &&
		len(source.Evidence.Keys) <= cap(destination.Evidence.ResolvedIDs) &&
		len(source.Findings.Rationales) <= cap(destination.Findings.Rationales) &&
		len(source.Findings.DriverRequirementIDs) <= cap(destination.Findings.DriverRequirementIDs) &&
		len(source.Findings.DriverClauseIDs) <= cap(destination.Findings.DriverClauseIDs) &&
		len(source.Findings.DriverReasons) <= cap(destination.Findings.DriverReasons) &&
		len(source.Findings.AppliedRequirements) <= cap(destination.Findings.AppliedRequirements) &&
		len(source.Findings.MissingEvidence) <= cap(destination.Findings.MissingEvidence) &&
		len(source.Findings.Assumptions) <= cap(destination.Findings.Assumptions) &&
		len(source.Findings.Uncertainty) <= cap(destination.Findings.Uncertainty) &&
		len(source.Findings.Remediation) <= cap(destination.Findings.Remediation) &&
		len(source.Findings.RequestRows) <= cap(destination.Findings.RequestRows) &&
		len(source.Findings.EvidenceOffsets) <= cap(destination.Findings.EvidenceOffsets) &&
		len(source.Findings.EvidenceRows) <= cap(destination.Findings.EvidenceRows) &&
		len(source.Findings.Decisions) <= cap(destination.Findings.Decisions)
}

func copyRequestSnapshots(destination, source *RequestSnapshots) {
	destination.Keys = append(destination.Keys[:0], source.Keys...)
	destination.Payloads = append(destination.Payloads[:0], source.Payloads...)
	destination.Hashes = append(destination.Hashes[:0], source.Hashes...)
	destination.CapturedAt = append(destination.CapturedAt[:0], source.CapturedAt...)
	destination.ResolvedIDs = destination.ResolvedIDs[:len(source.Keys)]
	clear(destination.ResolvedIDs)
}

func copyEvidenceSnapshots(destination, source *EvidenceSnapshots) {
	destination.Keys = append(destination.Keys[:0], source.Keys...)
	destination.Payloads = append(destination.Payloads[:0], source.Payloads...)
	destination.Hashes = append(destination.Hashes[:0], source.Hashes...)
	destination.CapturedAt = append(destination.CapturedAt[:0], source.CapturedAt...)
	destination.ExpiresAt = append(destination.ExpiresAt[:0], source.ExpiresAt...)
	destination.ResolvedIDs = destination.ResolvedIDs[:len(source.Keys)]
	clear(destination.ResolvedIDs)
}

func copyAuditFindings(destination, source *AuditFindings) {
	destination.Rationales = append(destination.Rationales[:0], source.Rationales...)
	destination.DriverRequirementIDs = append(destination.DriverRequirementIDs[:0], source.DriverRequirementIDs...)
	destination.DriverClauseIDs = append(destination.DriverClauseIDs[:0], source.DriverClauseIDs...)
	destination.DriverReasons = append(destination.DriverReasons[:0], source.DriverReasons...)
	destination.AppliedRequirements = append(destination.AppliedRequirements[:0], source.AppliedRequirements...)
	destination.MissingEvidence = append(destination.MissingEvidence[:0], source.MissingEvidence...)
	destination.Assumptions = append(destination.Assumptions[:0], source.Assumptions...)
	destination.Uncertainty = append(destination.Uncertainty[:0], source.Uncertainty...)
	destination.Remediation = append(destination.Remediation[:0], source.Remediation...)
	destination.RequestRows = append(destination.RequestRows[:0], source.RequestRows...)
	destination.EvidenceOffsets = append(destination.EvidenceOffsets[:0], source.EvidenceOffsets...)
	destination.EvidenceRows = append(destination.EvidenceRows[:0], source.EvidenceRows...)
	destination.Decisions = append(destination.Decisions[:0], source.Decisions...)
}

// ValidateAuditBatch checks all column shapes, content, and references without
// decoding JSON into object graphs.
func ValidateAuditBatch(batch *AuditBatch) error {
	if batch == nil || len(batch.Bytes) == 0 || uint64(len(batch.Bytes)) > uint64(^uint32(0)) ||
		batch.PolicyVersionID <= 0 || batch.StartedAt.IsZero() || batch.CompletedAt.Before(batch.StartedAt) ||
		!validRequiredAuditText(batch.Bytes, batch.IdempotencyKey, true) ||
		!validRequiredAuditText(batch.Bytes, batch.EngineVersion, true) ||
		!validAuditJSON(batch.Bytes, batch.ExecutionMetadata, '{', '}') ||
		!validRequestSnapshots(batch) || !validEvidenceSnapshots(batch) || !validAuditFindings(batch) {
		return ErrInvalidAuditBatch
	}
	return nil
}

func validRequestSnapshots(batch *AuditBatch) bool {
	requests := &batch.Requests
	count := len(requests.Keys)
	if count != len(requests.Payloads) || count != len(requests.Hashes) || count != len(requests.CapturedAt) {
		return false
	}
	for row := range count {
		payload := requests.Payloads[row].Bytes(batch.Bytes)
		if !validRequiredAuditText(batch.Bytes, requests.Keys[row], true) ||
			!validAuditJSON(batch.Bytes, requests.Payloads[row], '{', '}') || security.ContainsProtectedRows(payload) ||
			requests.CapturedAt[row].IsZero() {
			return false
		}
		digest := sha256.Sum256(payload)
		if subtle.ConstantTimeCompare(digest[:], requests.Hashes[row][:]) != 1 {
			return false
		}
	}
	return true
}

func validEvidenceSnapshots(batch *AuditBatch) bool {
	evidence := &batch.Evidence
	count := len(evidence.Keys)
	if count != len(evidence.Payloads) || count != len(evidence.Hashes) ||
		count != len(evidence.CapturedAt) || count != len(evidence.ExpiresAt) {
		return false
	}
	for row := range count {
		payload := evidence.Payloads[row].Bytes(batch.Bytes)
		expires := evidence.ExpiresAt[row]
		if !validRequiredAuditText(batch.Bytes, evidence.Keys[row], true) ||
			!validAuditJSON(batch.Bytes, evidence.Payloads[row], '{', '}') || security.ContainsProtectedRows(payload) ||
			evidence.CapturedAt[row].IsZero() ||
			(!expires.IsZero() && expires.Before(evidence.CapturedAt[row])) {
			return false
		}
		digest := sha256.Sum256(payload)
		if subtle.ConstantTimeCompare(digest[:], evidence.Hashes[row][:]) != 1 {
			return false
		}
	}
	return true
}

func validAuditFindings(batch *AuditBatch) bool {
	findings := &batch.Findings
	rows := int(batch.Rows)
	if rows != len(findings.RequestRows) || rows != len(findings.Decisions) ||
		rows != len(findings.Rationales) || rows != len(findings.DriverRequirementIDs) ||
		rows != len(findings.DriverClauseIDs) || rows != len(findings.DriverReasons) ||
		rows != len(findings.AppliedRequirements) || rows != len(findings.MissingEvidence) ||
		rows != len(findings.Assumptions) || rows != len(findings.Uncertainty) ||
		rows != len(findings.Remediation) || len(findings.EvidenceOffsets) != rows+1 ||
		len(findings.EvidenceOffsets) == 0 || findings.EvidenceOffsets[0] != 0 ||
		uint64(findings.EvidenceOffsets[rows]) != uint64(len(findings.EvidenceRows)) {
		return false
	}
	for row := range rows {
		if uint64(findings.RequestRows[row]) >= uint64(len(batch.Requests.Keys)) || !findings.Decisions[row].Valid() ||
			!validRequiredAuditText(batch.Bytes, findings.Rationales[row], false) ||
			!validOptionalAuditText(batch.Bytes, findings.DriverRequirementIDs[row]) ||
			!validOptionalAuditText(batch.Bytes, findings.DriverClauseIDs[row]) ||
			!validOptionalAuditText(batch.Bytes, findings.DriverReasons[row]) ||
			!validAuditJSON(batch.Bytes, findings.AppliedRequirements[row], '[', ']') ||
			!validAuditJSON(batch.Bytes, findings.MissingEvidence[row], '[', ']') ||
			!validAuditJSON(batch.Bytes, findings.Assumptions[row], '[', ']') ||
			!validAuditJSON(batch.Bytes, findings.Uncertainty[row], '[', ']') ||
			!validAuditJSON(batch.Bytes, findings.Remediation[row], '[', ']') ||
			findings.EvidenceOffsets[row] > findings.EvidenceOffsets[row+1] {
			return false
		}
	}
	for _, evidenceRow := range findings.EvidenceRows {
		if uint64(evidenceRow) >= uint64(len(batch.Evidence.Keys)) {
			return false
		}
	}
	return true
}

func validRequiredAuditText(data []byte, value ByteRange, exactTrim bool) bool {
	text := value.Bytes(data)
	if len(text) == 0 || len(bytes.TrimSpace(text)) == 0 {
		return false
	}
	return !exactTrim || len(bytes.TrimSpace(text)) == len(text)
}

func validOptionalAuditText(data []byte, value ByteRange) bool {
	if value.Start == value.End {
		return value.Bytes(data) != nil
	}
	return validRequiredAuditText(data, value, false)
}

func validAuditJSON(data []byte, value ByteRange, opening, closing byte) bool {
	encoded := value.Bytes(data)
	if len(encoded) == 0 || !json.Valid(encoded) {
		return false
	}
	trimmed := bytes.TrimSpace(encoded)
	return len(trimmed) >= 2 && trimmed[0] == opening && trimmed[len(trimmed)-1] == closing
}
