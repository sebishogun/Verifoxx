package diff

import (
	"context"
	"math"

	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
)

type searchPlan struct {
	fieldRows    []uint32
	optionCounts []uint32
	strides      []uint64
	oldFieldIDs  []schema.FieldID
	newFieldIDs  []schema.FieldID
	changed      []uint8

	referenced    []uint8
	changedFields []uint8
	changedRows   []uint8
	stack         []schema.InstructionID

	cardinality   uint64
	evidenceCount uint32
}

func buildSearchPlan(dst *searchPlan, oldProgram, newProgram *program.Program, domain Domain) (*searchPlan, error) {
	if oldProgram == nil || newProgram == nil || domain.MaxCandidates == 0 || domain.BatchRows == 0 || domain.BatchRows > MaxBatchRows {
		return nil, ErrInvalidDomain
	}
	validationDomain := domain
	validationDomain.MaxCandidates = math.MaxUint64
	if _, _, err := validationDomain.Validate(); err != nil {
		return nil, err
	}
	if dst == nil {
		dst = &searchPlan{}
	}
	dst.fieldRows = dst.fieldRows[:0]
	dst.optionCounts = dst.optionCounts[:0]
	dst.strides = dst.strides[:0]
	dst.oldFieldIDs = dst.oldFieldIDs[:0]
	dst.newFieldIDs = dst.newFieldIDs[:0]
	dst.changed = dst.changed[:0]
	dst.cardinality = 1
	dst.evidenceCount = 1
	if err := collectDependencies(dst, oldProgram, newProgram, domain); err != nil {
		return nil, err
	}
	for _, count := range dst.optionCounts {
		dst.strides = append(dst.strides, dst.cardinality)
		var ok bool
		dst.cardinality, ok = checkedProduct(dst.cardinality, uint64(count))
		if !ok {
			return nil, ErrInvalidDomain
		}
	}
	if programUsesEvidence(oldProgram) || programUsesEvidence(newProgram) {
		if len(domain.EvidenceSets) == 0 {
			return nil, ErrInvalidDomain
		}
		dst.evidenceCount = uint32(len(domain.EvidenceSets))
		var ok bool
		dst.cardinality, ok = checkedProduct(dst.cardinality, uint64(dst.evidenceCount))
		if !ok {
			return nil, ErrInvalidDomain
		}
	}
	if dst.cardinality > domain.MaxCandidates {
		return nil, ErrCandidateBudget
	}
	if dst.cardinality > math.MaxUint32 {
		return nil, ErrCandidateBudget
	}
	return dst, nil
}

func programUsesEvidence(compiled *program.Program) bool {
	for _, kind := range compiled.EvidenceKinds {
		if kind != 0 {
			return true
		}
	}
	return false
}

func (plan *searchPlan) generate(ctx context.Context, fieldOptions, evidenceOptions []uint32, start uint64, rows uint32) error {
	if plan == nil || rows == 0 || start > plan.cardinality || uint64(rows) > plan.cardinality-start ||
		uint64(len(fieldOptions)) < uint64(len(plan.optionCounts))*uint64(rows) || len(evidenceOptions) < int(rows) {
		return ErrInvalidDomain
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fieldStride := uint64(rows)
	evidenceStride := uint64(1)
	for _, count := range plan.optionCounts {
		evidenceStride *= uint64(count)
	}
	for row := uint32(0); row < rows; row++ {
		if row&63 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		candidate := start + uint64(row)
		for field := range plan.optionCounts {
			fieldOptions[uint64(field)*fieldStride+uint64(row)] = uint32(candidate / plan.strides[field] % uint64(plan.optionCounts[field]))
		}
		if plan.evidenceCount > 1 {
			evidenceOptions[row] = uint32(candidate/evidenceStride) % plan.evidenceCount
		} else {
			evidenceOptions[row] = 0
		}
	}
	return nil
}
