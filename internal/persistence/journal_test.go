package persistence

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

func appendAuditText(batch *AuditBatch, value string) ByteRange {
	start := len(batch.Bytes)
	batch.Bytes = append(batch.Bytes, value...)
	return ByteRange{Start: uint32(start), End: uint32(len(batch.Bytes))}
}

func testAuditBatch() AuditBatch {
	started := time.Unix(1_777_777_700, 0).UTC()
	batch := AuditBatch{
		PolicyVersionID: 1,
		StartedAt:       started,
		CompletedAt:     started.Add(25 * time.Millisecond),
		Rows:            1,
	}
	batch.IdempotencyKey = appendAuditText(&batch, "audit-1")
	batch.EngineVersion = appendAuditText(&batch, "engine-1")
	batch.ExecutionMetadata = appendAuditText(&batch, `{"simd":"scalar"}`)

	requestPayload := appendAuditText(&batch, `{"request":"R1"}`)
	batch.Requests.Keys = append(batch.Requests.Keys, appendAuditText(&batch, "R1"))
	batch.Requests.Payloads = append(batch.Requests.Payloads, requestPayload)
	batch.Requests.Hashes = append(batch.Requests.Hashes, sha256.Sum256(requestPayload.Bytes(batch.Bytes)))
	batch.Requests.CapturedAt = append(batch.Requests.CapturedAt, started.Add(-time.Second))

	evidencePayload := appendAuditText(&batch, `{"approval":"A1"}`)
	batch.Evidence.Keys = append(batch.Evidence.Keys, appendAuditText(&batch, "A1"))
	batch.Evidence.Payloads = append(batch.Evidence.Payloads, evidencePayload)
	batch.Evidence.Hashes = append(batch.Evidence.Hashes, sha256.Sum256(evidencePayload.Bytes(batch.Bytes)))
	batch.Evidence.CapturedAt = append(batch.Evidence.CapturedAt, started.Add(-time.Minute))
	batch.Evidence.ExpiresAt = append(batch.Evidence.ExpiresAt, started.Add(time.Hour))

	batch.Findings.RequestRows = append(batch.Findings.RequestRows, 0)
	batch.Findings.Decisions = append(batch.Findings.Decisions, DecisionApprove)
	batch.Findings.Rationales = append(batch.Findings.Rationales, appendAuditText(&batch, "approved by R1"))
	batch.Findings.DriverRequirementIDs = append(batch.Findings.DriverRequirementIDs, appendAuditText(&batch, "R1"))
	batch.Findings.DriverClauseIDs = append(batch.Findings.DriverClauseIDs, appendAuditText(&batch, "C1"))
	batch.Findings.DriverReasons = append(batch.Findings.DriverReasons, appendAuditText(&batch, "satisfied"))
	batch.Findings.AppliedRequirements = append(batch.Findings.AppliedRequirements, appendAuditText(&batch, `["R1"]`))
	batch.Findings.MissingEvidence = append(batch.Findings.MissingEvidence, appendAuditText(&batch, `[]`))
	batch.Findings.Assumptions = append(batch.Findings.Assumptions, appendAuditText(&batch, `[]`))
	batch.Findings.Uncertainty = append(batch.Findings.Uncertainty, appendAuditText(&batch, `[]`))
	batch.Findings.Remediation = append(batch.Findings.Remediation, appendAuditText(&batch, `[]`))
	batch.Findings.EvidenceOffsets = append(batch.Findings.EvidenceOffsets, 0, 1)
	batch.Findings.EvidenceRows = append(batch.Findings.EvidenceRows, 0)
	return batch
}

func testAuditCapacity() AuditCapacity {
	return AuditCapacity{Bytes: 1024, Requests: 2, Evidence: 2, Rows: 2, EvidenceLinks: 4}
}

func TestValidateAuditBatch(t *testing.T) {
	if err := ValidateAuditBatch(&[]AuditBatch{testAuditBatch()}[0]); err != nil {
		t.Fatalf("ValidateAuditBatch() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*AuditBatch)
	}{
		{name: "nil_bytes", mutate: func(batch *AuditBatch) { batch.Bytes = nil }},
		{name: "zero_policy_version", mutate: func(batch *AuditBatch) { batch.PolicyVersionID = 0 }},
		{name: "padded_idempotency_key", mutate: func(batch *AuditBatch) {
			batch.Bytes[batch.IdempotencyKey.Start] = ' '
		}},
		{name: "invalid_metadata", mutate: func(batch *AuditBatch) {
			batch.Bytes[batch.ExecutionMetadata.Start] = '['
		}},
		{name: "request_shape", mutate: func(batch *AuditBatch) { batch.Requests.Keys = nil }},
		{name: "request_hash", mutate: func(batch *AuditBatch) { batch.Requests.Hashes[0][0] ^= 0xff }},
		{name: "evidence_expiry", mutate: func(batch *AuditBatch) {
			batch.Evidence.ExpiresAt[0] = batch.Evidence.CapturedAt[0].Add(-time.Second)
		}},
		{name: "finding_shape", mutate: func(batch *AuditBatch) { batch.Findings.Rationales = nil }},
		{name: "invalid_decision", mutate: func(batch *AuditBatch) { batch.Findings.Decisions[0] = 99 }},
		{name: "request_reference", mutate: func(batch *AuditBatch) { batch.Findings.RequestRows[0] = 1 }},
		{name: "evidence_offsets", mutate: func(batch *AuditBatch) { batch.Findings.EvidenceOffsets[1] = 2 }},
		{name: "evidence_reference", mutate: func(batch *AuditBatch) { batch.Findings.EvidenceRows[0] = 1 }},
		{name: "row_count", mutate: func(batch *AuditBatch) { batch.Rows = 2 }},
		{name: "time_order", mutate: func(batch *AuditBatch) { batch.CompletedAt = batch.StartedAt.Add(-time.Second) }},
		{name: "invalid_json_array", mutate: func(batch *AuditBatch) {
			batch.Bytes[batch.Findings.Assumptions[0].Start] = '{'
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			batch := testAuditBatch()
			test.mutate(&batch)
			if err := ValidateAuditBatch(&batch); !errors.Is(err, ErrInvalidAuditBatch) {
				t.Fatalf("ValidateAuditBatch() error = %v, want %v", err, ErrInvalidAuditBatch)
			}
		})
	}
}

func TestCopyAuditBatchOwnsStorageAndRespectsCapacity(t *testing.T) {
	source := testAuditBatch()
	destination, err := NewAuditBatch(testAuditCapacity())
	if err != nil {
		t.Fatalf("NewAuditBatch() error = %v", err)
	}
	if err := CopyAuditBatch(&destination, &source); err != nil {
		t.Fatalf("CopyAuditBatch() error = %v", err)
	}
	wantBytes := append([]byte(nil), destination.Bytes...)
	wantDecision := destination.Findings.Decisions[0]
	source.Bytes[0] ^= 0xff
	source.Findings.Decisions[0] = DecisionReject
	if !bytes.Equal(destination.Bytes, wantBytes) || destination.Findings.Decisions[0] != wantDecision {
		t.Fatal("destination changed after source reuse")
	}
	if err := ValidateAuditBatch(&destination); err != nil {
		t.Fatalf("copied batch invalid: %v", err)
	}

	tooSmall, err := NewAuditBatch(AuditCapacity{Bytes: 1, Requests: 1, Evidence: 1, Rows: 1, EvidenceLinks: 1})
	if err != nil {
		t.Fatalf("NewAuditBatch(small) error = %v", err)
	}
	if err := CopyAuditBatch(&tooSmall, &destination); !errors.Is(err, ErrAuditBatchTooLarge) {
		t.Fatalf("CopyAuditBatch(small) error = %v, want %v", err, ErrAuditBatchTooLarge)
	}

	narrow, err := NewAuditBatch(testAuditCapacity())
	if err != nil {
		t.Fatalf("NewAuditBatch(narrow) error = %v", err)
	}
	narrow.Findings.Rationales = nil
	if err := CopyAuditBatch(&narrow, &destination); !errors.Is(err, ErrAuditBatchTooLarge) {
		t.Fatalf("CopyAuditBatch(narrow) error = %v, want %v", err, ErrAuditBatchTooLarge)
	}
}

func TestAuditModesAndDecisions(t *testing.T) {
	for _, mode := range []AuditMode{AuditOff, AuditBestEffort, AuditRequired} {
		if !mode.Valid() {
			t.Fatalf("mode %d is invalid", mode)
		}
	}
	if AuditMode(99).Valid() {
		t.Fatal("unknown audit mode is valid")
	}
	wants := []string{"Approve", "Reject", "Revise", "Escalate"}
	for index, decision := range []Decision{DecisionApprove, DecisionReject, DecisionRevise, DecisionEscalate} {
		if !decision.Valid() || decision.String() != wants[index] {
			t.Fatalf("decision %d = (%v, %q), want (true, %q)", decision, decision.Valid(), decision.String(), wants[index])
		}
	}
	if Decision(99).Valid() || Decision(99).String() != "" {
		t.Fatal("unknown decision is valid")
	}
}

func TestNewAuditBatchRejectsUnrepresentableCapacity(t *testing.T) {
	capacity := testAuditCapacity()
	capacity.Requests = int(^uint(0) >> 1)
	if batch, err := NewAuditBatch(capacity); err == nil || !errors.Is(err, ErrInvalidJournal) || len(batch.Bytes) != 0 {
		t.Fatalf("NewAuditBatch(huge) = (%+v, %v), want zero batch and %v", batch, err, ErrInvalidJournal)
	}
}
