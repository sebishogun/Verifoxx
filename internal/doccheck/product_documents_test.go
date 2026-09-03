package doccheck_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const repositoryRoot = "../.."

func TestProductDocumentsExist(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"README.md",
		"docs/semantic-model.md",
		"docs/ai-usage.md",
		"docs/archive/source-material/README.md",
	} {
		if _, err := os.Stat(filepath.Join(repositoryRoot, path)); err != nil {
			t.Errorf("required product document %q: %v", path, err)
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

func TestSemanticModelSummaryFitsOnePageAndCoversCoreTopics(t *testing.T) {
	t.Parallel()

	summary := readDocument(t, "docs/semantic-model.md")
	if words := len(strings.Fields(summary)); words > 700 {
		t.Errorf("docs/semantic-model.md has %d words; maximum is 700", words)
	}
	lower := strings.ToLower(summary)
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
			t.Errorf("docs/semantic-model.md does not cover %q", required)
		}
	}
}

func TestDevelopmentDisclosureStatesWhereToolsAssisted(t *testing.T) {
	t.Parallel()

	disclosure := strings.ToLower(readDocument(t, "docs/ai-usage.md"))
	for _, required := range []string{"ai", "used", "implementation", "tests", "documentation", "review"} {
		if !strings.Contains(disclosure, required) {
			t.Errorf("docs/ai-usage.md does not state %q", required)
		}
	}
}

func TestReviewerAgentUsesRepositoryRulesAndDefaultDenyPermissions(t *testing.T) {
	reviewer := readDocument(t, ".opencode/agents/reviewer.md")
	for _, required := range []string{
		"Read and follow the repository's AGENTS.md",
		`"*": deny`,
		"edit: deny",
		"task: deny",
		"external_directory: deny",
		"bash:\n    \"*\": deny",
	} {
		if !strings.Contains(reviewer, required) {
			t.Errorf("reviewer agent does not contain %q", required)
		}
	}
	if strings.Contains(reviewer, "repo's CLAUDE.md") {
		t.Error("reviewer agent references a repository rule file that does not exist")
	}
}

func TestReviewerReadPermissionsPreserveEnvironmentPrompts(t *testing.T) {
	reviewer := readDocument(t, ".opencode/agents/reviewer.md")
	want := "  read:\n" +
		"    \"*\": allow\n" +
		"    \"*.env\": ask\n" +
		"    \"*.env.*\": ask\n" +
		"    \"*.env.example\": allow"
	if !strings.Contains(reviewer, want) {
		t.Error("reviewer read permissions do not preserve ordered environment-file prompts")
	}
	if strings.Contains(reviewer, "  read: allow") {
		t.Error("reviewer scalar read permission overrides protected-file prompts")
	}
}

func TestReviewerBashPermissionsAllowOnlyExactVerificationCommands(t *testing.T) {
	reviewer := readDocument(t, ".opencode/agents/reviewer.md")
	want := map[string]bool{
		"git status --short --branch": false,
		"git diff":                    false,
		"git diff --cached":           false,
		"git diff --check":            false,
		"git diff --cached --check":   false,
		"git diff --no-ext-diff --no-textconv origin/main...HEAD --": false,
		"git log --oneline -10":                             false,
		"git log --oneline origin/main..HEAD":               false,
		"git ls-files":                                      false,
		"git rev-parse --show-toplevel":                     false,
		"timeout 300s go test -count=1 -timeout 240s ./...": false,
		"timeout 300s go test -count=1 -race -gcflags=all=-d=checkptr=2 -timeout 240s ./...": false,
		"timeout 300s go vet ./...":                      false,
		"timeout 300s go build ./...":                    false,
		"timeout 180s go build -gcflags=-m ./...":        false,
		"timeout 300s go mod tidy -diff":                 false,
		"timeout 300s ./scripts/check-fieldalignment.sh": false,
		"timeout 300s go run ./cmd/devx bench":           false,
		"timeout 180s go test -run NONE -bench BenchmarkCandidateBatch -benchmem -count=3 -timeout 150s ./internal/diff":                 false,
		"timeout 180s go test -run NONE -bench BenchmarkWASMWarmRuntimeEvaluate -benchmem -count=3 -timeout 150s ./internal/target/wasm": false,
		"timeout 300s go test -run NONE -bench BenchmarkTelemetry -benchmem -count=6 -timeout 270s ./telemetry":                          false,
	}
	inBash := false
	for _, line := range strings.Split(reviewer, "\n") {
		if line == "  bash:" {
			inBash = true
			continue
		}
		if !inBash {
			continue
		}
		if !strings.HasPrefix(line, "    ") {
			break
		}
		trimmed := strings.TrimSpace(line)
		if !strings.HasSuffix(trimmed, ": allow") || !strings.HasPrefix(trimmed, `"`) {
			continue
		}
		command := strings.TrimSuffix(strings.TrimPrefix(trimmed, `"`), `": allow`)
		if strings.ContainsAny(command, "*;&|><`$\\") {
			t.Errorf("reviewer Bash allow contains shell pattern or metacharacter: %q", command)
		}
		if _, ok := want[command]; !ok {
			t.Errorf("reviewer Bash allow is not an approved exact command: %q", command)
			continue
		}
		want[command] = true
	}
	for command, found := range want {
		if !found {
			t.Errorf("reviewer Bash permissions omit exact command %q", command)
		}
	}
}

func TestCompletedRoadmapTasksDeclareStatus(t *testing.T) {
	roadmap := readDocument(t, "docs/plans/2026-08-20-nornrune-policy-engine.md")
	for _, heading := range []string{
		"### Task 58: Draw Production Semantic Debugger Graphs",
		"### Task 59: Close Production Scheduler, Benchmark CLI, And Field-Alignment Gaps",
	} {
		if !strings.Contains(roadmap, heading+"\n\n**Status:** Complete") {
			t.Errorf("roadmap task lacks explicit completion status: %s", heading)
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
