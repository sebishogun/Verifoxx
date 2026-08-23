package result

import (
	"errors"

	"github.com/sebishogun/verifoxx/internal/schema"
)

// ErrBatchTooLarge reports a result shape that cannot be represented by the
// current architecture. It also covers an unusable nil destination.
var ErrBatchTooLarge = errors.New("result: batch too large")

// Batch stores one policy result per request row plus compact CSR provenance.
// A caller owns the backing storage and may reuse it across executions.
type Batch struct {
	OutcomeIDs []schema.OutcomeID

	RequirementOffsets []uint32
	RequirementIDs     []schema.RequirementID

	DriverOffsets      []uint32
	DriverRequirements []schema.RequirementID
	DriverClauses      []schema.ClauseID
	DriverNodes        []schema.NodeID
	DriverReasons      []schema.ReasonID
	DriverExplanations []schema.ExplanationID

	EvidenceOffsets      []uint32
	EvidenceIDs          []schema.EvidenceID
	ReasonOffsets        []uint32
	ReasonIDs            []schema.ReasonID
	ReasonNodes          []schema.NodeID
	ReasonEvidenceIDs    []schema.EvidenceID
	ReasonEvidenceStates []schema.EvidenceStateID

	RemediationOffsets []uint32
	RemediationIDs     []schema.RemediationID

	Rows uint32
}

// Reset replaces the active fixed-width shape and clears all CSR edge lengths
// while retaining their capacity for the next execution.
func (b *Batch) Reset(rows uint32) error {
	maxInt := uint64(^uint(0) >> 1)
	if b == nil || uint64(rows)+1 > maxInt {
		return ErrBatchTooLarge
	}
	n := int(rows)
	offsets := n + 1
	b.OutcomeIDs = resetBatchSlice(b.OutcomeIDs, n)
	b.RequirementOffsets = resetBatchSlice(b.RequirementOffsets, offsets)
	b.DriverOffsets = resetBatchSlice(b.DriverOffsets, offsets)
	b.EvidenceOffsets = resetBatchSlice(b.EvidenceOffsets, offsets)
	b.ReasonOffsets = resetBatchSlice(b.ReasonOffsets, offsets)
	b.RemediationOffsets = resetBatchSlice(b.RemediationOffsets, offsets)
	b.RequirementIDs = b.RequirementIDs[:0]
	b.DriverRequirements = b.DriverRequirements[:0]
	b.DriverClauses = b.DriverClauses[:0]
	b.DriverNodes = b.DriverNodes[:0]
	b.DriverReasons = b.DriverReasons[:0]
	b.DriverExplanations = b.DriverExplanations[:0]
	b.EvidenceIDs = b.EvidenceIDs[:0]
	b.ReasonIDs = b.ReasonIDs[:0]
	b.ReasonNodes = b.ReasonNodes[:0]
	b.ReasonEvidenceIDs = b.ReasonEvidenceIDs[:0]
	b.ReasonEvidenceStates = b.ReasonEvidenceStates[:0]
	b.RemediationIDs = b.RemediationIDs[:0]
	b.Rows = rows
	return nil
}

func resetBatchSlice[T any](dst []T, n int) []T {
	if cap(dst) < n {
		return make([]T, n)
	}
	dst = dst[:n]
	clear(dst)
	return dst
}
