// Package cmd implements the NornRune developer workflow CLI.
package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

var (
	ErrRepositoryRoot      = errors.New("devx: repository root not found")
	ErrDoctorFailed        = errors.New("devx: doctor found unavailable prerequisites")
	ErrWorkflowBlocked     = errors.New("devx: workflow blocked")
	errWorkflowUnavailable = errors.New("devx: workflow unavailable")
)

var (
	moduleKeyword   = []byte("module")
	moduleDirective = []byte("github.com/sebishogun/nornrune")
)

type commandSpec struct {
	executable              string
	arguments               []string
	environment             []string
	repositoryPathArguments []uint8
	timeout                 time.Duration
}

type commandRunner interface {
	Run(context.Context, string, commandSpec, io.Reader, io.Writer, io.Writer) error
}

type menuOption struct {
	name      string
	group     string
	reason    string
	available bool
}

type menuSelector interface {
	Select(context.Context, []menuOption, io.Reader, io.Writer) (string, error)
}

type confirmationPrompt interface {
	Confirm(context.Context, string, io.Reader, io.Writer) (bool, error)
}

type huhMenu struct{}

func (huhMenu) Select(ctx context.Context, options []menuOption, input io.Reader, output io.Writer) (string, error) {
	selectOptions := make([]huh.Option[string], 0, len(options))
	for _, option := range options {
		if option.available {
			label := fmt.Sprintf("%-11s %s", commandGroupTitle(option.group), option.name)
			selectOptions = append(selectOptions, huh.NewOption(label, option.name))
		}
	}
	if len(selectOptions) == 0 {
		return "", errWorkflowUnavailable
	}
	var selected string
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("NornRune workflows").
			Description("Press / to fuzzy-filter").
			Options(selectOptions...).
			Value(&selected),
	)).WithInput(input).WithOutput(output)
	if err := form.RunWithContext(ctx); err != nil {
		return "", err
	}
	return selected, nil
}

type huhConfirmation struct{}

func (huhConfirmation) Confirm(ctx context.Context, title string, input io.Reader, output io.Writer) (bool, error) {
	confirmed := false
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title(title).Affirmative("Install").Negative("Cancel").Value(&confirmed),
	)).WithInput(input).WithOutput(output)
	if err := form.RunWithContext(ctx); err != nil {
		return false, err
	}
	return confirmed, nil
}

type dependencies struct {
	stdin     io.Reader
	stdout    io.Writer
	stderr    io.Writer
	getwd     func() (string, error)
	readFile  func(string) ([]byte, error)
	lookPath  func(string) (string, error)
	stat      func(string) (os.FileInfo, error)
	removeAll func(string) error
	runner    commandRunner
	menu      menuSelector
	confirm   confirmationPrompt
}

type commandDefinition struct {
	name  string
	group string
}

var commandDefinitions = [...]commandDefinition{
	{name: "install", group: "setup"},
	{name: "uninstall", group: "setup"},
	{name: "doctor", group: "setup"},
	{name: "status", group: "setup"},
	{name: "completion", group: "setup"},
	{name: "build", group: "build"},
	{name: "build:exp", group: "build"},
	{name: "build:purego", group: "build"},
	{name: "clean", group: "build"},
	{name: "demo", group: "run"},
	{name: "tui", group: "run"},
	{name: "serve", group: "run"},
	{name: "full", group: "run"},
	{name: "db:up", group: "database"},
	{name: "db:down", group: "database"},
	{name: "db:reset", group: "database"},
	{name: "db:status", group: "database"},
	{name: "migrate", group: "database"},
	{name: "migrate:create", group: "database"},
	{name: "migrate:check", group: "database"},
	{name: "graph:check", group: "database"},
	{name: "proto:gen", group: "generation"},
	{name: "proto:check", group: "generation"},
	{name: "policy:compile", group: "generation"},
	{name: "policy:check", group: "generation"},
	{name: "results:gen", group: "generation"},
	{name: "results:check", group: "generation"},
	{name: "wasm:check", group: "generation"},
	{name: "test", group: "testing"},
	{name: "test:unit", group: "testing"},
	{name: "test:integration", group: "testing"},
	{name: "test:e2e", group: "testing"},
	{name: "test:race", group: "testing"},
	{name: "fuzz", group: "testing"},
	{name: "bench", group: "performance"},
	{name: "bench:compare", group: "performance"},
	{name: "profile", group: "performance"},
	{name: "perf", group: "performance"},
	{name: "load", group: "performance"},
	{name: "debug", group: "debugging"},
	{name: "debug:dap", group: "debugging"},
	{name: "debug:tui", group: "debugging"},
	{name: "docker:build", group: "containers"},
	{name: "docker:run", group: "containers"},
	{name: "docker:full", group: "containers"},
}

func newRoot(deps dependencies) *cobra.Command {
	root := &cobra.Command{
		Use:           "devx",
		Short:         "NornRune developer workflows",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
	}
	root.RunE = func(cmd *cobra.Command, _ []string) error {
		if deps.menu == nil {
			return errWorkflowUnavailable
		}
		options, err := menuOptions(deps)
		if err != nil {
			return err
		}
		selected, err := deps.menu.Select(cmd.Context(), options, deps.stdin, deps.stdout)
		if err != nil {
			return err
		}
		workflow, remaining, err := root.Find([]string{selected})
		if err != nil || workflow == root || len(remaining) != 0 || workflow.RunE == nil {
			return errWorkflowUnavailable
		}
		workflow.SetContext(cmd.Context())
		return workflow.RunE(workflow, nil)
	}
	root.AddGroup(
		&cobra.Group{ID: "setup", Title: "Setup:"},
		&cobra.Group{ID: "build", Title: "Build:"},
		&cobra.Group{ID: "run", Title: "Run:"},
		&cobra.Group{ID: "database", Title: "Database:"},
		&cobra.Group{ID: "generation", Title: "Generation:"},
		&cobra.Group{ID: "testing", Title: "Testing:"},
		&cobra.Group{ID: "performance", Title: "Performance:"},
		&cobra.Group{ID: "debugging", Title: "Debugging:"},
		&cobra.Group{ID: "containers", Title: "Containers:"},
	)
	for _, definition := range commandDefinitions {
		name := definition.name
		workflow := &cobra.Command{
			Use:     name,
			Short:   name,
			GroupID: definition.group,
			Args:    cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				return runNamedWorkflow(cmd.Context(), deps, name)
			},
		}
		if name == "install" {
			options := installOptions{}
			workflow.Flags().BoolVar(&options.dryRun, "dry-run", false, "print the install plan without executing it")
			workflow.Flags().BoolVarP(&options.yes, "yes", "y", false, "execute without an interactive confirmation")
			workflow.RunE = func(cmd *cobra.Command, _ []string) error {
				return runInstall(cmd.Context(), deps, options)
			}
		}
		if name == "uninstall" {
			options := uninstallOptions{}
			workflow.Flags().StringVar(&options.prefix, "prefix", "", "installation prefix (default: $DEVX_PREFIX or $HOME/.local)")
			workflow.Flags().BoolVar(&options.dryRun, "dry-run", false, "print the uninstall action without executing it")
			workflow.RunE = func(cmd *cobra.Command, _ []string) error {
				return runUninstall(cmd.Context(), deps, options)
			}
		}
		if name == "clean" {
			workflow.RunE = func(*cobra.Command, []string) error { return runClean(deps) }
		}
		if name == "completion" {
			shell := "bash"
			workflow.Flags().StringVar(&shell, "shell", "bash", "completion shell: bash, zsh, fish, or powershell")
			workflow.RunE = func(*cobra.Command, []string) error { return writeCompletion(root, shell, deps.stdout) }
		}
		if name == "migrate:create" {
			migrationName := ""
			workflow.Flags().StringVar(&migrationName, "name", "", "lowercase underscore migration name")
			workflow.RunE = func(*cobra.Command, []string) error { return createMigration(deps, migrationName) }
		}
		root.AddCommand(workflow)
	}
	if deps.stdin != nil {
		root.SetIn(deps.stdin)
	}
	if deps.stdout != nil {
		root.SetOut(deps.stdout)
	}
	if deps.stderr != nil {
		root.SetErr(deps.stderr)
	}
	return root
}

func menuOptions(deps dependencies) ([]menuOption, error) {
	repository, err := dependencyRepositoryRoot(deps)
	if err != nil {
		return nil, err
	}
	options := make([]menuOption, len(commandDefinitions))
	for index, definition := range commandDefinitions {
		reason := workflowBlocker(definition.name, deps, repository)
		options[index] = menuOption{
			name: definition.name, group: definition.group, available: reason == "", reason: reason,
		}
	}
	return options, nil
}

func commandGroupTitle(id string) string {
	switch id {
	case "setup":
		return "Setup"
	case "build":
		return "Build"
	case "run":
		return "Run"
	case "database":
		return "Database"
	case "generation":
		return "Generation"
	case "testing":
		return "Testing"
	case "performance":
		return "Performance"
	case "debugging":
		return "Debugging"
	case "containers":
		return "Containers"
	default:
		return id
	}
}

func runNamedWorkflow(ctx context.Context, deps dependencies, name string) error {
	if name == "status" {
		return writeStatus(deps)
	}
	if name == "doctor" {
		return runDoctor(ctx, deps)
	}
	plan, ok := namedCommandPlan(name)
	if !ok || ctx == nil || deps.getwd == nil || deps.readFile == nil || deps.runner == nil {
		return errWorkflowUnavailable
	}
	repository, err := dependencyRepositoryRoot(deps)
	if err != nil {
		return err
	}
	if reason := workflowBlocker(name, deps, repository); reason != "" {
		return fmt.Errorf("%w: %s: %s", ErrWorkflowBlocked, name, reason)
	}
	for _, spec := range plan {
		spec, err = bindRepositoryPaths(repository, spec)
		if err != nil {
			return err
		}
		commandCtx, cancel := context.WithTimeout(ctx, spec.timeout)
		err = deps.runner.Run(commandCtx, repository, spec, deps.stdin, deps.stdout, deps.stderr)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func bindRepositoryPaths(repository string, spec commandSpec) (commandSpec, error) {
	if len(spec.repositoryPathArguments) == 0 {
		return spec, nil
	}
	arguments := slices.Clone(spec.arguments)
	for _, rawIndex := range spec.repositoryPathArguments {
		index := int(rawIndex)
		if index >= len(arguments) || filepath.IsAbs(arguments[index]) {
			return commandSpec{}, errWorkflowUnavailable
		}
		arguments[index] = filepath.Join(repository, arguments[index])
	}
	spec.arguments = arguments
	return spec, nil
}

func dependencyRepositoryRoot(deps dependencies) (string, error) {
	if deps.getwd == nil || deps.readFile == nil {
		return "", errWorkflowUnavailable
	}
	workingDirectory, err := deps.getwd()
	if err != nil {
		return "", errors.Join(ErrRepositoryRoot, err)
	}
	return findRepositoryRoot(workingDirectory, deps.readFile)
}

func findRepositoryRoot(start string, readFile func(string) ([]byte, error)) (string, error) {
	if start == "" || readFile == nil {
		return "", ErrRepositoryRoot
	}
	current, err := filepath.Abs(start)
	if err != nil {
		return "", errors.Join(ErrRepositoryRoot, err)
	}
	for {
		module, err := readFile(filepath.Join(current, "go.mod"))
		if err == nil {
			if validModuleDirective(module) {
				return current, nil
			}
			return "", ErrRepositoryRoot
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", errors.Join(ErrRepositoryRoot, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", ErrRepositoryRoot
		}
		current = parent
	}
}

func validModuleDirective(module []byte) bool {
	for len(module) != 0 {
		line := module
		if newline := bytes.IndexByte(module, '\n'); newline >= 0 {
			line = module[:newline]
			module = module[newline+1:]
		} else {
			module = nil
		}
		line = bytes.TrimSpace(line)
		if len(line) <= len(moduleKeyword) || !bytes.HasPrefix(line, moduleKeyword) ||
			(line[len(moduleKeyword)] != ' ' && line[len(moduleKeyword)] != '\t') {
			continue
		}
		line = bytes.TrimSpace(line[len(moduleKeyword):])
		if bytes.IndexAny(line, " \t") >= 0 {
			return false
		}
		return bytes.Equal(line, moduleDirective)
	}
	return false
}
