package doccheck_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const repositoryRoot = "../.."

func TestSubmissionDocumentsExist(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"README.md",
		"docs/design-note.md",
		"docs/ai-usage.md",
	} {
		if _, err := os.Stat(filepath.Join(repositoryRoot, path)); err != nil {
			t.Errorf("required submission document %q: %v", path, err)
		}
	}
}

func TestReadmeDocumentsRunnableModesAndDataContract(t *testing.T) {
	t.Parallel()

	readme := readDocument(t, "README.md")
	for _, required := range []string{
		"go run ./cmd/nornrune demo",
		"go run ./cmd/nornrune tui",
		"docker build -t nornrune:local .",
		"docker run --rm nornrune:local demo",
		"docker compose --profile full up --build --wait",
		"--policy",
		"--requests",
		"--evidence",
		"request_id",
		"decision",
		"rationale",
		"requirements_applied",
		"evidence_used",
		"missing_or_conflicting_evidence",
		"assumptions",
		"unresolved_uncertainty",
	} {
		if !strings.Contains(readme, required) {
			t.Errorf("README.md does not document %q", required)
		}
	}
}

func TestDesignNoteFitsOnePageAndCoversRequiredTopics(t *testing.T) {
	t.Parallel()

	note := readDocument(t, "docs/design-note.md")
	if words := len(strings.Fields(note)); words > 700 {
		t.Errorf("docs/design-note.md has %d words; maximum is 700", words)
	}
	lower := strings.ToLower(note)
	for _, required := range []string{
		"semantic representation",
		"flat extraction",
		"approve",
		"reject",
		"revise",
		"escalate",
		"missing",
		"stale",
		"conflicting",
		"next improvements",
	} {
		if !strings.Contains(lower, required) {
			t.Errorf("docs/design-note.md does not cover %q", required)
		}
	}
}

func TestAIUsageDisclosureStatesWhereToolsAssisted(t *testing.T) {
	t.Parallel()

	disclosure := strings.ToLower(readDocument(t, "docs/ai-usage.md"))
	for _, required := range []string{"ai", "used", "implementation", "tests", "documentation", "review"} {
		if !strings.Contains(disclosure, required) {
			t.Errorf("docs/ai-usage.md does not state %q", required)
		}
	}
}

func readDocument(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(filepath.Join(repositoryRoot, path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}
