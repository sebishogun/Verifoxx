package eval

import (
	"bytes"
	"errors"

	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
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
	reasons []schema.ReasonID
	program *program.Program
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
