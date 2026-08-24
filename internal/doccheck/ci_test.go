package doccheck_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fieldAlignmentVersion = "v0.47.1-0.20260707181000-a299dadba899"

func TestCIAndReleaseConfigurationCoversRequiredLanes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path     string
		required []string
	}{
		{
			path: ".github/workflows/ci.yml",
			required: []string{
				"name: CI", "native:", "purego:", "scalar-386:", "unit:", "race:",
				"generated:", "docs:", "release-snapshot:", "-tags=purego", "GOARCH=386",
				"go run ./cmd/devx test", "go run ./cmd/devx test:unit", "go run ./cmd/devx test:race",
				"go run ./cmd/devx policy:check", "go run ./cmd/devx results:check",
				"go run ./cmd/devx proto:check", "./internal/doccheck", "release --snapshot --clean",
			},
		},
		{
			path: ".github/workflows/integration.yml",
			required: []string{
				"name: Integration", "integration:", "docker:",
				"go run ./cmd/devx test:integration", "go run ./cmd/devx docker:build",
			},
		},
		{
			path: ".github/workflows/release.yml",
			required: []string{
				"name: Release", "tags:", "'v*'", "contents: write", "fetch-depth: 0",
				"goreleaser/goreleaser-action", "release --clean",
			},
		},
		{
			path: ".goreleaser.yaml",
			required: []string{
				"version: 2", "main: ./cmd/verifoxx", "CGO_ENABLED=0", "-trimpath",
				"goos:", "- linux", "- darwin", "goarch:", "- amd64", "- arm64",
				"internal/buildinfo.version={{.Version}}", "archives:", "checksum:", "changelog:",
			},
		},
	}
	for _, test := range tests {
		content := readDocument(t, test.path)
		for _, required := range test.required {
			if !strings.Contains(content, required) {
				t.Errorf("%s does not contain %q", test.path, required)
			}
		}
	}
}

func TestCICommandsAndJobsHaveExplicitTimeouts(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		".github/workflows/ci.yml",
		".github/workflows/integration.yml",
		".github/workflows/release.yml",
	} {
		content := readDocument(t, path)
		if !strings.Contains(content, "timeout-minutes:") {
			t.Errorf("%s has no job or step timeout", path)
		}
		for lineNumber, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "run: ") {
				continue
			}
			if command := strings.TrimSpace(strings.TrimPrefix(line, "run: ")); !strings.HasPrefix(command, "timeout ") {
				t.Errorf("%s:%d command has no process timeout: %s", path, lineNumber+1, command)
			}
		}
	}
}

func TestFieldAlignmentGateIsPinnedAndSharedWithCI(t *testing.T) {
	scriptPath := "scripts/check-fieldalignment.sh"
	script := readDocument(t, scriptPath)
	for _, required := range []string{
		"set -eu",
		"timeout 240s",
		"fieldalignment@" + fieldAlignmentVersion,
		"-test=false",
		"./internal/...",
		"./cmd/...",
		"./policies/...",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("%s does not contain %q", scriptPath, required)
		}
	}
	info, err := os.Stat(filepath.Join(repositoryRoot, scriptPath))
	if err != nil {
		t.Fatalf("stat %s: %v", scriptPath, err)
	}
	if info.Mode()&0o111 == 0 {
		t.Errorf("%s is not executable", scriptPath)
	}

	ci := readDocument(t, ".github/workflows/ci.yml")
	for _, required := range []string{
		"name: Check production field alignment",
		"run: timeout 300s ./scripts/check-fieldalignment.sh",
	} {
		if !strings.Contains(ci, required) {
			t.Errorf("CI does not contain %q", required)
		}
	}
}

func TestFieldAlignmentGatePropagatesTimeoutFailure(t *testing.T) {
	tools := t.TempDir()
	fakeTimeout := filepath.Join(tools, "timeout")
	if err := os.WriteFile(fakeTimeout, []byte("#!/bin/sh\nexit 37\n"), 0o700); err != nil {
		t.Fatalf("write fake timeout: %v", err)
	}
	repository, err := filepath.Abs(repositoryRoot)
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	command := exec.Command(filepath.Join(repository, "scripts/check-fieldalignment.sh"))
	command.Dir = repository
	command.Env = append(os.Environ(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	err = command.Run()
	exitError, ok := err.(*exec.ExitError)
	if !ok || exitError.ExitCode() != 37 {
		t.Fatalf("field-alignment gate error = %v, want exit code 37", err)
	}
}
