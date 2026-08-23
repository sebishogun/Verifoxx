// Package app connects process I/O to the command-line adapter.
package app

import (
	"io"

	"github.com/sebishogun/verifoxx/internal/adapters/cli"
)

type emptyReader struct{}

func (emptyReader) Read([]byte) (int, error) { return 0, io.EOF }

// Run dispatches the command-line interface and returns the process exit code.
// It preserves the original no-stdin entry point for callers and tests.
func Run(args []string, stdout, stderr io.Writer) int {
	return RunWithInput(args, emptyReader{}, stdout, stderr)
}

// RunWithInput dispatches the command-line interface with caller-owned I/O.
func RunWithInput(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return cli.Execute(args, stdin, stdout, stderr)
}
