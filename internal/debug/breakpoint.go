package debug

import (
	"math"

	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// AnyRow applies a truth, evidence-state, or outcome breakpoint to every row.
const AnyRow = math.MaxUint32

// BreakpointKind identifies one fixed-cardinality semantic stop condition.
type BreakpointKind uint8

const (
	BreakInvalid BreakpointKind = iota
	BreakInstruction
	BreakNode
	BreakTruth
	BreakEvidenceState
	BreakOutcome
)

// BreakpointID is one session-local breakpoint handle.
type BreakpointID uint32

// Breakpoint stores only the typed target used by Kind. Row may be AnyRow for
// truth, evidence-state, and outcome breakpoints.
type Breakpoint struct {
	Instruction   schema.InstructionID
	Node          schema.NodeID
	EvidenceState schema.EvidenceStateID
	Outcome       schema.OutcomeID
	Row           uint32
	Kind          BreakpointKind
	Truth         TruthState
}

type breakpointEntry struct {
	breakpoint Breakpoint
	id         BreakpointID
}

func validBreakpoint(state *sessionState, breakpoint Breakpoint) bool {
	if state == nil || state.program == nil {
		return false
	}
	switch breakpoint.Kind {
	case BreakInstruction:
		return breakpoint.Instruction != 0 && uint64(breakpoint.Instruction) <= uint64(len(state.program.Opcodes))
	case BreakNode:
		start, end, ok := nodeInstructionRange(state.program, breakpoint.Node)
		return ok && start != end
	case BreakTruth:
		return breakpoint.Truth <= TruthBoth && validBreakpointRow(breakpoint.Row, state.batch.Rows)
	case BreakEvidenceState:
		return breakpoint.EvidenceState != 0 &&
			uint64(breakpoint.EvidenceState) <= uint64(len(state.program.EvidenceStateNames)) &&
			validBreakpointRow(breakpoint.Row, state.batch.Rows)
	case BreakOutcome:
		_, ok := state.program.Outcomes.Lookup(breakpoint.Outcome)
		return ok && validBreakpointRow(breakpoint.Row, state.batch.Rows)
	default:
		return false
	}
}

func validBreakpointRow(row, rows uint32) bool {
	return row == AnyRow || row < rows
}

func (state *sessionState) matchingBreakpoint(instruction schema.InstructionID) BreakpointID {
	for _, entry := range state.breakpoints {
		breakpoint := entry.breakpoint
		switch breakpoint.Kind {
		case BreakInstruction:
			if breakpoint.Instruction == instruction {
				return entry.id
			}
		case BreakNode:
			if nodeMapsInstruction(state.program, breakpoint.Node, instruction) {
				return entry.id
			}
		case BreakTruth:
			if state.matchesTruth(instruction, breakpoint) {
				return entry.id
			}
		case BreakEvidenceState:
			if state.matchesEvidence(instruction, breakpoint) {
				return entry.id
			}
		case BreakOutcome:
			if state.matchesOutcome(breakpoint) {
				return entry.id
			}
		}
	}
	return 0
}

func nodeInstructionRange(p *program.Program, node schema.NodeID) (uint64, uint64, bool) {
	if p == nil || node == 0 {
		return 0, 0, false
	}
	row := uint64(node - 1)
	if row >= uint64(len(p.NodeInstructionStarts)) || row >= uint64(len(p.NodeInstructionCounts)) {
		return 0, 0, false
	}
	start := uint64(p.NodeInstructionStarts[row])
	end := start + uint64(p.NodeInstructionCounts[row])
	return start, end, end >= start && end <= uint64(len(p.NodeInstructionIDs))
}

func nodeMapsInstruction(p *program.Program, node schema.NodeID, instruction schema.InstructionID) bool {
	start, end, ok := nodeInstructionRange(p, node)
	if !ok {
		return false
	}
	for _, mapped := range p.NodeInstructionIDs[int(start):int(end)] {
		if mapped == instruction {
			return true
		}
	}
	return false
}

func (state *sessionState) matchesTruth(instruction schema.InstructionID, breakpoint Breakpoint) bool {
	return visitBreakpointRows(breakpoint.Row, state.batch.Rows, func(row uint32) bool {
		positive, negative, ok := state.execution.InstructionTruth(instruction, row)
		return ok && classifyTruth(positive, negative) == breakpoint.Truth
	})
}

func (state *sessionState) matchesEvidence(instruction schema.InstructionID, breakpoint Breakpoint) bool {
	row := int(instruction - 1)
	if state.program.Opcodes[row] != program.OpcodeEvidence {
		return false
	}
	kind := state.program.EvidenceKinds[row]
	return visitBreakpointRows(breakpoint.Row, state.batch.Rows, func(requestRow uint32) bool {
		start, end, ok := state.batch.EvidenceRange(requestRow)
		if !ok {
			return false
		}
		for _, evidenceRow := range state.batch.EvidenceRefs[start:end] {
			if uint64(evidenceRow) < uint64(len(state.batch.Evidence.States)) &&
				state.batch.Evidence.Kinds[evidenceRow] == kind &&
				state.batch.Evidence.States[evidenceRow] == breakpoint.EvidenceState {
				return true
			}
		}
		return false
	})
}

func (state *sessionState) matchesOutcome(breakpoint Breakpoint) bool {
	resultBatch, ok := state.execution.Result()
	if !ok {
		return false
	}
	return visitBreakpointRows(breakpoint.Row, resultBatch.Rows, func(row uint32) bool {
		return resultBatch.OutcomeIDs[row] == breakpoint.Outcome
	})
}

func visitBreakpointRows(row, rows uint32, match func(uint32) bool) bool {
	if row != AnyRow {
		return match(row)
	}
	for candidate := uint32(0); candidate < rows; candidate++ {
		if match(candidate) {
			return true
		}
	}
	return false
}
