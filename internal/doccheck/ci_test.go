package doccheck_test

import (
	"strings"
	"testing"
)

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
