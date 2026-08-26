package result

import (
	"errors"

	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

// RuleSetID identifies one nine-row resolution block, one row per engine
// reason in fixed order.
type RuleSetID uint32

// ResolutionTable maps rule rows to outcomes and to CSR remediation ranges
// over RemediationIDs. The three row columns are parallel and the edge slice
// is a separate CSR backing array; all four are borrowed.
type ResolutionTable struct {
	OutcomeIDs        []schema.OutcomeID
	ExplanationIDs    []schema.ExplanationID
	RemediationStarts []uint32
	RemediationCounts []uint16
	RemediationIDs    []schema.RemediationID
}

// Resolver holds validated, immutable outcome, remediation, and resolution
// tables for policy-owned resolution decisions. The caller must keep the
// backing slices immutable for the resolver lifetime; Resolver never copies
// them.
type Resolver struct {
	outcomes     OutcomeTable
	remediations RemediationTable
	rules        ResolutionTable
}

var (
	ErrInvalidOutcomeTable         = errors.New("result: invalid outcome table")
	ErrInvalidRemediationTable     = errors.New("result: invalid remediation table")
	ErrInvalidResolutionTable      = errors.New("result: invalid resolution table")
	ErrInvalidOutcomeReference     = errors.New("result: invalid outcome reference")
	ErrInvalidRemediationReference = errors.New("result: invalid remediation reference")
)

// NewResolver validates the three tables and returns a Resolver borrowing
// their slice headers. Validation order: outcome table shape, remediation
// table shape, resolution row shape with a row count divisible by
// truth.ReasonCount, every rule row's outcome reference, every CSR range with
// widened arithmetic (an out-of-bounds range is a table shape error), then
// every remediation edge in the whole edge slice (an invalid edge ID is a
// reference error; unused valid edges are permitted).
func NewResolver(outcomes OutcomeTable, remediations RemediationTable, rules ResolutionTable) (Resolver, error) {
	if !outcomes.valid() {
		return Resolver{}, ErrInvalidOutcomeTable
	}
	if !remediations.valid() {
		return Resolver{}, ErrInvalidRemediationTable
	}
	n := len(rules.OutcomeIDs)
	if n == 0 || len(rules.ExplanationIDs) != n || len(rules.RemediationStarts) != n || len(rules.RemediationCounts) != n {
		return Resolver{}, ErrInvalidResolutionTable
	}
	if n%truth.ReasonCount != 0 {
		return Resolver{}, ErrInvalidResolutionTable
	}
	for _, id := range rules.OutcomeIDs {
		if _, ok := outcomes.Lookup(id); !ok {
			return Resolver{}, ErrInvalidOutcomeReference
		}
	}
	for _, id := range rules.ExplanationIDs {
		if id == 0 {
			return Resolver{}, ErrInvalidExplanationReference
		}
	}
	edgeLen := uint64(len(rules.RemediationIDs))
	for i := 0; i < n; i++ {
		if uint64(rules.RemediationStarts[i])+uint64(rules.RemediationCounts[i]) > edgeLen {
			return Resolver{}, ErrInvalidResolutionTable
		}
	}
	for _, id := range rules.RemediationIDs {
		if _, ok := remediations.Lookup(id); !ok {
			return Resolver{}, ErrInvalidRemediationReference
		}
	}
	return Resolver{outcomes: outcomes, remediations: remediations, rules: rules}, nil
}

// Resolution is one deterministic policy decision produced by Resolve.
type Resolution struct {
	Remediations []schema.RemediationID
	Outcome      schema.OutcomeID
	Explanation  schema.ExplanationID
	Reason       schema.ReasonID
	Terminal     bool
}

// Resolve selects the winning outcome for a reason mask within one rule set.
// An invalid mask or an out-of-range rule set panics with a static string
// before any rule row is read; an empty valid mask returns (Resolution{},
// false). The scan visits reason offsets 0..8 in ascending order and skips
// unset bits. Higher numeric precedence wins, an equal-precedence tie keeps
// the lower OutcomeID, and a repeated winning outcome keeps the first reason.
// Terminal is metadata only and never stops the scan. The returned
// remediation slice borrows the CSR edge array; Resolve never allocates.
func (r *Resolver) Resolve(ruleSet RuleSetID, reasons truth.ReasonMask) (Resolution, bool) {
	if !reasons.Valid() {
		panic("result: invalid reason mask")
	}
	blocks := uint64(len(r.rules.OutcomeIDs)) / uint64(truth.ReasonCount)
	if ruleSet == 0 || uint64(ruleSet) > blocks {
		panic("result: invalid rule set")
	}
	if reasons == 0 {
		return Resolution{}, false
	}
	base := int(uint64(ruleSet-1) * uint64(truth.ReasonCount))
	var current schema.OutcomeID
	var reason schema.ReasonID
	winRow := base
	for off := 0; off < truth.ReasonCount; off++ {
		if reasons&(1<<off) == 0 {
			continue
		}
		row := base + off
		winner := r.outcomes.preferKnown(current, r.rules.OutcomeIDs[row])
		if winner != current {
			current = winner
			reason = schema.ReasonID(off + 1)
			winRow = row
		}
	}
	start := int(r.rules.RemediationStarts[winRow])
	count := int(r.rules.RemediationCounts[winRow])
	return Resolution{
		Outcome:      current,
		Explanation:  r.rules.ExplanationIDs[winRow],
		Reason:       reason,
		Terminal:     r.outcomes.Terminal[int(current-1)],
		Remediations: r.rules.RemediationIDs[start : start+count],
	}, true
}
