package cmd

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakefileTargetsDelegateOneToOneToDevx(t *testing.T) {
	t.Parallel()

	contents, err := os.ReadFile(filepath.Join(installTestRepository(t), "Makefile"))
	if err != nil {
		t.Fatalf("ReadFile(Makefile) error = %v", err)
	}
	want := map[string]string{
		"menu":             "@$(DEVX)",
		"help":             "@$(DEVX) --help",
		"install":          "@$(DEVX) install",
		"uninstall":        "@$(DEVX) uninstall",
		"doctor":           "@$(DEVX) doctor",
		"status":           "@$(DEVX) status",
		"completion":       "@$(DEVX) completion",
		"build":            "@$(DEVX) build",
		"build-exp":        "@$(DEVX) build:exp",
		"build-purego":     "@$(DEVX) build:purego",
		"clean":            "@$(DEVX) clean",
		"demo":             "@$(DEVX) demo",
		"tui":              "@$(DEVX) tui",
		"serve":            "@$(DEVX) serve",
		"full":             "@$(DEVX) full",
		"db-up":            "@$(DEVX) db:up",
		"db-down":          "@$(DEVX) db:down",
		"db-reset":         "@$(DEVX) db:reset",
		"db-status":        "@$(DEVX) db:status",
		"migrate":          "@$(DEVX) migrate",
		"migrate-create":   "@$(DEVX) migrate:create --name \"$(NAME)\"",
		"migrate-check":    "@$(DEVX) migrate:check",
		"graph-check":      "@$(DEVX) graph:check",
		"proto-gen":        "@$(DEVX) proto:gen",
		"proto-check":      "@$(DEVX) proto:check",
		"policy-compile":   "@$(DEVX) policy:compile",
		"policy-check":     "@$(DEVX) policy:check",
		"results-gen":      "@$(DEVX) results:gen",
		"results-check":    "@$(DEVX) results:check",
		"test":             "@$(DEVX) test",
		"test-unit":        "@$(DEVX) test:unit",
		"test-integration": "@$(DEVX) test:integration",
		"test-e2e":         "@$(DEVX) test:e2e",
		"race":             "@$(DEVX) test:race",
		"fuzz":             "@$(DEVX) fuzz",
		"bench":            "@$(DEVX) bench",
		"bench-compare":    "@$(DEVX) bench:compare",
		"profile":          "@$(DEVX) profile",
		"perf":             "@$(DEVX) perf",
		"load":             "@$(DEVX) load",
		"debug":            "@$(DEVX) debug",
		"debug-dap":        "@$(DEVX) debug:dap",
		"debug-tui":        "@$(DEVX) debug:tui",
		"docker-build":     "@$(DEVX) docker:build",
		"docker-run":       "@$(DEVX) docker:run",
		"docker-full":      "@$(DEVX) docker:full",
	}
	got := parseMakefileRecipes(t, contents)
	if len(got) != len(want) {
		t.Fatalf("public Make targets = %d, want %d: %v", len(got), len(want), got)
	}
	for target, recipe := range want {
		if recipes := got[target]; len(recipes) != 1 || recipes[0] != recipe {
			t.Errorf("target %s recipes = %q, want [%q]", target, recipes, recipe)
		}
	}
	if !bytes.Contains(contents, []byte(".DEFAULT_GOAL := menu\n")) {
		t.Error("Makefile default goal is not menu")
	}
}

func parseMakefileRecipes(t *testing.T, contents []byte) map[string][]string {
	t.Helper()
	recipes := make(map[string][]string)
	current := ""
	scanner := bufio.NewScanner(bytes.NewReader(contents))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "\t") {
			if current != "" {
				recipes[current] = append(recipes[current], strings.TrimPrefix(line, "\t"))
			}
			continue
		}
		current = ""
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ".") || strings.Contains(line, ":=") || strings.Contains(line, "?=") {
			continue
		}
		separator := strings.IndexByte(line, ':')
		if separator <= 0 {
			continue
		}
		current = strings.TrimSpace(line[:separator])
		if strings.Contains(current, " ") {
			t.Fatalf("Makefile target declaration contains multiple targets: %q", line)
		}
		recipes[current] = nil
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan Makefile: %v", err)
	}
	return recipes
}
