// Package debug provides deterministic semantic execution over compiled policy programs.
package debug

import (
	"slices"

	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

// Status is one actor-owned session lifecycle state.
type Status uint8

const (
	StatusInvalid Status = iota
	StatusPaused
	StatusRunning
	StatusComplete
	StatusClosed
)

// StopReason identifies why execution returned control to the caller.
type StopReason uint8

const (
	StopNone StopReason = iota
	StopInstruction
	StopNode
	StopOver
	StopBreakpoint
	StopPause
	StopRestart
	StopReplay
	StopComplete
)

// TruthState is one scalar interpretation of positive and negative mask bits.
type TruthState uint8

const (
	TruthNeither TruthState = iota
	TruthTrue
	TruthFalse
	TruthBoth
)

// WatchKind identifies a bounded semantic value projection.
type WatchKind uint8

const (
	WatchInvalid WatchKind = iota
	WatchField
	WatchMask
	WatchEvidence
	WatchOutcome
)

// WatchID is one session-local watch handle.
type WatchID uint32

// Watch selects one field, instruction mask, evidence record, or result row.
type Watch struct {
	Instruction schema.InstructionID
	Evidence    schema.EvidenceID
	Field       schema.FieldID
	Row         uint32
	Kind        WatchKind
}

// WatchValue is one immutable snapshot projection.
type WatchValue struct {
	Integer   int64
	Timestamp int64
	Watch
	Outcome       schema.OutcomeID
	Symbol        schema.SymbolID
	EvidenceKind  schema.EvidenceKindID
	EvidenceState schema.EvidenceStateID
	ID            WatchID
	Reasons       truth.ReasonMask
	ValueKind     schema.ValueKind
	Truth         TruthState
	Ready         bool
	Present       bool
	Boolean       bool
}

// State is a caller-owned immutable copy of one semantic stop boundary.
type State struct {
	Active             []uint64
	Positive           []uint64
	Negative           []uint64
	Reasons            []uint64
	OutcomeIDs         []schema.OutcomeID
	RemediationOffsets []uint32
	RemediationIDs     []schema.RemediationID
	Watches            []WatchValue
	Instruction        schema.InstructionID
	NextInstruction    schema.InstructionID
	ReplayFrom         schema.InstructionID
	Breakpoint         BreakpointID
	Node               schema.NodeID
	TruthSlot          schema.SlotID
	ReasonSlot         schema.SlotID
	SourceStart        uint32
	SourceEnd          uint32
	TruthWordOffset    uint64
	ReasonWordOffset   uint64
	Cursor             uint32
	Rows               uint32
	CheckpointCount    uint32
	Worker             uint32
	Shard              uint32
	Status             Status
	Stop               StopReason
}

func classifyTruth(positive, negative bool) TruthState {
	switch {
	case positive && negative:
		return TruthBoth
	case positive:
		return TruthTrue
	case negative:
		return TruthFalse
	default:
		return TruthNeither
	}
}

func activeMask(rows uint32) []uint64 {
	words := int((uint64(rows) + 63) >> 6)
	mask := make([]uint64, words)
	for word := range mask {
		mask[word] = ^uint64(0)
	}
	if words != 0 && rows&63 != 0 {
		mask[words-1] = uint64(1)<<(rows&63) - 1
	}
	return mask
}

func slotWordOffsets(instruction schema.InstructionID, rows uint32) (uint64, uint64) {
	words := (uint64(rows) + 63) >> 6
	slot := uint64(instruction - 1)
	return slot * 2 * words, slot * truth.ReasonCount * words
}

func cloneResultBatch(source *result.Batch) result.Batch {
	clone := *source
	clone.OutcomeIDs = slices.Clone(source.OutcomeIDs)
	clone.RequirementOffsets = slices.Clone(source.RequirementOffsets)
	clone.RequirementIDs = slices.Clone(source.RequirementIDs)
	clone.DriverOffsets = slices.Clone(source.DriverOffsets)
	clone.DriverRequirements = slices.Clone(source.DriverRequirements)
	clone.DriverClauses = slices.Clone(source.DriverClauses)
	clone.DriverNodes = slices.Clone(source.DriverNodes)
	clone.DriverReasons = slices.Clone(source.DriverReasons)
	clone.DriverExplanations = slices.Clone(source.DriverExplanations)
	clone.EvidenceOffsets = slices.Clone(source.EvidenceOffsets)
	clone.EvidenceIDs = slices.Clone(source.EvidenceIDs)
	clone.ReasonOffsets = slices.Clone(source.ReasonOffsets)
	clone.ReasonIDs = slices.Clone(source.ReasonIDs)
	clone.ReasonNodes = slices.Clone(source.ReasonNodes)
	clone.ReasonEvidenceIDs = slices.Clone(source.ReasonEvidenceIDs)
	clone.ReasonEvidenceStates = slices.Clone(source.ReasonEvidenceStates)
	clone.RemediationOffsets = slices.Clone(source.RemediationOffsets)
	clone.RemediationIDs = slices.Clone(source.RemediationIDs)
	return clone
}
