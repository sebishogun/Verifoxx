package cli

import (
	"errors"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/sebishogun/verifoxx/internal/adapters/jsonresult"
	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

var (
	errInvalidRequestID = errors.New("request ID must be R followed by a nonzero decimal uint32")
	errRequestNotFound  = errors.New("request ID is not present in the selected input")
	errInvalidBatch     = errors.New("selected request batch is malformed")
)

func parseRequestID(value string) (schema.RequestID, error) {
	if len(value) < 2 || value[0] != 'R' {
		return 0, errInvalidRequestID
	}
	n, err := strconv.ParseUint(value[1:], 10, 32)
	if err != nil || n == 0 {
		return 0, errInvalidRequestID
	}
	return schema.RequestID(n), nil
}

type rowSelector struct {
	refs    []uint32
	builder eval.Builder
}

// compact copies one row from a full Builder batch. Range views retain a
// different physical stride and are rejected by validCompactFactShape.
func (s *rowSelector) compact(
	p *program.Program,
	source eval.Batch,
	sourceSymbols *eval.Builder,
	row uint32,
) (eval.Batch, error) {
	return s.compactWithOverrides(p, source, sourceSymbols, row, nil)
}

func (s *rowSelector) compactWithOverrides(
	p *program.Program,
	source eval.Batch,
	sourceSymbols *eval.Builder,
	row uint32,
	overrides []fieldOverride,
) (eval.Batch, error) {
	if s == nil || p == nil || sourceSymbols == nil || row >= source.Rows ||
		!validCompactFactShape(p, source) || source.RequestIDs[row] == 0 {
		return eval.Batch{}, errInvalidBatch
	}
	for i, override := range overrides {
		kind, _, ok := p.FieldIndex.Lookup(override.field)
		if !ok || kind != override.kind {
			return eval.Batch{}, errInvalidOverride
		}
		for _, previous := range overrides[:i] {
			if previous.field == override.field {
				return eval.Batch{}, errDuplicateOverride
			}
		}
	}
	evidenceStart, evidenceEnd, ok := source.EvidenceRange(row)
	if !ok {
		return eval.Batch{}, errInvalidBatch
	}
	evidenceCount := evidenceEnd - evidenceStart
	if err := s.builder.Begin(p, 1, evidenceCount, evidenceCount); err != nil {
		return eval.Batch{}, err
	}
	fail := func(err error) (eval.Batch, error) {
		s.builder.Abort()
		return eval.Batch{}, err
	}
	if err := s.builder.SetRequestID(0, source.RequestIDs[row]); err != nil {
		return fail(err)
	}

	for fieldRow := range p.FieldIndex.Kinds {
		field := schema.FieldID(fieldRow + 1)
		if override, ok := findFieldOverride(overrides, field); ok {
			if err := applyFieldOverride(&s.builder, override); err != nil {
				return fail(err)
			}
			continue
		}
		if !source.Present(field, row) {
			continue
		}
		kind, column, ok := p.FieldIndex.Lookup(field)
		if !ok {
			return fail(errInvalidBatch)
		}
		valueIndex := uint64(column)*uint64(source.Rows) + uint64(row)
		var err error
		switch kind {
		case schema.ValueKindSymbol:
			if valueIndex >= uint64(len(source.SymbolValues)) {
				return fail(errInvalidBatch)
			}
			value, resolveErr := internSelectedSymbol(&s.builder, sourceSymbols, source.SymbolValues[int(valueIndex)])
			if resolveErr != nil {
				return fail(resolveErr)
			}
			err = s.builder.SetSymbol(0, field, value)
		case schema.ValueKindInteger:
			if valueIndex >= uint64(len(source.IntegerValues)) {
				return fail(errInvalidBatch)
			}
			err = s.builder.SetInteger(0, field, source.IntegerValues[int(valueIndex)])
		case schema.ValueKindBoolean:
			err = s.builder.SetBoolean(0, field, source.Boolean(column, row))
		case schema.ValueKindTimestamp:
			if valueIndex >= uint64(len(source.TimestampValues)) {
				return fail(errInvalidBatch)
			}
			err = s.builder.SetTimestamp(0, field, source.TimestampValues[int(valueIndex)])
		case schema.ValueKindPresence:
			err = s.builder.SetPresent(0, field)
		default:
			return fail(errInvalidBatch)
		}
		if err != nil {
			return fail(err)
		}
	}

	if cap(s.refs) < int(evidenceCount) {
		s.refs = make([]uint32, evidenceCount)
	} else {
		s.refs = s.refs[:evidenceCount]
	}
	for selectedRow := uint32(0); selectedRow < evidenceCount; selectedRow++ {
		edge := uint64(evidenceStart) + uint64(selectedRow)
		if edge >= uint64(len(source.EvidenceRefs)) {
			return fail(errInvalidBatch)
		}
		sourceRow := source.EvidenceRefs[int(edge)]
		record, err := selectedEvidenceRecord(source, sourceSymbols, &s.builder, sourceRow)
		if err != nil {
			return fail(err)
		}
		if err := s.builder.SetEvidence(selectedRow, record); err != nil {
			return fail(err)
		}
		s.refs[selectedRow] = selectedRow
	}
	offsets := [2]uint32{0, evidenceCount}
	if err := s.builder.SetEvidenceCSR(offsets[:], s.refs); err != nil {
		return fail(err)
	}
	selected, err := s.builder.Finish()
	if err != nil {
		return fail(err)
	}
	return selected, nil
}

func validCompactFactShape(p *program.Program, source eval.Batch) bool {
	if p == nil || len(p.FieldIndex.Kinds) != len(p.FieldIndex.Columns) ||
		uint64(len(source.RequestIDs)) != uint64(source.Rows) {
		return false
	}
	rows := uint64(source.Rows)
	words := (rows + 63) >> 6
	counts := p.FieldIndex.Counts
	return uint64(len(source.SymbolValues)) == uint64(counts[schema.ValueKindSymbol])*rows &&
		uint64(len(source.IntegerValues)) == uint64(counts[schema.ValueKindInteger])*rows &&
		uint64(len(source.BooleanValues)) == uint64(counts[schema.ValueKindBoolean])*words &&
		uint64(len(source.TimestampValues)) == uint64(counts[schema.ValueKindTimestamp])*rows &&
		uint64(len(source.PresenceMasks)) == uint64(len(p.FieldIndex.Kinds))*words
}

func findFieldOverride(overrides []fieldOverride, field schema.FieldID) (fieldOverride, bool) {
	for _, override := range overrides {
		if override.field == field {
			return override, true
		}
	}
	return fieldOverride{}, false
}

func applyFieldOverride(builder *eval.Builder, override fieldOverride) error {
	switch override.kind {
	case schema.ValueKindSymbol:
		value, err := builder.InternSymbol([]byte(override.value))
		if err != nil {
			return err
		}
		return builder.SetSymbol(0, override.field, value)
	case schema.ValueKindInteger:
		return builder.SetInteger(0, override.field, override.integer)
	case schema.ValueKindBoolean:
		return builder.SetBoolean(0, override.field, override.boolean)
	case schema.ValueKindTimestamp:
		return builder.SetTimestamp(0, override.field, override.integer)
	case schema.ValueKindPresence:
		if !override.boolean {
			return nil
		}
		return builder.SetPresent(0, override.field)
	default:
		return errInvalidOverride
	}
}

func internSelectedSymbol(dst, source *eval.Builder, id schema.SymbolID) (schema.SymbolID, error) {
	if id == 0 {
		return 0, nil
	}
	value, ok := source.Symbol(id)
	if !ok {
		return 0, errInvalidBatch
	}
	return dst.InternSymbol(value)
}

func selectedEvidenceRecord(
	source eval.Batch,
	sourceSymbols, destinationSymbols *eval.Builder,
	row uint32,
) (eval.EvidenceRecord, error) {
	if uint64(row) >= uint64(source.Evidence.Len()) ||
		uint64(source.Evidence.Len()) != uint64(len(source.Evidence.Kinds)) ||
		uint64(source.Evidence.Len()) != uint64(len(source.Evidence.States)) ||
		uint64(source.Evidence.Len()) != uint64(len(source.Evidence.Subjects)) ||
		uint64(source.Evidence.Len()) != uint64(len(source.Evidence.Scopes)) ||
		uint64(source.Evidence.Len()) != uint64(len(source.Evidence.Reviewers)) ||
		uint64(source.Evidence.Len()) != uint64(len(source.Evidence.Timings)) ||
		uint64(source.Evidence.Len()) != uint64(len(source.Evidence.Timestamps)) {
		return eval.EvidenceRecord{}, errInvalidBatch
	}
	subject, err := internSelectedSymbol(destinationSymbols, sourceSymbols, source.Evidence.Subjects[row])
	if err != nil {
		return eval.EvidenceRecord{}, err
	}
	scope, err := internSelectedSymbol(destinationSymbols, sourceSymbols, source.Evidence.Scopes[row])
	if err != nil {
		return eval.EvidenceRecord{}, err
	}
	reviewer, err := internSelectedSymbol(destinationSymbols, sourceSymbols, source.Evidence.Reviewers[row])
	if err != nil {
		return eval.EvidenceRecord{}, err
	}
	timing, err := internSelectedSymbol(destinationSymbols, sourceSymbols, source.Evidence.Timings[row])
	if err != nil {
		return eval.EvidenceRecord{}, err
	}
	return eval.EvidenceRecord{
		Timestamp: source.Evidence.Timestamps[row],
		ID:        source.Evidence.IDs[row],
		Kind:      source.Evidence.Kinds[row],
		State:     source.Evidence.States[row],
		Subject:   subject,
		Scope:     scope,
		Reviewer:  reviewer,
		Timing:    timing,
	}, nil
}

func newExplainCommand(deps dependencies) *cobra.Command {
	var flags sourceFlags
	cmd := &cobra.Command{
		Use:   "explain <request-id>",
		Short: "Evaluate and explain one request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			requestID, err := parseRequestID(args[0])
			if err != nil {
				return usageError(err)
			}
			inputs, err := loadSources(flags, cmd.InOrStdin(), deps, sourceAll)
			if err != nil {
				return classifyCommandError(err)
			}
			var engine engine
			compiled, err := engine.compilePolicy(inputs.policy)
			if err != nil {
				return operationalError(err)
			}
			batch, err := engine.decodeBatch(compiled, inputs.requests, inputs.evidence)
			if err != nil {
				return operationalError(err)
			}
			row := batch.Rows
			for candidate, id := range batch.RequestIDs {
				if id == requestID {
					row = uint32(candidate)
					break
				}
			}
			if row == batch.Rows {
				return usageError(errRequestNotFound)
			}
			var selector rowSelector
			selected, err := selector.compact(compiled, batch, &engine.batchBuilder, row)
			if err != nil {
				return operationalError(pipelineFailure("select request", err))
			}
			decisions, err := engine.evaluate(compiled, selected)
			if err != nil {
				return operationalError(err)
			}
			var encoder jsonresult.Encoder
			if err := encoder.Bind(compiled); err != nil {
				return operationalError(pipelineFailure("encode results", err))
			}
			encoded, err := encoder.Append(nil, selected.RequestIDs, decisions, []byte(deps.version))
			if err != nil {
				return operationalError(pipelineFailure("encode results", err))
			}
			return operationalError(writeComplete(cmd.OutOrStdout(), encoded))
		},
	}
	bindSourceFlags(cmd, &flags, sourceAll)
	return cmd
}
