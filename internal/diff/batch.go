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
	oldIndex        candidateEvidenceIndex
	newIndex        candidateEvidenceIndex
	oldEvidence     candidateEvidenceShape
	newEvidence     candidateEvidenceShape
	oldBuilder      eval.Builder
	newBuilder      eval.Builder
}

type candidateEvidenceIndex struct {
	scenarioOffsets []uint32
	recordRows      []uint32
	kindIDs         []schema.EvidenceKindID
	stateIDs        []schema.EvidenceStateID
}

type candidateEvidenceShape struct {
	offsets []uint32
	refs    []uint32
}

const maxCandidateEvidenceRows = uint64(1 << 18)

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
	if err := materializer.prepareEvidenceIndexes(ctx, oldProgram, newProgram, domain); err != nil {
		return err
	}
	return materializer.materializePreparedContext(ctx, dst, oldProgram, newProgram, plan, domain, start, rows)
}

func (materializer *candidateMaterializer) materializePreparedContext(
	ctx context.Context,
	dst *candidateBatches,
	oldProgram, newProgram *program.Program,
	plan *searchPlan,
	domain Domain,
	start uint64,
	rows uint32,
) error {
	if materializer == nil || ctx == nil || dst == nil || oldProgram == nil || newProgram == nil || plan == nil || rows == 0 ||
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
	oldEvidenceRows, err := materializer.prepareEvidenceShape(ctx, &materializer.oldEvidence, &materializer.oldIndex, rows)
	if err != nil {
		return err
	}
	newEvidenceRows, err := materializer.prepareEvidenceShape(ctx, &materializer.newEvidence, &materializer.newIndex, rows)
	if err != nil {
		return err
	}
	oldBatch, err := materializer.build(
		ctx, &materializer.oldBuilder, &materializer.oldEvidence, &materializer.oldIndex,
		oldProgram, plan.oldFieldIDs, plan, domain, start, rows, oldEvidenceRows,
	)
	if err != nil {
		materializer.oldBuilder.Abort()
		materializer.newBuilder.Abort()
		return err
	}
	newBatch, err := materializer.build(
		ctx, &materializer.newBuilder, &materializer.newEvidence, &materializer.newIndex,
		newProgram, plan.newFieldIDs, plan, domain, start, rows, newEvidenceRows,
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

func (materializer *candidateMaterializer) prepareEvidenceIndexes(
	ctx context.Context,
	oldProgram, newProgram *program.Program,
	domain Domain,
) error {
	if materializer == nil || ctx == nil || oldProgram == nil || newProgram == nil {
		return ErrInvalidDomain
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	oldUsesEvidence := programUsesEvidence(oldProgram)
	newUsesEvidence := programUsesEvidence(newProgram)
	if !oldUsesEvidence && !newUsesEvidence {
		materializer.oldIndex.reset(0, 0)
		materializer.newIndex.reset(0, 0)
		return nil
	}
	if len(domain.EvidenceSets) > int(MaxBatchRows) {
		return ErrCandidateBudget
	}
	totalRecords := uint64(0)
	for scenario := range domain.EvidenceSets {
		totalRecords += uint64(len(domain.EvidenceSets[scenario].Records))
		if totalRecords > maxCandidateEvidenceRows {
			return ErrCandidateBudget
		}
	}
	oldRecords := 0
	if oldUsesEvidence {
		oldRecords = int(totalRecords)
	}
	newRecords := 0
	if newUsesEvidence {
		newRecords = int(totalRecords)
	}
	materializer.oldIndex.reset(len(domain.EvidenceSets), oldRecords)
	materializer.newIndex.reset(len(domain.EvidenceSets), newRecords)
	oldCount := uint32(0)
	newCount := uint32(0)
	visited := uint64(0)
	for scenario := range domain.EvidenceSets {
		records := domain.EvidenceSets[scenario].Records
		for recordRow := range records {
			if visited&63 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			visited++
			if oldUsesEvidence {
				kind, state, recognized := evidenceCatalogIDs(oldProgram, records[recordRow])
				if recognized {
					materializer.oldIndex.set(oldCount, uint32(recordRow), kind, state)
					oldCount++
				}
			}
			if newUsesEvidence {
				kind, state, recognized := evidenceCatalogIDs(newProgram, records[recordRow])
				if recognized {
					materializer.newIndex.set(newCount, uint32(recordRow), kind, state)
					newCount++
				}
			}
		}
		materializer.oldIndex.scenarioOffsets[scenario+1] = oldCount
		materializer.newIndex.scenarioOffsets[scenario+1] = newCount
	}
	materializer.oldIndex.truncate(oldCount)
	materializer.newIndex.truncate(newCount)
	return ctx.Err()
}

func (index *candidateEvidenceIndex) reset(scenarios, records int) {
	index.scenarioOffsets = resizeBatchSlice(index.scenarioOffsets, scenarios+1)
	index.recordRows = resizeBatchSlice(index.recordRows, records)
	index.kindIDs = resizeBatchSlice(index.kindIDs, records)
	index.stateIDs = resizeBatchSlice(index.stateIDs, records)
}

func (index *candidateEvidenceIndex) set(row, sourceRow uint32, kind schema.EvidenceKindID, state schema.EvidenceStateID) {
	index.recordRows[row] = sourceRow
	index.kindIDs[row] = kind
	index.stateIDs[row] = state
}

func (index *candidateEvidenceIndex) truncate(rows uint32) {
	index.recordRows = index.recordRows[:rows]
	index.kindIDs = index.kindIDs[:rows]
	index.stateIDs = index.stateIDs[:rows]
}

func (materializer *candidateMaterializer) prepareEvidenceShape(
	ctx context.Context,
	dst *candidateEvidenceShape,
	index *candidateEvidenceIndex,
	rows uint32,
) (uint32, error) {
	if ctx == nil || dst == nil || index == nil || len(index.scenarioOffsets) == 0 ||
		len(index.recordRows) != len(index.kindIDs) || len(index.recordRows) != len(index.stateIDs) {
		return 0, ErrInvalidDomain
	}
	scenarios := len(index.scenarioOffsets) - 1
	refs := uint64(0)
	for row := uint32(0); row < rows; row++ {
		if row&63 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		scenario := materializer.evidenceOptions[row]
		if uint64(scenario) >= uint64(scenarios) {
			if scenarios == 0 && scenario == 0 {
				continue
			}
			return 0, ErrInvalidDomain
		}
		start, end := index.scenarioOffsets[scenario], index.scenarioOffsets[scenario+1]
		if start > end || uint64(end) > uint64(len(index.recordRows)) {
			return 0, ErrInvalidDomain
		}
		refs += uint64(end - start)
		if refs > maxCandidateEvidenceRows {
			return 0, ErrCandidateBudget
		}
		if refs > math.MaxUint32 || refs > uint64(math.MaxInt) {
			return 0, ErrInvalidDomain
		}
	}
	dst.offsets = resizeBatchSlice(dst.offsets, int(rows)+1)
	dst.refs = resizeBatchSlice(dst.refs, int(refs))
	evidenceRow := uint32(0)
	for row := uint32(0); row < rows; row++ {
		dst.offsets[row] = evidenceRow
		if scenarios != 0 {
			scenario := materializer.evidenceOptions[row]
			end := evidenceRow + index.scenarioOffsets[scenario+1] - index.scenarioOffsets[scenario]
			for evidenceRow < end {
				if evidenceRow&63 == 0 {
					if err := ctx.Err(); err != nil {
						return 0, err
					}
				}
				dst.refs[evidenceRow] = evidenceRow
				evidenceRow++
			}
		}
	}
	dst.offsets[rows] = evidenceRow
	return evidenceRow, nil
}

func (materializer *candidateMaterializer) build(
	ctx context.Context,
	builder *eval.Builder,
	evidence *candidateEvidenceShape,
	index *candidateEvidenceIndex,
	compiled *program.Program,
	fieldIDs []schema.FieldID,
	plan *searchPlan,
	domain Domain,
	start uint64,
	rows, evidenceRows uint32,
) (eval.Batch, error) {
	if ctx == nil || index == nil {
		return eval.Batch{}, ErrInvalidDomain
	}
	if err := ctx.Err(); err != nil {
		return eval.Batch{}, err
	}
	if err := builder.Begin(compiled, rows, evidenceRows, evidenceRows); err != nil {
		return eval.Batch{}, err
	}
	for row := uint32(0); row < rows; row++ {
		if err := ctx.Err(); err != nil {
			return eval.Batch{}, err
		}
		if err := builder.SetRequestID(row, schema.RequestID(start+uint64(row)+1)); err != nil {
			return eval.Batch{}, err
		}
		for dimension, fieldRow := range plan.fieldRows {
			if dimension&63 == 0 {
				if err := ctx.Err(); err != nil {
					return eval.Batch{}, err
				}
			}
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
	scenarios := len(index.scenarioOffsets) - 1
	for row := uint32(0); row < rows && scenarios != 0; row++ {
		scenario := materializer.evidenceOptions[row]
		if uint64(scenario) >= uint64(scenarios) {
			return eval.Batch{}, ErrInvalidDomain
		}
		records := domain.EvidenceSets[scenario].Records
		indexStart, indexEnd := index.scenarioOffsets[scenario], index.scenarioOffsets[scenario+1]
		if indexStart > indexEnd || uint64(indexEnd) > uint64(len(index.recordRows)) {
			return eval.Batch{}, ErrInvalidDomain
		}
		for indexRow := indexStart; indexRow < indexEnd; indexRow++ {
			if evidenceRow&63 == 0 {
				if err := ctx.Err(); err != nil {
					return eval.Batch{}, err
				}
			}
			recordRow := index.recordRows[indexRow]
			if uint64(recordRow) >= uint64(len(records)) {
				return eval.Batch{}, ErrInvalidDomain
			}
			record, err := translateIndexedEvidence(
				builder, records[recordRow], recordRow+1, index.kindIDs[indexRow], index.stateIDs[indexRow],
			)
			if err != nil {
				return eval.Batch{}, err
			}
			if err := builder.SetEvidence(evidenceRow, record); err != nil {
				return eval.Batch{}, err
			}
			evidenceRow++
		}
	}
	if evidenceRow != evidenceRows {
		return eval.Batch{}, ErrInvalidDomain
	}
	if err := builder.SetEvidenceCSR(evidence.offsets, evidence.refs); err != nil {
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

func translateIndexedEvidence(
	builder *eval.Builder,
	source Evidence,
	id uint32,
	kind schema.EvidenceKindID,
	state schema.EvidenceStateID,
) (eval.EvidenceRecord, error) {
	if id == 0 || kind == 0 || state == 0 {
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
		ID: schema.EvidenceID(id), Kind: kind, State: state,
		Subject: subject, Scope: scope, Timing: timing,
	}, nil
}

func evidenceCatalogIDs(
	compiled *program.Program,
	source Evidence,
) (schema.EvidenceKindID, schema.EvidenceStateID, bool) {
	kind, kindOK := catalogID(compiled, compiled.EvidenceKindNames, source.Kind)
	state, stateOK := catalogID(compiled, compiled.EvidenceStateNames, source.State)
	return schema.EvidenceKindID(kind), schema.EvidenceStateID(state), kindOK && stateOK
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
