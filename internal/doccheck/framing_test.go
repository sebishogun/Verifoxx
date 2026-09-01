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
// Historical records keep their original wording: the archived source
// material, the dated development plans, and the verbatim license text. The
// scanner's own file is exempt because it necessarily contains the banned
// literals it enforces.
var framingAllowedPaths = map[string]bool{
	"LICENSE":                           true,
	"internal/doccheck/framing_test.go": true,
}

var framingAllowedPrefixes = [...]string{
	"docs/archive/source-material/",
	"docs/plans/",
}

// framingBannedWords are rejected wherever they appear in tracked product
// files, in any case or inflection. "exercis" is a stem and also matches
// "exercises", "exercised", and "exercising".
var framingBannedWords = [...]string{
	"exercis",
	"assignment",
	"take-home",
	"takehome",
	"grader",
	"recruiter",
	"interview",
}

// framingBannedPhrases are job-submission framing that must not reappear.
var framingBannedPhrases = [...]string{
	"candidate-exercise",
	"candidate submission",
	"candidate-facing",
	"evaluator-facing",
	"supplied request",
	"supplied pack",
	"supplied policy",
	"supplied input",
	"five supplied",
	"design note",
}

// framingSubmissionAllowedPrefixes keep "audit submission", "journal
// submission", and "batch submission" usable as pipeline terminology; every
// other "submission" is candidate-submission framing.
var framingSubmissionAllowedPrefixes = [...]string{"audit ", "journal ", "batch "}

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
		lower := strings.ToLower(string(content))
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
		diagnostics += framingCheckSubmissions(t, relative, lower, maxDiagnostics-diagnostics)
	}
	if diagnostics >= maxDiagnostics {
		t.Errorf("product framing diagnostics truncated at %d", maxDiagnostics)
	}
}

func framingCheckSubmissions(t *testing.T, relative, lower string, budget int) int {
	t.Helper()
	found := 0
	search := lower
	for {
		index := strings.Index(search, "submission")
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
		for _, prefix := range framingSubmissionAllowedPrefixes {
			if strings.HasSuffix(preceding, strings.TrimSpace(prefix)) {
				allowed = true
				break
			}
		}
		if !allowed {
			t.Errorf("product framing %q in %s", "submission ("+preceding+")", relative)
			found++
		}
		search = search[index+len("submission"):]
	}
}

func framingAllowed(relative string) bool {
	if framingAllowedPaths[relative] {
		return true
	}
	for _, prefix := range framingAllowedPrefixes {
		if strings.HasPrefix(relative, prefix) {
			return true
		}
	}
	return false
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
	for _, path := range []string{
		filepath.Join(archive, "NornRune_AI_Engineer_Assignment.pdf"),
		filepath.Join(archive, "Requirements.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("archived source material missing: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(repositoryRoot, "Requirements.md")); !os.IsNotExist(err) {
		t.Error("Requirements.md still exists at the repository root")
	}
	if _, err := os.Stat(filepath.Join(repositoryRoot, "NornRune_AI_Engineer_Assignment.pdf")); !os.IsNotExist(err) {
		t.Error("assignment PDF still exists at the repository root")
	}
}
