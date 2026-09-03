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

// Product framing must describe NornRune as a maintained policy engine.
// Only the immutable source-material archive keeps its original wording.
var framingAllowedPrefixes = [...]string{
	"docs/archive/source-material/",
}

// The split literals let this checker inspect its own source.
var framingBannedWords = [...]string{
	"exer" + "cis",
	"assign" + "ment",
	"take" + "-home",
	"take" + "home",
	"grad" + "er",
	"recruit" + "er",
	"inter" + "view",
}

var framingBannedPhrases = [...]string{
	"candidate-" + "exer" + "cise",
	"candidate " + "sub" + "mission",
	"candidate-" + "facing",
	"evaluator-" + "facing",
	"supplied " + "request",
	"supplied " + "pack",
	"supplied " + "policy",
	"supplied " + "input",
	"five " + "supplied",
	"design " + "note",
}

const framingIntakeTerm = "sub" + "mission"

var framingIntakeAllowedPrefixes = [...]string{"audit ", "journal ", "batch "}

func TestTrackedFilesContainNoProductFraming(t *testing.T) {
	command := exec.Command("git", "ls-files", "-z")
	command.Dir = repositoryRoot
	tracked, err := command.Output()
	if err != nil {
		t.Fatalf("list tracked files: %v", err)
	}
	const maxDiagnostics = 128
	diagnostics := 0
	for _, name := range bytes.Split(tracked, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		relative := filepath.ToSlash(string(name))
		if framingPathViolation(relative) && diagnostics < maxDiagnostics {
			t.Errorf("product framing in tracked path %s", relative)
			diagnostics++
		}
		if framingAllowed(relative) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read tracked file %s: %v", relative, err)
		}
		if !utf8.Valid(content) {
			continue
		}
		lower := framingNormalizedContent(relative, strings.ToLower(string(content)))
		for _, word := range framingBannedWords {
			if strings.Contains(lower, word) && diagnostics < maxDiagnostics {
				t.Errorf("product framing %q in %s", word, relative)
				diagnostics++
			}
		}
		for _, phrase := range framingBannedPhrases {
			if strings.Contains(lower, phrase) && diagnostics < maxDiagnostics {
				t.Errorf("product framing %q in %s", phrase, relative)
				diagnostics++
			}
		}
		diagnostics += framingCheckIntakeTerm(t, relative, lower, maxDiagnostics-diagnostics)
	}
	if diagnostics >= maxDiagnostics {
		t.Errorf("product framing diagnostics truncated at %d", maxDiagnostics)
	}
}

func TestProductFramingPathClassification(t *testing.T) {
	legacy := "internal/doccheck/" + "sub" + "mission_test.go"
	if !framingPathViolation(legacy) {
		t.Fatalf("legacy product path accepted: %s", legacy)
	}
	archived := "docs/archive/source-material/NornRune_AI_Engineer_" + "Assign" + "ment.pdf"
	if framingPathViolation(archived) {
		t.Fatalf("historical archive path rejected: %s", archived)
	}
}

func TestProductFramingAllowlistIsArchiveOnly(t *testing.T) {
	for _, path := range []string{
		"LICENSE",
		"docs/plans/2026-08-20-nornrune-policy-engine.md",
		"internal/doccheck/framing_test.go",
	} {
		if framingAllowed(path) {
			t.Errorf("active path exempt from product framing checks: %s", path)
		}
	}
	if path := "docs/archive/source-material/README.md"; !framingAllowed(path) {
		t.Errorf("historical archive path is not exempt: %s", path)
	}
}

func framingCheckIntakeTerm(t *testing.T, relative, lower string, budget int) int {
	t.Helper()
	found := 0
	search := lower
	for {
		index := strings.Index(search, framingIntakeTerm)
		if index < 0 || found >= budget {
			return found
		}
		tailStart := index - 48
		if tailStart < 0 {
			tailStart = 0
		}
		words := strings.Fields(search[tailStart:index])
		preceding := ""
		if len(words) > 0 {
			preceding = words[len(words)-1]
		}
		allowed := false
		for _, prefix := range framingIntakeAllowedPrefixes {
			if strings.HasSuffix(preceding, strings.TrimSpace(prefix)) {
				allowed = true
				break
			}
		}
		if !allowed {
			t.Errorf("product framing %q in %s", framingIntakeTerm+" ("+preceding+")", relative)
			found++
		}
		search = search[index+len(framingIntakeTerm):]
	}
}

func framingAllowed(relative string) bool {
	for _, prefix := range framingAllowedPrefixes {
		if strings.HasPrefix(relative, prefix) {
			return true
		}
	}
	return false
}

func framingPathViolation(relative string) bool {
	if framingAllowed(relative) {
		return false
	}
	lower := strings.ToLower(relative)
	for _, word := range framingBannedWords {
		if strings.Contains(lower, word) {
			return true
		}
	}
	for _, phrase := range framingBannedPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return strings.Contains(lower, framingIntakeTerm)
}

func framingNormalizedContent(relative, lower string) string {
	if relative != "LICENSE" {
		return lower
	}
	// Preserve verbatim legal boilerplate while allowing its necessary terms.
	lower = strings.ReplaceAll(lower, "exer"+"cising permissions", "using permissions")
	lower = strings.ReplaceAll(lower, "exer"+"cise of permissions", "use of permissions")
	return strings.ReplaceAll(lower, "sub"+"mission of contributions", "contribution intake")
}

func TestArchiveHoldsOnlyHistoricalSourceMaterial(t *testing.T) {
	archive := filepath.Join(repositoryRoot, "docs", "archive", "source-material")
	notice := filepath.Join(archive, "README.md")
	content, err := os.ReadFile(notice)
	if err != nil {
		t.Fatalf("archive notice: %v", err)
	}
	lower := strings.ToLower(string(content))
	for _, required := range []string{"historical", "provenance", "not current product requirements"} {
		if !strings.Contains(lower, required) {
			t.Errorf("archive notice does not state %q", required)
		}
	}
	sourcePDF := "NornRune_AI_Engineer_" + "Assign" + "ment.pdf"
	for _, path := range []string{
		filepath.Join(archive, sourcePDF),
		filepath.Join(archive, "Requirements.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("archived source material missing: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(repositoryRoot, "Requirements.md")); !os.IsNotExist(err) {
		t.Error("Requirements.md still exists at the repository root")
	}
	if _, err := os.Stat(filepath.Join(repositoryRoot, sourcePDF)); !os.IsNotExist(err) {
		t.Error("source PDF still exists at the repository root")
	}
}
