package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGraphCommandHelpAndValidation(t *testing.T) {
	code, stdout, stderr := runCLI(t, "graph", "--help")
	if code != 0 || stderr != "" {
		t.Fatalf("graph --help = (%d,%q,%q)", code, stdout, stderr)
	}
	for _, flag := range []string{"--view", "--format", "--output", "--force", "--policy", "--requests", "--evidence"} {
		if !strings.Contains(stdout, flag) {
			t.Errorf("graph help lacks %s", flag)
		}
	}

	for _, args := range [][]string{
		{"graph"},
		{"graph", "--output", filepath.Join(t.TempDir(), "graph.svg"), "--view", "source"},
		{"graph", "--output", filepath.Join(t.TempDir(), "graph.svg"), "--format", "png"},
		{"graph", "--output", filepath.Join(t.TempDir(), "graph.svg"), "extra"},
	} {
		code, stdout, stderr = runCLI(t, args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "Usage:") {
			t.Errorf("%q = (%d,%q,%q), want usage error", args, code, stdout, stderr)
		}
	}
}

func TestGraphCommandWritesAllDeterministicFormats(t *testing.T) {
	formats := []struct {
		name string
		want string
	}{{"dot", "digraph nornrune"}, {"svg", `<svg xmlns="http://www.w3.org/2000/svg"`}, {"html", "<!doctype html>"}}
	for _, format := range formats {
		for _, view := range []string{"ast", "program"} {
			t.Run(format.name+"/"+view, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "semantic."+format.name)
				code, stdout, stderr := runCLI(t, "graph", "--view", view, "--format", format.name, "--output", path)
				if code != 0 || stdout != "" || stderr != "" {
					t.Fatalf("graph = (%d,%q,%q)", code, stdout, stderr)
				}
				first, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Contains(first, []byte(format.want)) {
					t.Fatalf("%s output lacks %q", format.name, format.want)
				}
				if format.name == "html" && !bytes.Contains(first, []byte(`data-initial-view="`+view+`"`)) {
					t.Fatalf("HTML output does not select initial %s view", view)
				}
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("output mode = %v, error=%v, want 0600", info.Mode().Perm(), err)
				}
				code, stdout, stderr = runCLI(t, "graph", "--view", view, "--format", format.name, "--output", path, "--force")
				if code != 0 || stdout != "" || stderr != "" {
					t.Fatalf("forced graph = (%d,%q,%q)", code, stdout, stderr)
				}
				second, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(first, second) {
					t.Fatalf("forced output changed: error=%v", err)
				}
			})
		}
	}
}

func TestGraphCommandRejectsExistingFileUnlessForced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "graph.dot")
	original := []byte("keep me")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI(t, "graph", "--format", "dot", "--output", path)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "exists") {
		t.Fatalf("existing graph = (%d,%q,%q)", code, stdout, stderr)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("existing output changed: %q error=%v", got, err)
	}
}

func TestGraphCommandUsesSourceFlagsAndInjectedAtomicWriter(t *testing.T) {
	deps := productTestDependencies()
	var outputPath string
	var output []byte
	var forced bool
	deps.writeGraphFile = func(path string, data []byte, force bool) error {
		outputPath = path
		output = append(output[:0], data...)
		forced = force
		return nil
	}
	code, stdout, stderr := runCLIWithDependencies(t, deps,
		"graph", "--policy", "valid-policy.json", "--requests", "requests.json", "--evidence", "evidence.json",
		"--view", "program", "--format", "svg", "--output", "semantic.svg", "--force")
	if code != 0 || stdout != "" || stderr != "" || outputPath != "semantic.svg" || !forced ||
		!bytes.Contains(output, []byte(`id="graph-graph"`)) {
		t.Fatalf("injected graph = (%d,%q,%q,path=%q,force=%v,bytes=%d)", code, stdout, stderr, outputPath, forced, len(output))
	}
}

func TestGraphCommandRejectsMalformedSourcesBeforeWrite(t *testing.T) {
	for _, source := range []string{"policy", "requests", "evidence"} {
		t.Run(source, func(t *testing.T) {
			deps := productTestDependencies()
			called := false
			deps.writeGraphFile = func(string, []byte, bool) error { called = true; return nil }
			args := []string{"graph", "--output", "unused.svg"}
			switch source {
			case "policy":
				args = append(args, "--policy", "malformed-policy.json")
			case "requests":
				args = append(args, "--requests", "malformed-requests.json")
			case "evidence":
				args = append(args, "--evidence", "malformed-evidence.json")
			}
			code, stdout, stderr := runCLIWithDependencies(t, deps, args...)
			if code != 1 || stdout != "" || stderr == "" || called {
				t.Fatalf("malformed %s = (%d,%q,%q,write=%v)", source, code, stdout, stderr, called)
			}
		})
	}
}

func TestGraphCommandReturnsWriteFailureWithoutPartialDestination(t *testing.T) {
	deps := productTestDependencies()
	wantErr := errors.New("storage unavailable")
	deps.writeGraphFile = func(string, []byte, bool) error { return wantErr }
	code, stdout, stderr := runCLIWithDependencies(t, deps, "graph", "--output", "semantic.svg")
	if code != 1 || stdout != "" || !strings.Contains(stderr, wantErr.Error()) {
		t.Fatalf("write failure = (%d,%q,%q)", code, stdout, stderr)
	}

	directory := t.TempDir()
	path := filepath.Join(directory, "missing", "graph.svg")
	if err := writeAtomicGraphFile(path, []byte("complete"), false); err == nil {
		t.Fatal("writeAtomicGraphFile accepted a missing parent")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial destination exists: %v", err)
	}
	temporary, err := filepath.Glob(filepath.Join(directory, ".graph.svg.tmp-*"))
	if err != nil || len(temporary) != 0 {
		t.Fatalf("temporary outputs = %v, error=%v", temporary, err)
	}
}
