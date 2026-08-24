package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrPackageManager = errors.New("devx: no supported package manager found")

type installOptions struct {
	dryRun bool
	yes    bool
}

type installAction struct {
	spec     commandSpec
	elevated bool
}

func runInstall(ctx context.Context, deps dependencies, options installOptions) error {
	if ctx == nil || deps.lookPath == nil || deps.stdout == nil {
		return errWorkflowUnavailable
	}
	manager, actions, err := buildInstallPlan(deps.lookPath)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(deps.stdout, "Package manager: %s\n", manager); err != nil {
		return err
	}
	for _, action := range actions {
		marker := "[user]"
		if action.elevated {
			marker = "[elevated]"
		}
		if _, err := fmt.Fprintf(deps.stdout, "%s %s %s\n", marker, action.spec.executable, strings.Join(action.spec.arguments, " ")); err != nil {
			return err
		}
	}
	if options.dryRun {
		return nil
	}
	if !options.yes {
		if deps.confirm == nil {
			return errWorkflowUnavailable
		}
		confirmed, err := deps.confirm.Confirm(ctx, "Execute this install plan?", deps.stdin, deps.stdout)
		if err != nil {
			return err
		}
		if !confirmed {
			return nil
		}
	}
	if deps.getwd == nil || deps.readFile == nil || deps.runner == nil {
		return errWorkflowUnavailable
	}
	workingDirectory, err := deps.getwd()
	if err != nil {
		return errors.Join(ErrRepositoryRoot, err)
	}
	repository, err := findRepositoryRoot(workingDirectory, deps.readFile)
	if err != nil {
		return err
	}
	for _, action := range actions {
		commandCtx, cancel := context.WithTimeout(ctx, action.spec.timeout)
		err = deps.runner.Run(commandCtx, repository, action.spec, deps.stdin, deps.stdout, deps.stderr)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func buildInstallPlan(lookPath func(string) (string, error)) (string, []installAction, error) {
	manager, packageAction, ok := packageManagerAction(lookPath)
	if !ok {
		return "", nil, ErrPackageManager
	}
	actions := make([]installAction, 1, 5)
	actions[0] = packageAction
	for _, target := range []string{
		"github.com/go-delve/delve/cmd/dlv@v1.27.1",
		"github.com/bufbuild/buf/cmd/buf@v1.72.0",
		"github.com/bojand/ghz/cmd/ghz@v0.121.0",
		"golang.org/x/perf/cmd/benchstat@v0.0.0-20260819171926-ebcb4798430d",
	} {
		actions = append(actions, installAction{spec: commandSpec{
			executable: "go", arguments: []string{"install", target}, timeout: 15 * time.Minute,
		}})
	}
	return manager, actions, nil
}

func packageManagerAction(lookPath func(string) (string, error)) (string, installAction, bool) {
	if _, err := lookPath("apt-get"); err == nil {
		return "apt-get", installAction{elevated: true, spec: commandSpec{
			executable: "sudo",
			arguments:  []string{"apt-get", "install", "-y", "golang-go", "docker.io", "docker-compose-plugin", "protobuf-compiler", "postgresql-client"},
			timeout:    15 * time.Minute,
		}}, true
	}
	if _, err := lookPath("dnf"); err == nil {
		return "dnf", installAction{elevated: true, spec: commandSpec{
			executable: "sudo",
			arguments:  []string{"dnf", "install", "-y", "golang", "docker", "docker-compose-plugin", "protobuf-compiler", "postgresql"},
			timeout:    15 * time.Minute,
		}}, true
	}
	if _, err := lookPath("pacman"); err == nil {
		return "pacman", installAction{elevated: true, spec: commandSpec{
			executable: "sudo",
			arguments:  []string{"pacman", "-S", "--needed", "go", "docker", "docker-compose", "protobuf", "postgresql-libs"},
			timeout:    15 * time.Minute,
		}}, true
	}
	if _, err := lookPath("brew"); err == nil {
		return "brew", installAction{spec: commandSpec{
			executable: "brew",
			arguments:  []string{"install", "go", "docker", "docker-compose", "protobuf", "libpq"},
			timeout:    15 * time.Minute,
		}}, true
	}
	return "", installAction{}, false
}
