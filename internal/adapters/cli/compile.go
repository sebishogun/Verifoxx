package cli

import (
	"errors"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/sebishogun/verifoxx/internal/program"
)

var errInvalidCompiledMetadata = errors.New("compiled policy has invalid metadata")

func newCompileCommand(deps dependencies) *cobra.Command {
	var flags sourceFlags
	cmd := &cobra.Command{
		Use:   "compile",
		Short: "Compile and summarize a policy document",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			inputs, err := loadSources(flags, cmd.InOrStdin(), deps, sourcePolicy)
			if err != nil {
				return classifyCommandError(err)
			}
			var engine engine
			compiled, err := engine.compilePolicy(inputs.policy)
			if err != nil {
				return operationalError(err)
			}
			encoded, err := appendCompileSummary(nil, compiled)
			if err != nil {
				return operationalError(err)
			}
			return operationalError(writeComplete(cmd.OutOrStdout(), encoded))
		},
	}
	bindSourceFlags(cmd, &flags, sourcePolicy)
	return cmd
}

func appendCompileSummary(dst []byte, compiled *program.Program) ([]byte, error) {
	if compiled == nil {
		return dst, errInvalidCompiledMetadata
	}
	name, ok := compiled.Symbol(compiled.PolicyName)
	if !ok {
		return dst, errInvalidCompiledMetadata
	}
	version, ok := compiled.Symbol(compiled.PolicyVersion)
	if !ok {
		return dst, errInvalidCompiledMetadata
	}
	dst = append(dst, "{\"name\":"...)
	dst = appendOutputString(dst, name)
	dst = append(dst, ",\"version\":"...)
	dst = appendOutputString(dst, version)
	dst = append(dst, ",\"sha256\":\""...)
	dst = appendOutputHash(dst, compiled.ContentHash)
	dst = append(dst, "\",\"instructions\":"...)
	dst = strconv.AppendUint(dst, uint64(len(compiled.Opcodes)), 10)
	dst = append(dst, ",\"requirements\":"...)
	dst = strconv.AppendUint(dst, uint64(len(compiled.RequirementIDs)), 10)
	dst = append(dst, ",\"clauses\":"...)
	dst = strconv.AppendUint(dst, uint64(len(compiled.ClauseAssertionRoots)), 10)
	dst = append(dst, ",\"truth_slots\":"...)
	dst = strconv.AppendUint(dst, uint64(compiled.TruthSlotCount), 10)
	dst = append(dst, ",\"reason_slots\":"...)
	dst = strconv.AppendUint(dst, uint64(compiled.ReasonSlotCount), 10)
	return append(dst, "}\n"...), nil
}
