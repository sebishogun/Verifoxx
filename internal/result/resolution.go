package result

import (
	"errors"

	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

// RuleSetID identifies one nine-row resolution block, one row per engine
// reason in fixed order.
type RuleSetID uint32

// ResolutionTable maps rule rows to outcomes and to CSR remediation ranges
// over RemediationIDs. The three row columns are parallel and the edge slice
// is a separate CSR backing array; all four are borrowed.
type ResolutionTable struct {
	OutcomeIDs        []schema.OutcomeID
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
	if n == 0 || len(rules.RemediationStarts) != n || len(rules.RemediationCounts) != n {
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
