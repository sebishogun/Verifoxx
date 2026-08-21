package app

import (
	"bytes"
	"errors"
	"testing"
)

type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) { return 0, errors.New("write failed") }

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"--version"}, &out, &errOut); code != 0 {
		t.Fatalf("Run(--version) = %d, want 0", code)
	}
	if got := out.String(); got != "devel\n" {
		t.Fatalf("Run(--version) stdout = %q, want %q", got, "devel\n")
	}
	if errOut.Len() != 0 {
		t.Fatalf("Run(--version) wrote to stderr: %q", errOut.String())
	}
}

func TestRunHelpFlags(t *testing.T) {
	for _, flag := range []string{"help", "--help", "-h"} {
		var out, errOut bytes.Buffer
		if code := Run([]string{flag}, &out, &errOut); code != 0 {
			t.Fatalf("Run(%q) = %d, want 0", flag, code)
		}
		if out.Len() == 0 {
			t.Fatalf("Run(%q) wrote no help text", flag)
		}
		if errOut.Len() != 0 {
			t.Fatalf("Run(%q) wrote to stderr: %q", flag, errOut.String())
		}
	}
}

func TestRunNoArgs(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run(nil, &out, &errOut); code != 0 {
		t.Fatalf("Run() = %d, want 0", code)
	}
	if out.Len() == 0 {
		t.Fatal("Run() with no args wrote no help text")
	}
	if errOut.Len() != 0 {
		t.Fatalf("Run() wrote to stderr: %q", errOut.String())
	}
}

func TestRunUnknown(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"bogus"}, &out, &errOut); code != 2 {
		t.Fatalf("Run(bogus) = %d, want 2", code)
	}
	if out.Len() != 0 {
		t.Fatalf("Run(bogus) wrote to stdout: %q, want empty", out.String())
	}
	if errOut.Len() == 0 {
		t.Fatal("Run(bogus) wrote no usage to stderr")
	}
}

func TestRunTrailingArgs(t *testing.T) {
	for _, cmd := range []string{"--version", "help", "--help", "-h"} {
		var out, errOut bytes.Buffer
		if code := Run([]string{cmd, "extra"}, &out, &errOut); code != 2 {
			t.Fatalf("Run(%q extra) = %d, want 2", cmd, code)
		}
		if out.Len() != 0 {
			t.Fatalf("Run(%q extra) wrote to stdout: %q, want empty", cmd, out.String())
		}
		if errOut.Len() == 0 {
			t.Fatalf("Run(%q extra) wrote no usage to stderr", cmd)
		}
	}
}

func TestRunVersionStdoutFailure(t *testing.T) {
	var errOut bytes.Buffer
	if code := Run([]string{"--version"}, errWriter{}, &errOut); code != 1 {
		t.Fatalf("Run(--version) with failing stdout = %d, want 1", code)
	}
}

func TestRunHelpStdoutFailure(t *testing.T) {
	var errOut bytes.Buffer
	if code := Run([]string{"help"}, errWriter{}, &errOut); code != 1 {
		t.Fatalf("Run(help) with failing stdout = %d, want 1", code)
	}
}

func TestRunUsageStderrFailure(t *testing.T) {
	var out bytes.Buffer
	if code := Run([]string{"bogus"}, &out, errWriter{}); code != 1 {
		t.Fatalf("Run(bogus) with failing stderr = %d, want 1", code)
	}
}
