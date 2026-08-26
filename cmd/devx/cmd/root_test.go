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
	"strings"
	"testing"
	"time"
)

type recordedRun struct {
	ctx       context.Context
	directory string
	spec      commandSpec
}

type recordingRunner struct {
	runs []recordedRun
}

func (runner *recordingRunner) Run(ctx context.Context, directory string, spec commandSpec, _ io.Reader, _, _ io.Writer) error {
	runner.runs = append(runner.runs, recordedRun{ctx: ctx, directory: directory, spec: spec})
	return nil
}

type recordingMenu struct {
	selected string
	options  []menuOption
}

func (menu *recordingMenu) Select(_ context.Context, options []menuOption, _ io.Reader, _ io.Writer) (string, error) {
	menu.options = append(menu.options, options...)
	return menu.selected, nil
}

type recordingConfirmation struct {
	confirmed bool
	calls     int
}

func (prompt *recordingConfirmation) Confirm(context.Context, string, io.Reader, io.Writer) (bool, error) {
	prompt.calls++
	return prompt.confirmed, nil
}

func TestRootDefinesApprovedCommandGroups(t *testing.T) {
	t.Parallel()

	root := newRoot(dependencies{})
	want := map[string][]string{
		"setup":       {"completion", "doctor", "install", "status", "uninstall"},
		"build":       {"build", "build:exp", "build:purego", "clean"},
		"run":         {"demo", "full", "serve", "tui"},
		"database":    {"db:down", "db:reset", "db:status", "db:up", "graph:check", "migrate", "migrate:check", "migrate:create"},
		"generation":  {"policy:check", "policy:compile", "proto:check", "proto:gen", "results:check", "results:gen"},
		"testing":     {"fuzz", "test", "test:e2e", "test:integration", "test:race", "test:unit"},
		"performance": {"bench", "bench:compare", "load", "perf", "profile"},
		"debugging":   {"debug", "debug:dap", "debug:tui"},
		"containers":  {"docker:build", "docker:full", "docker:run"},
	}
	got := make(map[string][]string, len(want))
	for _, command := range root.Commands() {
		if command.Name() == "help" {
			continue
		}
		got[command.GroupID] = append(got[command.GroupID], command.Name())
	}
	if len(got) != len(want) {
		t.Fatalf("command groups = %v", got)
	}
	for group, names := range want {
		slices.Sort(got[group])
		if !slices.Equal(got[group], names) {
			t.Errorf("%s commands = %v, want %v", group, got[group], names)
		}
	}
}

func TestFindRepositoryRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/sebishogun/nornrune\n\ngo 1.27.0\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	nested := filepath.Join(root, "cmd", "devx", "cmd")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("MkdirAll(nested) error = %v", err)
	}
	got, err := findRepositoryRoot(nested, os.ReadFile)
	if err != nil {
		t.Fatalf("findRepositoryRoot() error = %v", err)
	}
	if got != root {
		t.Fatalf("findRepositoryRoot() = %q, want %q", got, root)
	}
}

func TestFindRepositoryRootRejectsDifferentModule(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/not-nornrune\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	if _, err := findRepositoryRoot(root, os.ReadFile); !errors.Is(err, ErrRepositoryRoot) {
		t.Fatalf("findRepositoryRoot() error = %v, want ErrRepositoryRoot", err)
	}
}

func TestModuleDirectiveRequiresKeywordBoundary(t *testing.T) {
	t.Parallel()

	for _, module := range []string{
		"modulegithub.com/sebishogun/nornrune\n",
		"module_name github.com/sebishogun/nornrune\n",
		"module github.com/sebishogun/nornrune extra\n",
	} {
		if validModuleDirective([]byte(module)) {
			t.Errorf("validModuleDirective(%q) = true", module)
		}
	}
}

func TestBuildRunsExactBoundedCommandFromRepositoryRoot(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module github.com/sebishogun/nornrune\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	nested := filepath.Join(repository, "cmd", "devx")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("MkdirAll(nested) error = %v", err)
	}
	runner := &recordingRunner{}
	var stdout, stderr bytes.Buffer
	root := newRoot(dependencies{
		getwd:    func() (string, error) { return nested, nil },
		readFile: os.ReadFile,
		runner:   runner,
		stdin:    bytes.NewReader(nil),
		stdout:   &stdout,
		stderr:   &stderr,
	})
	root.SetArgs([]string{"build"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute(build) error = %v", err)
	}
	if len(runner.runs) != 1 {
		t.Fatalf("runner calls = %d", len(runner.runs))
	}
	run := runner.runs[0]
	if run.directory != repository || run.spec.executable != "go" ||
		!slices.Equal(run.spec.arguments, []string{"build", "-trimpath", "-o", "bin/nornrune", "./cmd/nornrune"}) {
		t.Fatalf("build run = directory:%q spec:%+v", run.directory, run.spec)
	}
	deadline, ok := run.ctx.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 2*time.Minute {
		t.Fatalf("build deadline = %v, present:%v", deadline, ok)
	}
	if !errors.Is(run.ctx.Err(), context.Canceled) {
		t.Fatalf("build context after command = %v", run.ctx.Err())
	}
}

func TestDatabaseResetRunsOrderedBoundedCommands(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module github.com/sebishogun/nornrune\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	runner := &recordingRunner{}
	root := newRoot(dependencies{
		getwd: func() (string, error) { return repository, nil }, readFile: os.ReadFile, runner: runner,
	})
	root.SetArgs([]string{"db:reset"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute(db:reset) error = %v", err)
	}
	if len(runner.runs) != 2 {
		t.Fatalf("runner calls = %d", len(runner.runs))
	}
	want := [][]string{
		{"compose", "down", "-v"},
		{"compose", "up", "-d", "--wait", "postgres"},
	}
	for index, run := range runner.runs {
		if run.spec.executable != "docker" || !slices.Equal(run.spec.arguments, want[index]) {
			t.Errorf("run %d = %+v, want docker %v", index, run.spec, want[index])
		}
		if !errors.Is(run.ctx.Err(), context.Canceled) {
			t.Errorf("run %d context after command = %v", index, run.ctx.Err())
		}
	}
}

func TestNoArgumentRootBuildsMenuAndRunsSelection(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module github.com/sebishogun/nornrune\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	menu := &recordingMenu{selected: "build"}
	runner := &recordingRunner{}
	root := newRoot(dependencies{
		getwd: func() (string, error) { return repository, nil }, readFile: os.ReadFile,
		menu: menu, runner: runner,
	})
	root.SetArgs(nil)
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute(menu) error = %v", err)
	}
	if len(menu.options) != len(commandDefinitions) {
		t.Fatalf("menu options = %d, want %d", len(menu.options), len(commandDefinitions))
	}
	for index, option := range menu.options {
		definition := commandDefinitions[index]
		if option.name != definition.name || option.group != definition.group {
			t.Fatalf("menu option %d = %+v, want %+v", index, option, definition)
		}
	}
	if len(runner.runs) != 1 || runner.runs[0].spec.executable != "go" {
		t.Fatalf("runner calls = %+v", runner.runs)
	}
}

func TestStatusReportsAndGatesMissingWorkflowPrerequisite(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module github.com/sebishogun/nornrune\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	lookPath := func(name string) (string, error) {
		if name == "docker" {
			return "", os.ErrNotExist
		}
		return filepath.Join("/usr/bin", name), nil
	}
	var output bytes.Buffer
	deps := dependencies{
		getwd: func() (string, error) { return repository, nil }, readFile: os.ReadFile,
		lookPath: lookPath, runner: &recordingRunner{}, stdout: &output,
	}
	status := newRoot(deps)
	status.SetArgs([]string{"status"})
	if err := status.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute(status) error = %v", err)
	}
	if !strings.Contains(output.String(), "build\trunnable\n") ||
		!strings.Contains(output.String(), "db:up\tblocked: missing executable docker\n") {
		t.Fatalf("status output:\n%s", output.String())
	}

	runner := &recordingRunner{}
	deps.runner = runner
	database := newRoot(deps)
	database.SetArgs([]string{"db:up"})
	err := database.ExecuteContext(context.Background())
	if !errors.Is(err, ErrWorkflowBlocked) {
		t.Fatalf("Execute(db:up) error = %v", err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("runner calls = %d", len(runner.runs))
	}
}

func TestStatusReportsAndGatesMissingRepositoryAsset(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module github.com/sebishogun/nornrune\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	var output bytes.Buffer
	deps := dependencies{
		getwd: func() (string, error) { return repository, nil }, readFile: os.ReadFile,
		lookPath: func(name string) (string, error) { return filepath.Join("/usr/bin", name), nil },
		stat:     os.Stat, runner: &recordingRunner{}, stdout: &output,
	}
	status := newRoot(deps)
	status.SetArgs([]string{"status"})
	if err := status.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute(status) error = %v", err)
	}
	if !strings.Contains(output.String(), "docker:build\tblocked: missing repository asset Dockerfile\n") {
		t.Fatalf("status output:\n%s", output.String())
	}

	runner := &recordingRunner{}
	deps.runner = runner
	build := newRoot(deps)
	build.SetArgs([]string{"docker:build"})
	err := build.ExecuteContext(context.Background())
	if !errors.Is(err, ErrWorkflowBlocked) {
		t.Fatalf("Execute(docker:build) error = %v", err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("runner calls = %d", len(runner.runs))
	}
}

func TestKnownUnavailableWorkflowIsGatedBeforeExecution(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module github.com/sebishogun/nornrune\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	runner := &recordingRunner{}
	root := newRoot(dependencies{
		getwd: func() (string, error) { return repository, nil }, readFile: os.ReadFile,
		lookPath: func(name string) (string, error) { return filepath.Join("/usr/bin", name), nil }, runner: runner,
	})
	root.SetArgs([]string{"build:exp"})
	err := root.ExecuteContext(context.Background())
	if !errors.Is(err, ErrWorkflowBlocked) || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("Execute(build:exp) error = %v", err)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("runner calls = %d", len(runner.runs))
	}
}

func TestWorkflowPrerequisitesMatchGeneratedCodePlans(t *testing.T) {
	t.Parallel()

	if got := workflowExecutables("proto:gen"); !slices.Equal(got, []string{"go", "buf"}) {
		t.Fatalf("proto:gen executables = %v", got)
	}
	if got := workflowExecutables("proto:check"); !slices.Equal(got, []string{"go", "buf", "docker"}) {
		t.Fatalf("proto:check executables = %v", got)
	}
}

func TestPerformanceWorkflowPlansAndRequirements(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		executable  string
		arguments   []string
		requirement []string
		assets      []string
	}{
		{
			name: "bench", executable: "go",
			arguments:   []string{"test", "-run", "^$", "-bench", "^BenchmarkEvaluate$", "-benchmem", "-benchtime", "200ms", "-count", "6", "-timeout", "120s", "./internal/eval"},
			requirement: []string{"go"},
		},
		{
			name: "bench:compare", executable: "sh", arguments: []string{"scripts/bench-compare.sh"},
			requirement: []string{"sh", "timeout", "benchstat"}, assets: []string{"scripts/bench-compare.sh"},
		},
		{
			name: "load", executable: "go",
			arguments:   []string{"run", "./cmd/loadgen", "-protocol", "http", "-target", "127.0.0.1:8080", "-requests", "1000", "-concurrency", "4", "-timeout", "30s"},
			requirement: []string{"go"}, assets: []string{"cmd/loadgen/main.go"},
		},
	}
	for _, test := range tests {
		plan, ok := namedCommandPlan(test.name)
		if !ok || len(plan) != 1 || plan[0].executable != test.executable || !slices.Equal(plan[0].arguments, test.arguments) {
			t.Errorf("workflow %q plan = %+v, want %s %v", test.name, plan, test.executable, test.arguments)
		}
		if got := workflowExecutables(test.name); !slices.Equal(got, test.requirement) {
			t.Errorf("workflow %q executables = %v, want %v", test.name, got, test.requirement)
		}
		if got := workflowRepositoryAssets(test.name); !slices.Equal(got, test.assets) {
			t.Errorf("workflow %q assets = %v, want %v", test.name, got, test.assets)
		}
		if reason := workflowStaticBlocker(test.name); reason != "" {
			t.Errorf("workflow %q static blocker = %q", test.name, reason)
		}
	}
}

func TestImplementedTUIWorkflowsHaveNoStaticBlocker(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"tui", "debug:tui"} {
		if reason := workflowStaticBlocker(name); reason != "" {
			t.Errorf("workflow %q static blocker = %q", name, reason)
		}
	}
}

func TestStatusBlocksInstallWithoutSupportedPackageManager(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module github.com/sebishogun/nornrune\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	var output bytes.Buffer
	root := newRoot(dependencies{
		getwd: func() (string, error) { return repository, nil }, readFile: os.ReadFile,
		lookPath: func(string) (string, error) { return "", os.ErrNotExist }, stdout: &output,
	})
	root.SetArgs([]string{"status"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute(status) error = %v", err)
	}
	if !strings.Contains(output.String(), "install\tblocked: no supported package manager found\n") {
		t.Fatalf("status output:\n%s", output.String())
	}
}

func TestDoctorRunsReadOnlyBoundedProbesAndReportsMissingTools(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module github.com/sebishogun/nornrune\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	var lookedUp []string
	lookPath := func(name string) (string, error) {
		lookedUp = append(lookedUp, name)
		if name == "dlv" {
			return "", os.ErrNotExist
		}
		return filepath.Join("/usr/bin", name), nil
	}
	runner := &recordingRunner{}
	var output bytes.Buffer
	root := newRoot(dependencies{
		getwd: func() (string, error) { return repository, nil }, readFile: os.ReadFile,
		lookPath: lookPath, runner: runner, stdout: &output,
	})
	root.SetArgs([]string{"doctor"})
	err := root.ExecuteContext(context.Background())
	if !errors.Is(err, ErrDoctorFailed) {
		t.Fatalf("Execute(doctor) error = %v", err)
	}
	wantLookups := []string{"go", "docker", "docker", "dlv", "buf", "protoc", "benchstat", "psql"}
	if !slices.Equal(lookedUp, wantLookups) {
		t.Fatalf("looked-up tools = %v, want %v", lookedUp, wantLookups)
	}
	if len(runner.runs) != len(wantLookups)-1 {
		t.Fatalf("version probe calls = %d", len(runner.runs))
	}
	wantCommands := []struct {
		executable string
		arguments  []string
	}{
		{executable: "go", arguments: []string{"version"}},
		{executable: "docker", arguments: []string{"--version"}},
		{executable: "docker", arguments: []string{"compose", "version"}},
		{executable: "buf", arguments: []string{"--version"}},
		{executable: "protoc", arguments: []string{"--version"}},
		{executable: "benchstat", arguments: []string{"-h"}},
		{executable: "psql", arguments: []string{"--version"}},
	}
	for index, run := range runner.runs {
		want := wantCommands[index]
		if run.spec.executable != want.executable || !slices.Equal(run.spec.arguments, want.arguments) {
			t.Errorf("probe %d = %+v, want %+v", index, run.spec, want)
		}
		if !errors.Is(run.ctx.Err(), context.Canceled) {
			t.Errorf("probe %d context after command = %v", index, run.ctx.Err())
		}
	}
	if !strings.Contains(output.String(), "Delve\tmissing executable dlv\n") {
		t.Fatalf("doctor output:\n%s", output.String())
	}
}

func TestInstallDryRunPrintsExactPlanWithoutExecuting(t *testing.T) {
	t.Parallel()

	lookPath := func(name string) (string, error) {
		if name == "apt-get" {
			return "/usr/bin/apt-get", nil
		}
		return "", os.ErrNotExist
	}
	runner := &recordingRunner{}
	var output bytes.Buffer
	root := newRoot(dependencies{lookPath: lookPath, runner: runner, stdout: &output})
	root.SetArgs([]string{"install", "--dry-run"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute(install --dry-run) error = %v", err)
	}
	want := strings.Join([]string{
		"Package manager: apt-get",
		"[elevated] sudo apt-get install -y golang-go docker.io docker-compose-plugin protobuf-compiler postgresql-client",
		"[user] go install github.com/go-delve/delve/cmd/dlv@v1.27.1",
		"[user] go install github.com/bufbuild/buf/cmd/buf@v1.72.0",
		"[user] go install golang.org/x/perf/cmd/benchstat@v0.0.0-20260819171926-ebcb4798430d",
		"",
	}, "\n")
	if output.String() != want {
		t.Fatalf("install plan:\n%s\nwant:\n%s", output.String(), want)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("runner calls = %d", len(runner.runs))
	}
}

func TestInstallConfirmationAndNonInteractiveExecution(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module github.com/sebishogun/nornrune\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	lookPath := func(name string) (string, error) {
		if name == "apt-get" {
			return "/usr/bin/apt-get", nil
		}
		return "", os.ErrNotExist
	}
	cancelPrompt := &recordingConfirmation{}
	cancelRunner := &recordingRunner{}
	cancel := newRoot(dependencies{
		getwd: func() (string, error) { return repository, nil }, readFile: os.ReadFile,
		lookPath: lookPath, runner: cancelRunner, confirm: cancelPrompt, stdout: io.Discard,
	})
	cancel.SetArgs([]string{"install"})
	if err := cancel.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute(cancel install) error = %v", err)
	}
	if cancelPrompt.calls != 1 || len(cancelRunner.runs) != 0 {
		t.Fatalf("cancelled install = prompt calls:%d runner calls:%d", cancelPrompt.calls, len(cancelRunner.runs))
	}

	ciPrompt := &recordingConfirmation{}
	ciRunner := &recordingRunner{}
	ci := newRoot(dependencies{
		getwd: func() (string, error) { return repository, nil }, readFile: os.ReadFile,
		lookPath: lookPath, runner: ciRunner, confirm: ciPrompt, stdout: io.Discard,
	})
	ci.SetArgs([]string{"install", "--yes"})
	if err := ci.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute(install --yes) error = %v", err)
	}
	if ciPrompt.calls != 0 || len(ciRunner.runs) != 4 {
		t.Fatalf("CI install = prompt calls:%d runner calls:%d", ciPrompt.calls, len(ciRunner.runs))
	}
	for index, run := range ciRunner.runs {
		if !errors.Is(run.ctx.Err(), context.Canceled) {
			t.Errorf("install step %d context after command = %v", index, run.ctx.Err())
		}
	}
}

func TestEveryProcessWorkflowHasConcreteBoundedPlan(t *testing.T) {
	t.Parallel()

	special := map[string]bool{
		"install": true, "uninstall": true, "doctor": true, "status": true,
		"completion": true, "clean": true, "migrate:create": true,
	}
	for _, definition := range commandDefinitions {
		if special[definition.name] {
			continue
		}
		plan, ok := namedCommandPlan(definition.name)
		if !ok || len(plan) == 0 {
			t.Errorf("workflow %q has no process plan", definition.name)
			continue
		}
		for index, spec := range plan {
			if spec.executable == "" || len(spec.arguments) == 0 || spec.timeout <= 0 {
				t.Errorf("workflow %q step %d is not concrete and bounded: %+v", definition.name, index, spec)
			}
		}
	}
}

func TestRepresentativeWorkflowPlansUseExactArguments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		executable string
		arguments  []string
	}{
		{name: "demo", executable: "go", arguments: []string{"run", "./cmd/nornrune", "demo"}},
		{name: "proto:gen", executable: "go", arguments: []string{"build", "-trimpath", "-o", ".nornrune/tools/protoc-gen-nornrune", "./cmd/protoc-gen-nornrune"}},
		{name: "test", executable: "go", arguments: []string{"test", "-count=1", "-timeout", "60s", "./..."}},
		{name: "bench", executable: "go", arguments: []string{"test", "-run", "^$", "-bench", "^BenchmarkEvaluate$", "-benchmem", "-benchtime", "200ms", "-count", "6", "-timeout", "120s", "./internal/eval"}},
		{name: "load", executable: "go", arguments: []string{"run", "./cmd/loadgen", "-protocol", "http", "-target", "127.0.0.1:8080", "-requests", "1000", "-concurrency", "4", "-timeout", "30s"}},
		{name: "debug", executable: "dlv", arguments: []string{"dap", "--listen", "127.0.0.1:38697", "--log"}},
		{name: "debug:dap", executable: "dlv", arguments: []string{"dap", "--listen", "127.0.0.1:38697", "--log"}},
		{name: "docker:build", executable: "docker", arguments: []string{"build", "-t", "nornrune:dev", "."}},
	}
	for _, test := range tests {
		plan, ok := namedCommandPlan(test.name)
		if !ok || len(plan) == 0 {
			t.Errorf("workflow %q plan is missing", test.name)
			continue
		}
		got := plan[0]
		if got.executable != test.executable || !slices.Equal(got.arguments, test.arguments) {
			t.Errorf("workflow %q first step = %+v, want %s %v", test.name, got, test.executable, test.arguments)
		}
	}
}

func TestProtoGenerationBuildsPluginAndRunsBothPinnedTemplates(t *testing.T) {
	t.Parallel()

	plan, ok := namedCommandPlan("proto:gen")
	if !ok || len(plan) != 3 {
		t.Fatalf("proto:gen plan = (%+v, %v), want three steps", plan, ok)
	}
	want := []struct {
		executable string
		arguments  []string
	}{
		{executable: "go", arguments: []string{"build", "-trimpath", "-o", ".nornrune/tools/protoc-gen-nornrune", "./cmd/protoc-gen-nornrune"}},
		{executable: "buf", arguments: []string{"generate"}},
		{executable: "buf", arguments: []string{"generate", "--template", "buf.frontend.gen.yaml"}},
	}
	for index, step := range plan {
		if step.executable != want[index].executable || !slices.Equal(step.arguments, want[index].arguments) || step.timeout != 2*time.Minute {
			t.Errorf("proto:gen step %d = %+v, want %s %v with two-minute timeout", index, step, want[index].executable, want[index].arguments)
		}
	}
}

func TestDebugWorkflowAliasesDAPServer(t *testing.T) {
	t.Parallel()

	debug, ok := namedCommandPlan("debug")
	if !ok || len(debug) != 1 {
		t.Fatalf("debug plan = (%+v, %v)", debug, ok)
	}
	dap, ok := namedCommandPlan("debug:dap")
	if !ok || len(dap) != 1 {
		t.Fatalf("debug:dap plan = (%+v, %v)", dap, ok)
	}
	if debug[0].executable != dap[0].executable || !slices.Equal(debug[0].arguments, dap[0].arguments) ||
		debug[0].timeout != dap[0].timeout || len(debug[0].repositoryPathArguments) != 0 {
		t.Fatalf("debug plan = %+v, debug:dap plan = %+v", debug[0], dap[0])
	}
}

func TestExecRunnerUsesExactArgvDirectoryAndEnvironment(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	var output bytes.Buffer
	spec := commandSpec{
		executable: os.Args[0],
		arguments: []string{
			"-test.run=^TestExecRunnerHelperProcess$", "--", "two words", "$HOME", "a;b",
		},
		environment: []string{"DEVX_HELPER_PROCESS=print", "DEVX_HELPER_VALUE=preserved"},
		timeout:     10 * time.Second,
	}
	if err := (execRunner{}).Run(context.Background(), directory, spec, nil, &output, &output); err != nil {
		t.Fatalf("Run(helper) error = %v\noutput:\n%s", err, output.String())
	}
	want := directory + "\npreserved\ntwo words\x00$HOME\x00a;b\n"
	if output.String() != want {
		t.Fatalf("helper output = %q, want %q", output.String(), want)
	}
}

func TestExecRunnerEnforcesSpecTimeout(t *testing.T) {
	t.Parallel()

	spec := commandSpec{
		executable:  os.Args[0],
		arguments:   []string{"-test.run=^TestExecRunnerHelperProcess$"},
		environment: []string{"DEVX_HELPER_PROCESS=wait"},
		timeout:     20 * time.Millisecond,
	}
	started := time.Now()
	err := (execRunner{}).Run(context.Background(), t.TempDir(), spec, nil, io.Discard, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run(timeout helper) error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("timeout helper elapsed = %v", elapsed)
	}
}

func TestExecRunnerHelperProcess(t *testing.T) {
	switch os.Getenv("DEVX_HELPER_PROCESS") {
	case "":
		return
	case "wait":
		time.Sleep(200 * time.Millisecond)
		os.Exit(0)
	case "print":
	default:
		t.Fatalf("unknown helper mode")
	}
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		t.Fatal("missing argument separator")
	}
	if _, err := fmt.Fprintf(os.Stdout, "%s\n%s\n%s\n", directory, os.Getenv("DEVX_HELPER_VALUE"), strings.Join(os.Args[separator+1:], "\x00")); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func TestCleanRemovesOnlyRepositoryGeneratedPaths(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module github.com/sebishogun/nornrune\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	var removed []string
	root := newRoot(dependencies{
		getwd: func() (string, error) { return repository, nil }, readFile: os.ReadFile,
		removeAll: func(path string) error { removed = append(removed, path); return nil },
	})
	root.SetArgs([]string{"clean"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute(clean) error = %v", err)
	}
	want := []string{
		filepath.Join(repository, "bin"),
		filepath.Join(repository, ".nornrune"),
		filepath.Join(repository, "cpu.pprof"),
	}
	if !slices.Equal(removed, want) {
		t.Fatalf("removed paths = %v, want %v", removed, want)
	}
}

func TestMigrateCreateWritesNextPairedMigration(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module github.com/sebishogun/nornrune\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	migrations := filepath.Join(repository, "migrations")
	if err := os.Mkdir(migrations, 0o700); err != nil {
		t.Fatalf("Mkdir(migrations) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(migrations, "000007_existing.up.sql"), nil, 0o600); err != nil {
		t.Fatalf("WriteFile(existing migration) error = %v", err)
	}
	var output bytes.Buffer
	root := newRoot(dependencies{
		getwd: func() (string, error) { return repository, nil }, readFile: os.ReadFile, stdout: &output,
	})
	root.SetArgs([]string{"migrate:create", "--name", "add_audit_index"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute(migrate:create) error = %v", err)
	}
	for _, filename := range []string{"000008_add_audit_index.up.sql", "000008_add_audit_index.down.sql"} {
		contents, err := os.ReadFile(filepath.Join(migrations, filename))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", filename, err)
		}
		if string(contents) != "BEGIN;\n\nCOMMIT;\n" {
			t.Fatalf("%s contents = %q", filename, contents)
		}
	}
	if output.String() != "created migrations/000008_add_audit_index.{up,down}.sql\n" {
		t.Fatalf("migrate:create output = %q", output.String())
	}
}
