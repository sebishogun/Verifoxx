package result

import (
	"errors"

	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

const (
	MaxAssumptionTemplates      = 8
	MaxUncertaintyTemplates     = 8
	MaxRenderedExplanationBytes = 4096
	EvidenceIssueTemplateCount  = truth.ReasonCount
)

var (
	ErrInvalidExplanationTable     = errors.New("result: invalid explanation table")
	ErrInvalidEvidenceIssueTable   = errors.New("result: invalid evidence issue table")
	ErrInvalidTemplateReference    = errors.New("result: invalid template reference")
	ErrInvalidExplanationReference = errors.New("result: invalid explanation reference")
)

// ExplanationTable is a non-owning immutable view over rationale rows,
// uncertainty CSR edges, and source-ordered policy assumptions.
type ExplanationTable struct {
	RationaleTemplateIDs   []schema.TemplateID
	UncertaintyStarts      []uint32
	UncertaintyCounts      []uint16
	UncertaintyTemplateIDs []schema.TemplateID
	AssumptionTemplateIDs  []schema.TemplateID
}

// Explanation is one borrowed rationale and uncertainty row.
type Explanation struct {
	Uncertainty []schema.TemplateID
	Rationale   schema.TemplateID
}

// Lookup returns one borrowed explanation row. It checks local row shape but
// leaves cross-table TemplateID validation to Validate.
func (table *ExplanationTable) Lookup(id schema.ExplanationID) (Explanation, bool) {
	if table == nil || id == 0 {
		return Explanation{}, false
	}
	row := uint64(id - 1)
	if row >= uint64(len(table.RationaleTemplateIDs)) || row >= uint64(len(table.UncertaintyStarts)) ||
		row >= uint64(len(table.UncertaintyCounts)) {
		return Explanation{}, false
	}
	rationale := table.RationaleTemplateIDs[row]
	start := uint64(table.UncertaintyStarts[row])
	count := uint64(table.UncertaintyCounts[row])
	end := start + count
	if rationale == 0 || count > MaxUncertaintyTemplates || end > uint64(len(table.UncertaintyTemplateIDs)) {
		return Explanation{}, false
	}
	for i := start; i < end; i++ {
		if table.UncertaintyTemplateIDs[int(i)] == 0 {
			return Explanation{}, false
		}
	}
	return Explanation{
		Uncertainty: table.UncertaintyTemplateIDs[int(start):int(end)],
		Rationale:   rationale,
	}, true
}

// Assumptions returns the borrowed source-ordered assumption TemplateIDs.
func (table *ExplanationTable) Assumptions() []schema.TemplateID {
	if table == nil {
		return nil
	}
	return table.AssumptionTemplateIDs
}

func templateMaximum(table *TemplateTable, id schema.TemplateID) (uint32, bool) {
	if id == 0 || uint64(id) > uint64(len(table.MaxBytes)) {
		return 0, false
	}
	return table.MaxBytes[id-1], true
}

// Validate checks table shape, exact CSR consumption, every TemplateID, and
// the maximum combined assumption/rationale/uncertainty expansion per row.
func (table *ExplanationTable) Validate(templates *TemplateTable) error {
	if templates == nil || templates.Validate() != nil {
		return ErrInvalidTemplateTable
	}
	if table == nil {
		return ErrInvalidExplanationTable
	}
	rows := len(table.RationaleTemplateIDs)
	if rows == 0 || len(table.UncertaintyStarts) != rows || len(table.UncertaintyCounts) != rows ||
		len(table.AssumptionTemplateIDs) > MaxAssumptionTemplates {
		return ErrInvalidExplanationTable
	}
	var assumptionBytes uint64
	for _, id := range table.AssumptionTemplateIDs {
		maximum, ok := templateMaximum(templates, id)
		if !ok {
			return ErrInvalidTemplateReference
		}
		assumptionBytes += uint64(maximum)
		if assumptionBytes > MaxRenderedExplanationBytes {
			return ErrInvalidExplanationTable
		}
	}
	var cursor uint64
	for row := 0; row < rows; row++ {
		start := uint64(table.UncertaintyStarts[row])
		count := uint64(table.UncertaintyCounts[row])
		end := start + count
		if count > MaxUncertaintyTemplates || start != cursor || end > uint64(len(table.UncertaintyTemplateIDs)) {
			return ErrInvalidExplanationTable
		}
		maximum, ok := templateMaximum(templates, table.RationaleTemplateIDs[row])
		if !ok {
			return ErrInvalidTemplateReference
		}
		total := assumptionBytes + uint64(maximum)
		for i := start; i < end; i++ {
			maximum, ok = templateMaximum(templates, table.UncertaintyTemplateIDs[int(i)])
			if !ok {
				return ErrInvalidTemplateReference
			}
			total += uint64(maximum)
			if total > MaxRenderedExplanationBytes {
				return ErrInvalidExplanationTable
			}
		}
		cursor = end
	}
	if cursor != uint64(len(table.UncertaintyTemplateIDs)) {
		return ErrInvalidExplanationTable
	}
	return nil
}
