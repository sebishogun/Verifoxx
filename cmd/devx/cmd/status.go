package cmd

import (
	"fmt"
	"path/filepath"
)

func writeStatus(deps dependencies) error {
	if deps.stdout == nil {
		return errWorkflowUnavailable
	}
	repository, err := dependencyRepositoryRoot(deps)
	if err != nil {
		return err
	}
	for _, definition := range commandDefinitions {
		reason := workflowBlocker(definition.name, deps, repository)
		if reason == "" {
			if _, err := fmt.Fprintf(deps.stdout, "%s\trunnable\n", definition.name); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprintf(deps.stdout, "%s\tblocked: %s\n", definition.name, reason); err != nil {
			return err
		}
	}
	return nil
}

func workflowBlocker(name string, deps dependencies, repository string) string {
	if reason := workflowStaticBlocker(name); reason != "" {
		return reason
	}
	if name == "install" && deps.lookPath != nil {
		_, action, ok := packageManagerAction(deps.lookPath)
		if !ok {
			return "no supported package manager found"
		}
		if action.elevated {
			if _, err := deps.lookPath("sudo"); err != nil {
				return "missing executable sudo"
			}
		}
	}
	if deps.lookPath != nil {
		for _, executable := range workflowExecutables(name) {
			if _, err := deps.lookPath(executable); err != nil {
				return "missing executable " + executable
			}
		}
	}
	if deps.stat != nil {
		for _, relative := range workflowRepositoryAssets(name) {
			if _, err := deps.stat(filepath.Join(repository, relative)); err != nil {
				return "missing repository asset " + relative
			}
		}
	}
	return ""
}

func workflowStaticBlocker(name string) string {
	switch name {
	case "build:exp":
		return "pinned SIMD dependency is incompatible with the Go 1.27 experiment API"
	case "tui", "debug:tui":
		return "tui product command is unavailable"
	case "serve":
		return "serve product command is unavailable"
	case "load":
		return "load target requires the unavailable serve product command"
	case "test:e2e":
		return "e2e test suite is unavailable"
	default:
		return ""
	}
}

func workflowExecutables(name string) []string {
	switch name {
	case "install", "uninstall", "doctor", "status", "completion", "clean", "migrate:create":
		return nil
	case "full", "db:up", "db:down", "db:reset", "db:status", "docker:build", "docker:run", "docker:full":
		return []string{"docker"}
	case "migrate", "migrate:check", "graph:check", "test:integration", "test:e2e":
		return []string{"go", "docker"}
	case "proto:gen":
		return []string{"buf"}
	case "proto:check":
		return []string{"buf", "docker"}
	case "bench:compare":
		return []string{"benchstat"}
	case "perf":
		return []string{"perf"}
	case "load":
		return []string{"ghz"}
	case "debug", "debug:dap":
		return []string{"go", "dlv"}
	default:
		return []string{"go"}
	}
}

func workflowRepositoryAssets(name string) []string {
	switch name {
	case "uninstall":
		return []string{"cli/install.sh"}
	case "full", "docker:build", "docker:run", "docker:full":
		return []string{"Dockerfile", "compose.yaml"}
	case "db:up", "db:down", "db:reset", "db:status":
		return []string{"compose.yaml"}
	case "migrate", "migrate:create", "migrate:check", "graph:check":
		return []string{"migrations"}
	case "proto:gen", "proto:check":
		return []string{"buf.yaml", "buf.gen.yaml", "api/proto/verifoxx/v1/verifoxx.proto"}
	case "bench:compare":
		return []string{"bench/baseline.txt", "bench/current.txt"}
	default:
		return nil
	}
}
