package app

import (
	"io"

	"github.com/sebishogun/verifoxx/internal/buildinfo"
)

const helpText = `verifoxx - evidence-aware policy engine

Usage:
  verifoxx <command>

Commands:
  help       show this help
  --version  print the build version
`

// Run dispatches the command-line interface and returns the process exit code.
// Output goes to the caller-provided writers so tests can capture it.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return runHelp(stdout)
	}
	switch args[0] {
	case "--version":
		if len(args) != 1 {
			return runUsage(stderr)
		}
		return runVersion(stdout)
	case "help", "--help", "-h":
		if len(args) != 1 {
			return runUsage(stderr)
		}
		return runHelp(stdout)
	default:
		return runUsage(stderr)
	}
}

func runVersion(w io.Writer) int {
	if _, err := io.WriteString(w, buildinfo.Version()+"\n"); err != nil {
		return 1
	}
	return 0
}

func runHelp(w io.Writer) int {
	if _, err := io.WriteString(w, helpText); err != nil {
		return 1
	}
	return 0
}

func runUsage(w io.Writer) int {
	if _, err := io.WriteString(w, helpText); err != nil {
		return 1
	}
	return 2
}
