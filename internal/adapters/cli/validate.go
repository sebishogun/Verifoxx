package cli

import (
	"errors"
	"io"

	"github.com/spf13/cobra"

	public "github.com/sebishogun/verifoxx/frontend"
	"github.com/sebishogun/verifoxx/internal/compile"
)

func newValidateCommand(deps dependencies) *cobra.Command {
	var flags sourceFlags
	var frontendFlags frontendFlags
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate a policy document",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			inputs, selection, err := loadFrontendSources(flags, frontendFlags, cmd.InOrStdin(), deps, sourcePolicy)
			if err != nil {
				return classifyCommandError(err)
			}
			if selection.language != public.LanguageNative {
				_, diagnostics, compileErr := compileFrontend(selection, inputs.policy)
				if compileErr != nil {
					return operationalError(compileErr)
				}
				if len(diagnostics) != 0 {
					return writeFrontendDiagnostics(cmd.OutOrStdout(), diagnostics)
				}
				return operationalError(writeComplete(cmd.OutOrStdout(), []byte("{\"valid\":true,\"diagnostics\":[]}\n")))
			}
			var engine engine
			policy, err := engine.decodePolicy(inputs.policy)
			if err != nil {
				return operationalError(err)
			}
			diagnostics := engine.validatePolicy(policy)
			return writeValidationResult(cmd.OutOrStdout(), diagnostics)
		},
	}
	bindSourceFlags(cmd, &flags, sourcePolicy)
	bindFrontendFlags(cmd, &frontendFlags)
	return cmd
}

func writeValidationResult(w io.Writer, diagnostics []compile.Diagnostic) error {
	if len(diagnostics) == 0 {
		return operationalError(writeComplete(w, []byte("{\"valid\":true,\"diagnostics\":[]}\n")))
	}
	if err := writeComplete(w, appendDiagnostics(nil, diagnostics)); err != nil {
		return operationalError(err)
	}
	return &commandError{err: errInvalidPolicy, code: 1, quiet: true}
}

func classifyCommandError(err error) error {
	if err == nil {
		return nil
	}
	var status *commandError
	if errors.As(err, &status) {
		return err
	}
	return operationalError(err)
}
