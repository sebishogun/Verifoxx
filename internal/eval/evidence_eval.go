package eval

import (
	"bytes"
	"errors"

	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

// ErrInvalidEvidenceProgram reports malformed immutable evidence-state
// metadata during cold evaluator binding.
var ErrInvalidEvidenceProgram = errors.New("eval: invalid evidence Program")

var (
	evidenceStateStale        = []byte("stale")
	evidenceStateUnclear      = []byte("unclear")
	evidenceStateUnverifiable = []byte("unverifiable")
	evidenceStateInvalid      = []byte("invalid")
	evidenceStateConflict     = []byte("conflict")
	evidenceStateConflicting  = []byte("conflicting")
)

// EvidenceStateIndex classifies one immutable Program's evidence states. It is
// reusable across Program publications and read-only during evaluation.
type EvidenceStateIndex struct {
	program *program.Program
	reasons []schema.ReasonID
}

// EvidencePredicate is one fixed-width evidence query. Zero optional symbols
// leave that evidence attribute unconstrained.
type EvidencePredicate struct {
	Kind    schema.EvidenceKindID
	State   schema.EvidenceStateID
	Subject schema.SymbolID
	Scope   schema.SymbolID
	Timing  schema.SymbolID
}

type evidenceRecordClassification struct {
	reasons  truth.ReasonMask
	positive bool
	negative bool
}

var evidenceReasonMasks = [truth.ReasonCount + 1]truth.ReasonMask{
	0,
	1 << (truth.ReasonMissing - 1),
	1 << (truth.ReasonStale - 1),
	1 << (truth.ReasonUnclear - 1),
	1 << (truth.ReasonUnverifiable - 1),
	1 << (truth.ReasonWrongScope - 1),
	1 << (truth.ReasonWrongSubject - 1),
	1 << (truth.ReasonWrongTiming - 1),
	1 << (truth.ReasonInvalid - 1),
	1 << (truth.ReasonConflict - 1),
}

// Bind validates and classifies p without changing a previously usable index
// on error. Rebinding the same immutable Program is a no-op.
func (i *EvidenceStateIndex) Bind(p *program.Program) error {
	if i == nil || p == nil {
		return ErrInvalidEvidenceProgram
	}
	if i.program == p {
		return nil
	}
	for _, nameID := range p.EvidenceStateNames {
		if _, ok := p.Symbol(nameID); !ok {
			return ErrInvalidEvidenceProgram
		}
	}

	reasons := resizeClear(i.reasons, len(p.EvidenceStateNames))
	for row, nameID := range p.EvidenceStateNames {
		name, _ := p.Symbol(nameID)
		switch {
		case bytes.Equal(name, evidenceStateStale):
			reasons[row] = truth.ReasonStale
		case bytes.Equal(name, evidenceStateUnclear):
			reasons[row] = truth.ReasonUnclear
		case bytes.Equal(name, evidenceStateUnverifiable):
			reasons[row] = truth.ReasonUnverifiable
		case bytes.Equal(name, evidenceStateInvalid):
			reasons[row] = truth.ReasonInvalid
		case bytes.Equal(name, evidenceStateConflict), bytes.Equal(name, evidenceStateConflicting):
			reasons[row] = truth.ReasonConflict
		}
	}
	i.reasons = reasons
	i.program = p
	return nil
}

func (i *EvidenceStateIndex) reason(state schema.EvidenceStateID) schema.ReasonID {
	if i == nil || i.program == nil || state == 0 || uint64(state-1) >= uint64(len(i.reasons)) {
		panic("eval: invalid evidence state")
	}
	return i.reasons[state-1]
}

func requireEvidenceBatch(batch Batch, p *program.Program, states *EvidenceStateIndex) {
	if p == nil || states == nil || states.program != p || len(states.reasons) != len(p.EvidenceStateNames) {
		panic("eval: invalid evidence program")
	}
	if !validEvidenceBatch(batch, p) {
		panic("eval: invalid evidence batch")
	}
}

func validEvidenceBatch(batch Batch, p *program.Program) bool {
	if p == nil {
		return false
	}
	if uint64(len(batch.RequestIDs)) != uint64(batch.Rows) ||
		uint64(len(batch.EvidenceOffsets)) != uint64(batch.Rows)+1 ||
		len(batch.EvidenceOffsets) == 0 || batch.EvidenceOffsets[0] != 0 ||
		uint64(batch.EvidenceOffsets[len(batch.EvidenceOffsets)-1]) != uint64(len(batch.EvidenceRefs)) {
		return false
	}
	evidence := batch.Evidence
	rows := len(evidence.IDs)
	if len(evidence.Kinds) != rows || len(evidence.States) != rows || len(evidence.Subjects) != rows ||
		len(evidence.Scopes) != rows || len(evidence.Reviewers) != rows || len(evidence.Timings) != rows ||
		len(evidence.Timestamps) != rows {
		return false
	}
	previous := uint32(0)
	for _, offset := range batch.EvidenceOffsets[1:] {
		if offset < previous || uint64(offset) > uint64(len(batch.EvidenceRefs)) {
			return false
		}
		previous = offset
	}
	for _, ref := range batch.EvidenceRefs {
		if uint64(ref) >= uint64(rows) {
			return false
		}
	}
	for row := range rows {
		if evidence.IDs[row] == 0 || evidence.Kinds[row] == 0 ||
			uint64(evidence.Kinds[row]) > uint64(len(p.EvidenceKindNames)) || evidence.States[row] == 0 ||
			uint64(evidence.States[row]) > uint64(len(p.EvidenceStateNames)) {
			return false
		}
	}
	return true
}

func requireEvidencePredicate(p *program.Program, states *EvidenceStateIndex, predicate EvidencePredicate) {
	if p == nil || states == nil || states.program != p || len(states.reasons) != len(p.EvidenceStateNames) ||
		predicate.Kind == 0 || uint64(predicate.Kind) > uint64(len(p.EvidenceKindNames)) ||
		predicate.State == 0 || uint64(predicate.State) > uint64(len(p.EvidenceStateNames)) {
		panic("eval: invalid evidence predicate")
	}
}

func evalEvidence(dst truth.Planes, reasons ReasonPlanes, batch Batch, p *program.Program, states *EvidenceStateIndex, predicate EvidencePredicate) {
	requireEvidencePredicate(p, states, predicate)
	requireEvidenceBatch(batch, p, states)
	evalEvidenceUnchecked(dst, reasons, batch, states, predicate)
}

func evalEvidenceValidated(dst truth.Planes, reasons ReasonPlanes, batch Batch, p *program.Program, states *EvidenceStateIndex, predicate EvidencePredicate) {
	requireEvidencePredicate(p, states, predicate)
	evalEvidenceUnchecked(dst, reasons, batch, states, predicate)
}

func classifyEvidenceRecord(
	state, expectedState schema.EvidenceStateID,
	stateReason schema.ReasonID,
	subject, expectedSubject schema.SymbolID,
	scope, expectedScope schema.SymbolID,
	timing, expectedTiming schema.SymbolID,
) evidenceRecordClassification {
	stateMatches := state == expectedState
	if stateMatches {
		stateReason = 0
	}

	var qualifierReasons truth.ReasonMask
	if expectedSubject != 0 && subject != expectedSubject {
		qualifierReasons |= 1 << (truth.ReasonWrongSubject - 1)
	}
	if expectedScope != 0 && scope != expectedScope {
		qualifierReasons |= 1 << (truth.ReasonWrongScope - 1)
	}
	if expectedTiming != 0 && timing != expectedTiming {
		qualifierReasons |= 1 << (truth.ReasonWrongTiming - 1)
	}
	return evidenceRecordClassification{
		reasons:  evidenceReasonMasks[stateReason] | qualifierReasons,
		positive: stateMatches && qualifierReasons == 0 || stateReason == truth.ReasonConflict,
		negative: !stateMatches && (stateReason == 0 || stateReason == truth.ReasonConflict),
	}
}

func evalEvidenceUnchecked(dst truth.Planes, reasons ReasonPlanes, batch Batch, states *EvidenceStateIndex, predicate EvidencePredicate) {
	words := resetLeafOutputs(dst, reasons, batch.Rows)
	evidence := &batch.Evidence
	for row := uint32(0); row < batch.Rows; row++ {
		start := batch.EvidenceOffsets[row]
		end := batch.EvidenceOffsets[row+1]
		foundKind := false
		positive := false
		negative := false
		var reasonMask truth.ReasonMask
		for edge := start; edge < end; edge++ {
			evidenceRow := batch.EvidenceRefs[edge]
			if evidence.Kinds[evidenceRow] != predicate.Kind {
				continue
			}
			foundKind = true
			state := evidence.States[evidenceRow]
			classification := classifyEvidenceRecord(
				state,
				predicate.State,
				states.reasons[state-1],
				evidence.Subjects[evidenceRow],
				predicate.Subject,
				evidence.Scopes[evidenceRow],
				predicate.Scope,
				evidence.Timings[evidenceRow],
				predicate.Timing,
			)
			positive = positive || classification.positive
			negative = negative || classification.negative
			reasonMask |= classification.reasons
		}
		if !foundKind {
			reasonMask = reasonMask.With(truth.ReasonMissing)
		}
		if positive && negative {
			reasonMask = reasonMask.With(truth.ReasonConflict)
		}

		word := row >> 6
		bit := uint64(1) << (row & 63)
		if positive {
			dst.Positive[word] |= bit
		}
		if negative {
			dst.Negative[word] |= bit
		}
		for reason := truth.ReasonMissing; reason <= truth.ReasonConflict; reason++ {
			if reasonMask.Has(reason) {
				reasons.plane(reason, words)[word] |= bit
			}
		}
	}
}
