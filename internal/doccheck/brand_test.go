package doccheck_test

import (
	"bytes"
	"io/fs"
	"os"
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

	err := filepath.WalkDir(repositoryRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(repositoryRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == ".git" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if allowed[relative] {
			return nil
		}
		for _, value := range legacy {
			if strings.Contains(relative, value) && diagnostics < maxDiagnostics {
				t.Errorf("legacy brand %q in path %s", value, relative)
				diagnostics++
				break
			}
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !utf8.Valid(content) {
			return nil
		}
		for _, value := range legacy {
			if bytes.Contains(content, []byte(value)) && diagnostics < maxDiagnostics {
				t.Errorf("legacy brand %q in %s", value, relative)
				diagnostics++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics == maxDiagnostics {
		t.Errorf("legacy brand diagnostics truncated at %d", maxDiagnostics)
	}
}
