package diff

import (
	"context"
	"math"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
)

type candidateBatches struct {
	old eval.Batch
	new eval.Batch
}

type candidateMaterializer struct {
	fieldOptions    []uint32
	evidenceOptions []uint32
	evidenceOffsets []uint32
	evidenceRefs    []uint32
	oldBuilder      eval.Builder
	newBuilder      eval.Builder
}

func (materializer *candidateMaterializer) materialize(
	dst *candidateBatches,
	oldProgram, newProgram *program.Program,
	plan *searchPlan,
	domain Domain,
	start uint64,
	rows uint32,
) error {
	return materializer.materializeContext(context.Background(), dst, oldProgram, newProgram, plan, domain, start, rows)
}

func (materializer *candidateMaterializer) materializeContext(
	ctx context.Context,
	dst *candidateBatches,
	oldProgram, newProgram *program.Program,
	plan *searchPlan,
	domain Domain,
	start uint64,
	rows uint32,
) error {
	if materializer == nil || dst == nil || oldProgram == nil || newProgram == nil || plan == nil || rows == 0 ||
		len(plan.fieldRows) != len(plan.oldFieldIDs) || len(plan.fieldRows) != len(plan.newFieldIDs) ||
		start >= math.MaxUint32 || uint64(rows) > math.MaxUint32-start {
		return ErrInvalidDomain
	}
	optionLength := uint64(len(plan.fieldRows)) * uint64(rows)
	if optionLength > math.MaxInt {
		return ErrInvalidDomain
	}
	materializer.fieldOptions = resizeBatchSlice(materializer.fieldOptions, int(optionLength))
	materializer.evidenceOptions = resizeBatchSlice(materializer.evidenceOptions, int(rows))
	if err := plan.generate(ctx, materializer.fieldOptions, materializer.evidenceOptions, start, rows); err != nil {
		return err
	}
	evidenceRows, ok := materializer.prepareEvidenceShape(domain, rows)
	if !ok {
		return ErrInvalidDomain
	}
	oldBatch, err := materializer.build(
		&materializer.oldBuilder, oldProgram, plan.oldFieldIDs, plan, domain, start, rows, evidenceRows,
	)
	if err != nil {
		materializer.oldBuilder.Abort()
		materializer.newBuilder.Abort()
		return err
	}
	newBatch, err := materializer.build(
		&materializer.newBuilder, newProgram, plan.newFieldIDs, plan, domain, start, rows, evidenceRows,
	)
	if err != nil {
		materializer.oldBuilder.Abort()
		materializer.newBuilder.Abort()
		return err
	}
	dst.old = oldBatch
	dst.new = newBatch
	return nil
}

func (materializer *candidateMaterializer) prepareEvidenceShape(domain Domain, rows uint32) (uint32, bool) {
	refs := uint64(0)
	for row := uint32(0); row < rows; row++ {
		scenario := materializer.evidenceOptions[row]
		if uint64(scenario) >= uint64(len(domain.EvidenceSets)) {
			if len(domain.EvidenceSets) == 0 && scenario == 0 {
				continue
			}
			return 0, false
		}
		refs += uint64(len(domain.EvidenceSets[scenario].Records))
		if refs > math.MaxUint32 || refs > uint64(math.MaxInt) {
			return 0, false
		}
	}
	materializer.evidenceOffsets = resizeBatchSlice(materializer.evidenceOffsets, int(rows)+1)
	materializer.evidenceRefs = resizeBatchSlice(materializer.evidenceRefs, int(refs))
	evidenceRow := uint32(0)
	for row := uint32(0); row < rows; row++ {
		materializer.evidenceOffsets[row] = evidenceRow
		if len(domain.EvidenceSets) != 0 {
			for range domain.EvidenceSets[materializer.evidenceOptions[row]].Records {
				materializer.evidenceRefs[evidenceRow] = evidenceRow
				evidenceRow++
			}
		}
	}
	materializer.evidenceOffsets[rows] = evidenceRow
	return evidenceRow, true
}

func (materializer *candidateMaterializer) build(
	builder *eval.Builder,
	compiled *program.Program,
	fieldIDs []schema.FieldID,
	plan *searchPlan,
	domain Domain,
	start uint64,
	rows, evidenceRows uint32,
) (eval.Batch, error) {
	if err := builder.Begin(compiled, rows, evidenceRows, evidenceRows); err != nil {
		return eval.Batch{}, err
	}
	for row := uint32(0); row < rows; row++ {
		if err := builder.SetRequestID(row, schema.RequestID(start+uint64(row)+1)); err != nil {
			return eval.Batch{}, err
		}
		for dimension, fieldRow := range plan.fieldRows {
			option := materializer.fieldOptions[uint64(dimension)*uint64(rows)+uint64(row)]
			if uint64(option) >= uint64(len(domain.Fields[fieldRow].Values)) {
				return eval.Batch{}, ErrInvalidDomain
			}
			if err := setCandidateValue(builder, row, fieldIDs[dimension], domain.Fields[fieldRow].Values[option]); err != nil {
				return eval.Batch{}, err
			}
		}
	}
	evidenceRow := uint32(0)
	for row := uint32(0); row < rows && len(domain.EvidenceSets) != 0; row++ {
		records := domain.EvidenceSets[materializer.evidenceOptions[row]].Records
		for recordRow := range records {
			record, err := translateEvidence(builder, compiled, records[recordRow], evidenceRow+1)
			if err != nil {
				return eval.Batch{}, err
			}
			if err := builder.SetEvidence(evidenceRow, record); err != nil {
				return eval.Batch{}, err
			}
			evidenceRow++
		}
	}
	if err := builder.SetEvidenceCSR(materializer.evidenceOffsets, materializer.evidenceRefs); err != nil {
		return eval.Batch{}, err
	}
	return builder.Finish()
}

func setCandidateValue(builder *eval.Builder, row uint32, field schema.FieldID, value Value) error {
	if value.State == ValueMissing {
		return nil
	}
	switch value.Kind {
	case FieldKindString:
		symbol, err := builder.InternSymbol([]byte(value.String))
		if err != nil {
			return err
		}
		return builder.SetSymbol(row, field, symbol)
	case FieldKindInteger:
		return builder.SetInteger(row, field, value.Integer)
	case FieldKindBoolean:
		return builder.SetBoolean(row, field, value.Boolean)
	case FieldKindTimestamp:
		return builder.SetTimestamp(row, field, value.Integer)
	case FieldKindPresence:
		return builder.SetPresent(row, field)
	default:
		return ErrInvalidDomain
	}
}

func translateEvidence(builder *eval.Builder, compiled *program.Program, source Evidence, id uint32) (eval.EvidenceRecord, error) {
	kind, ok := catalogID(compiled, compiled.EvidenceKindNames, source.Kind)
	if !ok {
		return eval.EvidenceRecord{}, ErrInvalidDomain
	}
	state, ok := catalogID(compiled, compiled.EvidenceStateNames, source.State)
	if !ok {
		return eval.EvidenceRecord{}, ErrInvalidDomain
	}
	subject, err := optionalSymbol(builder, source.Subject)
	if err != nil {
		return eval.EvidenceRecord{}, err
	}
	scope, err := optionalSymbol(builder, source.Scope)
	if err != nil {
		return eval.EvidenceRecord{}, err
	}
	timing, err := optionalSymbol(builder, source.Timing)
	if err != nil {
		return eval.EvidenceRecord{}, err
	}
	return eval.EvidenceRecord{
		ID: schema.EvidenceID(id), Kind: schema.EvidenceKindID(kind), State: schema.EvidenceStateID(state),
		Subject: subject, Scope: scope, Timing: timing,
	}, nil
}

func catalogID(compiled *program.Program, names []schema.SymbolID, value string) (uint32, bool) {
	for row, name := range names {
		bytes, ok := compiled.Symbol(name)
		if ok && bytesEqualString(bytes, value) {
			return uint32(row + 1), true
		}
	}
	return 0, false
}

func optionalSymbol(builder *eval.Builder, value string) (schema.SymbolID, error) {
	if value == "" {
		return 0, nil
	}
	return builder.InternSymbol([]byte(value))
}

func resizeBatchSlice[T any](values []T, length int) []T {
	if cap(values) < length {
		return make([]T, length)
	}
	values = values[:length]
	clear(values)
	return values
}
