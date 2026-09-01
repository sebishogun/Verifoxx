package cmd

import (
	"errors"
	"io"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

func namedCommandPlan(name string) ([]commandSpec, bool) {
	if plan, ok := buildCommandPlan(name); ok {
		return plan, true
	}
	if plan, ok := databaseCommandPlan(name); ok {
		return plan, true
	}
	if plan, ok := testCommandPlan(name); ok {
		return plan, true
	}
	if plan, ok := performanceCommandPlan(name); ok {
		return plan, true
	}
	if plan, ok := debugCommandPlan(name); ok {
		return plan, true
	}
	return containerCommandPlan(name)
}

func buildCommandPlan(name string) ([]commandSpec, bool) {
	switch name {
	case "build":
		return []commandSpec{{
			executable: "go",
			arguments:  []string{"build", "-trimpath", "-o", "bin/nornrune", "./cmd/nornrune"},
			timeout:    2 * time.Minute,
		}}, true
	case "build:exp":
		return []commandSpec{{
			executable:  "go",
			arguments:   []string{"build", "-trimpath", "-o", "bin/nornrune-exp", "./cmd/nornrune"},
			environment: []string{"GOEXPERIMENT=simd"},
			timeout:     2 * time.Minute,
		}}, true
	case "build:purego":
		return []commandSpec{{
			executable: "go",
			arguments:  []string{"build", "-trimpath", "-tags=purego", "-o", "bin/nornrune-purego", "./cmd/nornrune"},
			timeout:    2 * time.Minute,
		}}, true
	case "demo":
		return []commandSpec{{executable: "go", arguments: []string{"run", "./cmd/nornrune", "demo"}, timeout: 2 * time.Minute}}, true
	case "tui":
		return []commandSpec{{executable: "go", arguments: []string{"run", "./cmd/nornrune", "tui"}, timeout: 30 * time.Minute}}, true
	case "serve":
		return []commandSpec{{executable: "go", arguments: []string{"run", "./cmd/nornrune", "serve"}, timeout: 30 * time.Minute}}, true
	case "full":
		return []commandSpec{{
			executable: "docker", arguments: []string{"compose", "--profile", "full", "up", "--build", "--wait"}, timeout: 10 * time.Minute,
		}}, true
	case "proto:gen":
		return []commandSpec{
			{executable: "go", arguments: []string{"build", "-trimpath", "-o", ".nornrune/tools/protoc-gen-nornrune", "./cmd/protoc-gen-nornrune"}, timeout: 2 * time.Minute},
			{executable: "buf", arguments: []string{"generate"}, timeout: 2 * time.Minute},
			{executable: "buf", arguments: []string{"generate", "--template", "buf.frontend.gen.yaml"}, timeout: 2 * time.Minute},
		}, true
	case "proto:check":
		return []commandSpec{
			{executable: "buf", arguments: []string{"lint"}, timeout: 2 * time.Minute},
			{
				executable:  "go",
				arguments:   []string{"test", "-count=1", "-timeout", "150s", "-run", "^(TestGeneratedCodeIsCurrent|TestBufImageIsPinned)$", "./internal/adapters/grpcapi"},
				environment: []string{"NORNRUNE_CHECK_GENERATED=1"},
				timeout:     3 * time.Minute,
			},
		}, true
	case "policy:compile":
		return []commandSpec{{executable: "go", arguments: []string{"run", "./cmd/nornrune", "compile"}, timeout: 2 * time.Minute}}, true
	case "policy:check":
		return []commandSpec{{executable: "go", arguments: []string{"run", "./cmd/nornrune", "validate"}, timeout: 2 * time.Minute}}, true
	case "results:gen":
		return []commandSpec{{executable: "go", arguments: []string{"run", "./cmd/nornrune", "evaluate"}, timeout: 2 * time.Minute}}, true
	case "results:check":
		return []commandSpec{{
			executable: "go", arguments: []string{"test", "-count=1", "-timeout", "60s", "./internal/conformance"}, timeout: 90 * time.Second,
		}}, true
	case "wasm:check":
		return []commandSpec{{
			executable: "sh", arguments: []string{"scripts/check-wasm.sh"}, timeout: 5 * time.Minute,
		}}, true
	default:
		return nil, false
	}
}

func runClean(deps dependencies) error {
	if deps.removeAll == nil {
		return errWorkflowUnavailable
	}
	repository, err := dependencyRepositoryRoot(deps)
	if err != nil {
		return err
	}
	for _, relative := range []string{"bin", ".nornrune", "cpu.pprof"} {
		if err := deps.removeAll(filepath.Join(repository, relative)); err != nil {
			return errors.Join(errors.New("devx: clean generated path"), err)
		}
	}
	return nil
}

func writeCompletion(root *cobra.Command, shell string, output io.Writer) error {
	if root == nil || output == nil {
		return errWorkflowUnavailable
	}
	switch shell {
	case "bash":
		return root.GenBashCompletion(output)
	case "zsh":
		return root.GenZshCompletion(output)
	case "fish":
		return root.GenFishCompletion(output, true)
	case "powershell":
		return root.GenPowerShellCompletion(output)
	default:
		return errors.New("devx: unsupported completion shell")
	}
}
