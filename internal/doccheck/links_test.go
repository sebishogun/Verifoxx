package doccheck_test

import (
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var markdownLinkPattern = regexp.MustCompile(`!?\[[^]]*\]\(([^)[:space:]]+)(?:[[:space:]]+"[^"]*")?\)`)

func TestTechnicalDocumentationFilesAndLocalLinks(t *testing.T) {
	t.Parallel()

	paths := []string{
		"README.md",
		"docs/architecture.md",
		"docs/policy-language.md",
		"docs/concurrency.md",
		"docs/database.md",
		"docs/api.md",
		"docs/development.md",
		"docs/operations.md",
		"docs/debugging.md",
		"docs/performance.md",
		"docs/policy-diff.md",
		"docs/wasm.md",
		"docs/telemetry.md",
	}
	for _, path := range paths {
		content := readDocument(t, path)
		for _, match := range markdownLinkPattern.FindAllStringSubmatch(content, -1) {
			target := strings.Trim(match[1], "<>")
			if target == "" || strings.HasPrefix(target, "#") || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if fragment := strings.IndexByte(target, '#'); fragment >= 0 {
				target = target[:fragment]
			}
			decoded, err := url.PathUnescape(target)
			if err != nil {
				t.Errorf("%s has invalid local link %q: %v", path, target, err)
				continue
			}
			resolved := filepath.Join(repositoryRoot, filepath.Dir(path), filepath.FromSlash(decoded))
			if _, err := os.Stat(resolved); err != nil {
				t.Errorf("%s local link %q: %v", path, target, err)
			}
		}
	}
}

func TestTechnicalDocumentationReferencesExecutableCommands(t *testing.T) {
	t.Parallel()

	commandsByDocument := map[string][]string{
		"docs/policy-language.md": {"policy:compile", "policy:check"},
		"docs/concurrency.md":     {"test:race"},
		"docs/database.md":        {"db:up", "migrate", "graph:check"},
		"docs/api.md":             {"serve"},
		"docs/development.md":     {"doctor", "build", "test"},
		"docs/operations.md":      {"status", "full"},
		"docs/debugging.md":       {"debug:dap", "debug:tui"},
		"docs/performance.md":     {"bench", "bench:compare", "perf"},
	}
	registry := readDocument(t, "cmd/devx/cmd/root.go")
	for path, commands := range commandsByDocument {
		content := readDocument(t, path)
		for _, command := range commands {
			if !strings.Contains(registry, `{name: "`+command+`"`) {
				t.Fatalf("test contract references unregistered devx command %q", command)
			}
			if !strings.Contains(content, "./cli/devx "+command) && !strings.Contains(content, "go run ./cmd/devx "+command) {
				t.Errorf("%s does not reference executable devx command %q", path, command)
			}
		}
	}
}

func TestTechnicalDocumentationCoversOperationalRequirements(t *testing.T) {
	t.Parallel()

	required := map[string][]string{
		"docs/architecture.md": {"Ownership", "Data layout"},
		"docs/concurrency.md":  {"Lock table"},
		"docs/database.md":     {"PostgreSQL 19", "beta", "Migrations", "Recovery"},
		"docs/api.md":          {"Examples"},
		"docs/debugging.md":    {"Neovim", "DAP"},
		"docs/performance.md":  {"Methodology"},
	}
	for path, topics := range required {
		content := readDocument(t, path)
		for _, topic := range topics {
			if !strings.Contains(strings.ToLower(content), strings.ToLower(topic)) {
				t.Errorf("%s does not cover %q", path, topic)
			}
		}
	}
}
