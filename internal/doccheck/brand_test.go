package doccheck_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

var canonicalBrandFiles = map[string][]string{
	"go.mod":                                {"module github.com/sebishogun/nornrune"},
	"README.md":                             {"# NornRune", "Why NornRune"},
	".goreleaser.yaml":                      {"project_name: nornrune", "binary: nornrune"},
	"compose.yaml":                          {"name: nornrune", "/nornrune"},
	"api/proto/nornrune/v1/nornrune.proto":  {"package nornrune.v1;"},
	"frontend/proto/options.proto":          {"package nornrune.frontend;"},
	"testdata/frontends/proto/policy.proto": {"package nornrune.frontend.fixture;"},
	"docs/plans/2026-08-20-nornrune-policy-engine.md": {"# NornRune"},
}

func TestCanonicalBrandSurface(t *testing.T) {
	for path, required := range canonicalBrandFiles {
		content, err := os.ReadFile(filepath.Join(repositoryRoot, path))
		if err != nil {
			t.Errorf("canonical path %q: %v", path, err)
			continue
		}
		for _, value := range required {
			if !bytes.Contains(content, []byte(value)) {
				t.Errorf("%s does not contain %q", path, value)
			}
		}
	}

	legacyLower := "veri" + "foxx"
	for _, path := range []string{
		"cmd/" + legacyLower, "cmd/protoc-gen-" + legacyLower, "policies/" + legacyLower,
		"api/proto/" + legacyLower, "api/gen/" + legacyLower,
		"internal/fixtures/" + legacyLower + "-policy.json", "internal/fixtures/" + legacyLower + "-requests.json",
		"internal/fixtures/" + legacyLower + "-evidence.json",
	} {
		if _, err := os.Stat(filepath.Join(repositoryRoot, path)); !os.IsNotExist(err) {
			t.Errorf("legacy path %q still exists", path)
		}
	}
}

func TestTrackedTextContainsNoLegacyBrand(t *testing.T) {
	allowed := map[string]bool{
		"docs/plans/2026-08-27-nornrune-complete-rename-design.md": true,
		"docs/plans/2026-08-27-nornrune-complete-rename.md":        true,
	}
	legacy := [...]string{"Veri" + "foxx", "veri" + "foxx", "VERI" + "FOXX"}
	const maxDiagnostics = 128
	diagnostics := 0

	command := exec.Command("git", "ls-files", "-z")
	command.Dir = repositoryRoot
	tracked, err := command.Output()
	if err != nil {
		t.Fatalf("list tracked files: %v", err)
	}
	for _, name := range bytes.Split(tracked, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		relative := filepath.ToSlash(string(name))
		if allowed[relative] {
			continue
		}
		for _, value := range legacy {
			if strings.Contains(relative, value) && diagnostics < maxDiagnostics {
				t.Errorf("legacy brand %q in path %s", value, relative)
				diagnostics++
				break
			}
		}
		content, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read tracked file %s: %v", relative, err)
		}
		if !utf8.Valid(content) {
			continue
		}
		for _, value := range legacy {
			if bytes.Contains(content, []byte(value)) && diagnostics < maxDiagnostics {
				t.Errorf("legacy brand %q in %s", value, relative)
				diagnostics++
			}
		}
	}
	if diagnostics == maxDiagnostics {
		t.Errorf("legacy brand diagnostics truncated at %d", maxDiagnostics)
	}
}
