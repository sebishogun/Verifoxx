package cli

import (
	"errors"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/sebishogun/nornrune/internal/adapters/jsondiff"
	policydiff "github.com/sebishogun/nornrune/policy/diff"
)

const (
	maxDiffPolicyBytes    = 16 << 20
	maxDiffConfigBytes    = 4 << 20
	maxDiffExceptionBytes = 1 << 20
)

type diffFlags struct {
	oldPolicyPath  string
	newPolicyPath  string
	domainPath     string
	exceptionsPath string
	format         string
}

func newDiffCommand(deps dependencies) *cobra.Command {
	var flags diffFlags
	command := &cobra.Command{
		Use:   "diff",
		Short: "Compare two native policies over a finite domain",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if flags.oldPolicyPath == "" || flags.newPolicyPath == "" || flags.domainPath == "" {
				return usageError(errors.New("--old-policy, --new-policy, and --domain are required"))
			}
			if flags.format != "json" && flags.format != "text" {
				return usageError(errors.New("--format must be json or text"))
			}
			paths := [...]string{flags.oldPolicyPath, flags.newPolicyPath, flags.domainPath, flags.exceptionsPath}
			stdinCount := 0
			for _, path := range paths {
				if path == "-" {
					stdinCount++
				}
			}
			if stdinCount > 1 {
				return usageError(errors.New("only one input may read from stdin"))
			}
			oldSource, err := loadDiffSource(flags.oldPolicyPath, maxDiffPolicyBytes, command.InOrStdin(), deps)
			if err != nil {
				return operationalError(err)
			}
			newSource, err := loadDiffSource(flags.newPolicyPath, maxDiffPolicyBytes, command.InOrStdin(), deps)
			if err != nil {
				return operationalError(err)
			}
			configSource, err := loadDiffSource(flags.domainPath, maxDiffConfigBytes, command.InOrStdin(), deps)
			if err != nil {
				return operationalError(err)
			}
			config, err := jsondiff.DecodeConfig(configSource, jsondiff.Limits{
				MaxBytes: maxDiffConfigBytes, MaxFields: 4096, MaxValues: 1 << 17,
				MaxEvidenceSets: 4096, MaxEvidenceRecords: 1 << 17,
			})
			if err != nil {
				return operationalError(err)
			}
			exceptions, err := loadDiffExceptions(flags.exceptionsPath, command.InOrStdin(), deps)
			if err != nil {
				return operationalError(err)
			}
			var analyzer policydiff.Analyzer
			var result policydiff.Result
			if err := analyzer.Compare(command.Context(), &result, oldSource, newSource, config.Fields, config.Domain, config.Matrix, nil); err != nil {
				return operationalError(err)
			}
			var encoded []byte
			if flags.format == "text" {
				encoded = jsondiff.AppendResultText(nil, result)
			} else {
				encoded = jsondiff.AppendResultJSON(nil, result)
			}
			if err := writeComplete(command.OutOrStdout(), encoded); err != nil {
				return operationalError(err)
			}
			if result.Outcome == policydiff.Inconclusive {
				return &commandError{err: errors.New("comparison inconclusive"), code: 4, quiet: true}
			}
			if result.Forbidden {
				now := time.Now()
				if deps.now != nil {
					now = deps.now()
				}
				decision := policydiff.CheckRegression(result, oldSource, newSource, exceptions, now)
				if !decision.Allowed {
					return &commandError{err: errors.New("forbidden policy regression"), code: 3, quiet: true}
				}
			}
			return nil
		},
	}
	command.Flags().StringVar(&flags.oldPolicyPath, "old-policy", "", "old native policy JSON path (- for stdin)")
	command.Flags().StringVar(&flags.newPolicyPath, "new-policy", "", "new native policy JSON path (- for stdin)")
	command.Flags().StringVar(&flags.domainPath, "domain", "", "finite-domain JSON path (- for stdin)")
	command.Flags().StringVar(&flags.exceptionsPath, "exceptions", "", "optional regression exceptions JSON path (- for stdin)")
	command.Flags().StringVar(&flags.format, "format", "json", "output format: json or text")
	return command
}

func loadDiffSource(path string, maximum int, stdin io.Reader, deps dependencies) ([]byte, error) {
	var source []byte
	var err error
	if path == "-" {
		if stdin == nil {
			return nil, errors.New("stdin is unavailable")
		}
		source, err = io.ReadAll(io.LimitReader(stdin, int64(maximum)+1))
	} else if deps.readBoundedFile != nil {
		source, err = deps.readBoundedFile(path, uint32(maximum))
	} else if deps.readFile != nil {
		source, err = deps.readFile(path)
	} else {
		return nil, errors.New("file input is unavailable")
	}
	if err != nil {
		return nil, err
	}
	if len(source) > maximum {
		return nil, errors.New("input exceeds size limit")
	}
	return source, nil
}

func loadDiffExceptions(path string, stdin io.Reader, deps dependencies) ([]policydiff.Exception, error) {
	if path == "" {
		return nil, nil
	}
	source, err := loadDiffSource(path, maxDiffExceptionBytes, stdin, deps)
	if err != nil {
		return nil, err
	}
	return policydiff.DecodeExceptions(source, 4096)
}
