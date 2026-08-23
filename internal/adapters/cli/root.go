// Package cli implements the scriptable Verifoxx command-line adapter.
package cli

import (
	"bytes"
	"errors"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/sebishogun/verifoxx/internal/buildinfo"
	"github.com/sebishogun/verifoxx/internal/fixtures"
	verifoxx "github.com/sebishogun/verifoxx/policies/verifoxx"
)

type dependencies struct {
	readFile func(string) ([]byte, error)
	policy   string
	requests string
	evidence string
	version  string
}

func productionDependencies() dependencies {
	return dependencies{
		readFile: os.ReadFile,
		policy:   verifoxx.Source(),
		requests: fixtures.RequestsJSON(),
		evidence: fixtures.EvidenceJSON(),
		version:  buildinfo.Version(),
	}
}

type sourceFlags struct {
	policyPath   string
	requestPath  string
	evidencePath string
}

type sourceMask uint8

const (
	sourcePolicy sourceMask = 1 << iota
	sourceRequests
	sourceEvidence
	sourceAll = sourcePolicy | sourceRequests | sourceEvidence
)

type sources struct {
	policy   []byte
	requests []byte
	evidence []byte
}

type commandError struct {
	err   error
	code  int
	quiet bool
}

func (e *commandError) Error() string { return e.err.Error() }
func (e *commandError) Unwrap() error { return e.err }

func operationalError(err error) error {
	if err == nil {
		return nil
	}
	return &commandError{err: err, code: 1}
}

func usageError(err error) error {
	if err == nil {
		return nil
	}
	return &commandError{err: err, code: 2}
}

type trackingWriter struct {
	w   io.Writer
	err error
}

func (w *trackingWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.w == nil {
		w.err = errors.New("cli: nil output writer")
		return 0, w.err
	}
	n, err := w.w.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = err
	}
	return n, err
}

// Execute runs one command with caller-owned I/O and returns its process exit
// code. It never exits the process itself.
func Execute(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return executeWithDependencies(args, stdin, stdout, stderr, productionDependencies())
}

func executeWithDependencies(args []string, stdin io.Reader, stdout, stderr io.Writer, deps dependencies) int {
	if stdin == nil {
		stdin = bytes.NewReader(nil)
	}
	out := trackingWriter{w: stdout}
	errOut := trackingWriter{w: stderr}
	root := newRoot(&out, &errOut, deps)
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(&out)
	root.SetErr(&errOut)

	var err error
	if len(args) > 1 && (args[0] == "--help" || args[0] == "-h") {
		err = errors.New("help accepts no arguments")
	} else {
		err = root.Execute()
	}
	if out.err != nil || errOut.err != nil {
		return 1
	}
	if err == nil {
		return 0
	}

	code := 2
	quiet := false
	var status *commandError
	if errors.As(err, &status) {
		code = status.code
		quiet = status.quiet
	}
	if quiet {
		return code
	}
	if _, writeErr := io.WriteString(&errOut, "Error: "+err.Error()+"\n"); writeErr != nil {
		return 1
	}
	if code == 2 {
		root.SetOut(&errOut)
		if usageErr := root.Usage(); usageErr != nil {
			return 1
		}
	}
	if errOut.err != nil {
		return 1
	}
	return code
}

func newRoot(stdout, stderr io.Writer, deps dependencies) *cobra.Command {
	var version bool
	root := &cobra.Command{
		Use:           "verifoxx",
		Short:         "Evidence-aware policy engine",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if version {
				_, err := io.WriteString(cmd.OutOrStdout(), deps.version+"\n")
				return operationalError(err)
			}
			return operationalError(cmd.Help())
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.Flags().BoolVar(&version, "version", false, "print the build version")
	root.AddCommand(
		newEvaluateCommand(deps),
		newValidateCommand(deps),
		newCompileCommand(deps),
		newExplainCommand(deps),
		newSimulateCommand(deps),
		newDemoCommand(deps),
	)
	root.SetHelpCommand(&cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		RunE: func(_ *cobra.Command, args []string) error {
			target := root
			if len(args) != 0 {
				found, remaining, err := root.Find(args)
				if err != nil || found == root || len(remaining) != 0 {
					return usageError(errors.New("unknown help topic"))
				}
				target = found
			}
			return operationalError(target.Help())
		},
	})
	return root
}

func bindSourceFlags(cmd *cobra.Command, flags *sourceFlags, need sourceMask) {
	if need&sourcePolicy != 0 {
		cmd.Flags().StringVar(&flags.policyPath, "policy", "", "policy JSON path (default embedded, - for stdin)")
	}
	if need&sourceRequests != 0 {
		cmd.Flags().StringVar(&flags.requestPath, "requests", "", "request JSON path (default embedded, - for stdin)")
	}
	if need&sourceEvidence != 0 {
		cmd.Flags().StringVar(&flags.evidencePath, "evidence", "", "evidence JSON path (default embedded, - for stdin)")
	}
}

func loadSources(flags sourceFlags, stdin io.Reader, deps dependencies, need sourceMask) (sources, error) {
	stdinCount := 0
	if need&sourcePolicy != 0 && flags.policyPath == "-" {
		stdinCount++
	}
	if need&sourceRequests != 0 && flags.requestPath == "-" {
		stdinCount++
	}
	if need&sourceEvidence != 0 && flags.evidencePath == "-" {
		stdinCount++
	}
	if stdinCount > 1 {
		return sources{}, usageError(errors.New("only one input may read from stdin"))
	}

	var result sources
	var err error
	if need&sourcePolicy != 0 {
		result.policy, err = loadSource(flags.policyPath, deps.policy, stdin, deps.readFile)
		if err != nil {
			return sources{}, err
		}
	}
	if need&sourceRequests != 0 {
		result.requests, err = loadSource(flags.requestPath, deps.requests, stdin, deps.readFile)
		if err != nil {
			return sources{}, err
		}
	}
	if need&sourceEvidence != 0 {
		result.evidence, err = loadSource(flags.evidencePath, deps.evidence, stdin, deps.readFile)
		if err != nil {
			return sources{}, err
		}
	}
	return result, nil
}

func loadSource(path, embedded string, stdin io.Reader, readFile func(string) ([]byte, error)) ([]byte, error) {
	switch path {
	case "":
		return []byte(embedded), nil
	case "-":
		if stdin == nil {
			return nil, errors.New("stdin is unavailable")
		}
		return io.ReadAll(stdin)
	default:
		if readFile == nil {
			return nil, errors.New("file input is unavailable")
		}
		return readFile(path)
	}
}
