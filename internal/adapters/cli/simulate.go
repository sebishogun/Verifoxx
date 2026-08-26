package cli

import (
	"errors"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sebishogun/nornrune/internal/adapters/jsonresult"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
)

var (
	errMissingOverride   = errors.New("at least one --set field=value is required")
	errInvalidOverride   = errors.New("override must be a known field with a non-empty compatible value")
	errDuplicateOverride = errors.New("a field may be overridden only once")
)

type fieldOverride struct {
	value   string
	integer int64
	field   schema.FieldID
	kind    schema.ValueKind
	boolean bool
}

func parseOverrides(dst []fieldOverride, p *program.Program, assignments []string) ([]fieldOverride, error) {
	original := dst
	if p == nil || len(assignments) == 0 {
		return original, errMissingOverride
	}
	for _, assignment := range assignments {
		separator := strings.IndexByte(assignment, '=')
		if separator <= 0 || separator == len(assignment)-1 {
			return original, errInvalidOverride
		}
		name := assignment[:separator]
		value := assignment[separator+1:]
		field, kind, ok := compiledField(p, name)
		if !ok {
			return original, errInvalidOverride
		}
		for _, existing := range dst {
			if existing.field == field {
				return original, errDuplicateOverride
			}
		}
		override := fieldOverride{field: field, kind: kind}
		switch kind {
		case schema.ValueKindSymbol:
			override.value = value
		case schema.ValueKindInteger, schema.ValueKindTimestamp:
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return original, errInvalidOverride
			}
			override.integer = parsed
		case schema.ValueKindBoolean, schema.ValueKindPresence:
			switch value {
			case "true":
				override.boolean = true
			case "false":
				override.boolean = false
			default:
				return original, errInvalidOverride
			}
		default:
			return original, errInvalidOverride
		}
		dst = append(dst, override)
	}
	return dst, nil
}

func compiledField(p *program.Program, name string) (schema.FieldID, schema.ValueKind, bool) {
	nameID, ok := p.LookupSymbol([]byte(name))
	if !ok || len(p.FieldNames) != len(p.FieldKinds) {
		return 0, 0, false
	}
	for row, candidate := range p.FieldNames {
		if candidate == nameID && p.FieldKinds[row].Valid() {
			return schema.FieldID(row + 1), p.FieldKinds[row], true
		}
	}
	return 0, 0, false
}

func newSimulateCommand(deps dependencies) *cobra.Command {
	var flags sourceFlags
	var assignments []string
	cmd := &cobra.Command{
		Use:   "simulate <request-id>",
		Short: "Evaluate one request with typed field overrides",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			requestID, err := parseRequestID(args[0])
			if err != nil {
				return usageError(err)
			}
			if len(assignments) == 0 {
				return usageError(errMissingOverride)
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
			overrides, err := parseOverrides(nil, compiled, assignments)
			if err != nil {
				return usageError(err)
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
			selected, err := selector.compactWithOverrides(compiled, batch, &engine.batchBuilder, row, overrides)
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
	cmd.Flags().StringArrayVar(&assignments, "set", nil, "override one field=value (repeatable)")
	bindSourceFlags(cmd, &flags, sourceAll)
	return cmd
}
