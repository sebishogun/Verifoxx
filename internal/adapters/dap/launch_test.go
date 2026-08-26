package dap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLauncherPlanBuildsLoopbackDAPCommand(t *testing.T) {
	t.Parallel()

	selected := false
	launcher := Launcher{
		selectPort: func(context.Context) (uint16, error) {
			selected = true
			return 43210, nil
		},
	}
	targetArguments := []string{"demo", "--output", "json"}
	plan, err := launcher.Plan(context.Background(), Config{
		DelvePath:        "/opt/dlv",
		WorkingDirectory: "/workspace/nornrune",
		Target:           "./cmd/nornrune",
		TargetArguments:  targetArguments,
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if !selected {
		t.Fatal("Plan() did not select a port")
	}
	if plan.Executable != "/opt/dlv" || plan.WorkingDirectory != "/workspace/nornrune" ||
		plan.Address != "127.0.0.1:43210" {
		t.Fatalf("Plan() process = %+v", plan)
	}
	if want := []string{"dap", "--listen", "127.0.0.1:43210"}; !slices.Equal(plan.Arguments, want) {
		t.Fatalf("Plan() arguments = %q, want %q", plan.Arguments, want)
	}
	configuration := plan.Configuration
	if configuration.Type != "go" || configuration.Request != "launch" || configuration.Mode != "debug" ||
		configuration.Program != "./cmd/nornrune" || configuration.WorkingDirectory != "/workspace/nornrune" ||
		configuration.BuildFlags != "-tags=debug -gcflags='all=-N -l'" ||
		!slices.Equal(configuration.Arguments, targetArguments) {
		t.Fatalf("Plan() DAP configuration = %+v", configuration)
	}
	targetArguments[0] = "changed"
	if configuration.Arguments[0] != "demo" {
		t.Fatalf("Plan() retained caller arguments: %q", configuration.Arguments)
	}
}

func TestLauncherPlanPreservesCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (Launcher{}).Plan(ctx, Config{
		DelvePath:        "dlv",
		WorkingDirectory: ".",
		Target:           "./cmd/nornrune",
	})
	if !errors.Is(err, ErrInvalidLaunch) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Plan() error = %v, want ErrInvalidLaunch and context.Canceled", err)
	}
}

func TestLauncherPlanUsesFixedPortWithoutSelection(t *testing.T) {
	t.Parallel()

	launcher := Launcher{selectPort: func(context.Context) (uint16, error) {
		t.Fatal("fixed port called selector")
		return 0, nil
	}}
	plan, err := launcher.Plan(context.Background(), Config{
		DelvePath:        "dlv",
		WorkingDirectory: ".",
		Target:           "./cmd/nornrune",
		Port:             45678,
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if plan.Address != "127.0.0.1:45678" {
		t.Fatalf("Plan() address = %q", plan.Address)
	}
}

func TestLauncherPlanEnablesDelveLogging(t *testing.T) {
	t.Parallel()

	plan, err := (Launcher{}).Plan(context.Background(), Config{
		DelvePath:        "dlv",
		WorkingDirectory: ".",
		Target:           "./cmd/nornrune",
		Port:             45678,
		Log:              true,
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if want := []string{"dap", "--listen", "127.0.0.1:45678", "--log"}; !slices.Equal(plan.Arguments, want) {
		t.Fatalf("Plan() arguments = %q, want %q", plan.Arguments, want)
	}
}

func TestLauncherPlanReportsPortSelectionFailure(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("select port")
	launcher := Launcher{selectPort: func(context.Context) (uint16, error) { return 0, wantErr }}
	_, err := launcher.Plan(context.Background(), Config{
		DelvePath:        "dlv",
		WorkingDirectory: ".",
		Target:           "./cmd/nornrune",
	})
	if !errors.Is(err, ErrInvalidLaunch) || !errors.Is(err, wantErr) {
		t.Fatalf("Plan() error = %v, want ErrInvalidLaunch and selector error", err)
	}
}

func TestLauncherPlanRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	valid := Config{
		DelvePath:        "dlv",
		WorkingDirectory: ".",
		Target:           "./cmd/nornrune",
		Port:             45678,
	}
	tooManyArguments := make([]string, maxArguments+1)
	for index := range tooManyArguments {
		tooManyArguments[index] = "argument"
	}
	for _, test := range []struct {
		name   string
		config Config
	}{
		{name: "empty", config: Config{}},
		{name: "missing delve", config: Config{WorkingDirectory: ".", Target: "./cmd/nornrune", Port: 45678}},
		{name: "missing directory", config: Config{DelvePath: "dlv", Target: "./cmd/nornrune", Port: 45678}},
		{name: "missing target", config: Config{DelvePath: "dlv", WorkingDirectory: ".", Port: 45678}},
		{name: "too many arguments", config: Config{
			DelvePath: "dlv", WorkingDirectory: ".", Target: "./cmd/nornrune", Port: 45678,
			TargetArguments: tooManyArguments,
		}},
		{name: "nul argument", config: Config{
			DelvePath: "dlv", WorkingDirectory: ".", Target: "./cmd/nornrune", Port: 45678,
			TargetArguments: []string{"bad\x00argument"},
		}},
		{name: "oversized argument", config: Config{
			DelvePath: "dlv", WorkingDirectory: ".", Target: "./cmd/nornrune", Port: 45678,
			TargetArguments: []string{strings.Repeat("x", maxArgumentSize+1)},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := (Launcher{}).Plan(context.Background(), test.config); !errors.Is(err, ErrInvalidLaunch) {
				t.Fatalf("Plan() error = %v, want ErrInvalidLaunch", err)
			}
		})
	}
	if _, err := (Launcher{}).Plan(nil, valid); !errors.Is(err, ErrInvalidLaunch) {
		t.Fatalf("Plan(nil) error = %v, want ErrInvalidLaunch", err)
	}
}

func TestSelectLoopbackPort(t *testing.T) {
	t.Parallel()

	port, err := selectLoopbackPort(context.Background())
	if err != nil {
		t.Fatalf("selectLoopbackPort() error = %v", err)
	}
	if port == 0 {
		t.Fatal("selectLoopbackPort() port = 0")
	}
}

func TestLauncherCancellationStopsDelve(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	launcher := Launcher{commandContext: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDAPHelperProcess$")
		command.Env = append(os.Environ(), "NORNRUNE_DAP_HELPER=sleep")
		return command
	}}
	server, err := launcher.Launch(ctx, Config{
		DelvePath:        "dlv",
		WorkingDirectory: ".",
		Target:           "./cmd/nornrune",
		Port:             45678,
	})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	cancel()
	done := make(chan error, 1)
	go func() { done <- server.Wait() }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Wait() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait() did not reap canceled Delve process")
	}
}

func TestLauncherCancellationLetsDelveCleanUp(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	readyPath := filepath.Join(directory, "ready")
	cleanedPath := filepath.Join(directory, "cleaned")
	ctx, cancel := context.WithCancel(context.Background())
	launcher := Launcher{commandContext: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDAPHelperProcess$")
		command.Env = append(os.Environ(),
			"NORNRUNE_DAP_HELPER=graceful",
			"NORNRUNE_DAP_READY="+readyPath,
			"NORNRUNE_DAP_CLEANED="+cleanedPath,
		)
		return command
	}}
	server, err := launcher.Launch(ctx, Config{
		DelvePath:        "dlv",
		WorkingDirectory: ".",
		Target:           "./cmd/nornrune",
		Port:             45678,
	})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	waitForFile(t, readyPath)
	cancel()
	if err := server.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait() error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(cleanedPath); err != nil {
		t.Fatalf("Delve cleanup marker error = %v", err)
	}
}

func TestDelveInterruptDoesNotMarkExitedProcess(t *testing.T) {
	t.Parallel()

	command := exec.Command(os.Args[0], "-test.run=^TestDAPHelperProcess$")
	command.Env = append(os.Environ(), "NORNRUNE_DAP_HELPER=fail")
	if err := command.Run(); err == nil {
		t.Fatal("helper process error = nil")
	}
	var requested atomic.Bool
	err := interruptDelve(command, &requested)
	if !errors.Is(err, os.ErrProcessDone) {
		t.Fatalf("interruptDelve() error = %v, want os.ErrProcessDone", err)
	}
	if requested.Load() {
		t.Fatal("interruptDelve() marked cancellation after process exit")
	}
}

func TestLauncherReportsProcessStartFailure(t *testing.T) {
	t.Parallel()

	_, err := (Launcher{}).Launch(context.Background(), Config{
		DelvePath:        filepath.Join(t.TempDir(), "missing-dlv"),
		WorkingDirectory: ".",
		Target:           "./cmd/nornrune",
		Port:             45678,
	})
	if !errors.Is(err, ErrLaunchProcess) {
		t.Fatalf("Launch() error = %v, want ErrLaunchProcess", err)
	}
}

func TestLauncherCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	readyPath := filepath.Join(t.TempDir(), "ready")
	launcher := Launcher{commandContext: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDAPHelperProcess$")
		command.Env = append(os.Environ(), "NORNRUNE_DAP_HELPER=sleep", "NORNRUNE_DAP_READY="+readyPath)
		return command
	}}
	server, err := launcher.Launch(context.Background(), Config{
		DelvePath: "dlv", WorkingDirectory: ".", Target: "./cmd/nornrune", Port: 45678,
	})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	waitForFile(t, readyPath)
	if err := server.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestLauncherRoutesDelveOutput(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	launcher := Launcher{commandContext: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDAPHelperProcess$")
		command.Env = append(os.Environ(), "NORNRUNE_DAP_HELPER=output")
		return command
	}}
	server, err := launcher.Launch(context.Background(), Config{
		DelvePath: "dlv", WorkingDirectory: ".", Target: "./cmd/nornrune", Port: 45678,
		Stdout: &stdout, Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	if err := server.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if !strings.Contains(stdout.String(), "delve stdout\n") || !strings.Contains(stderr.String(), "delve stderr\n") {
		t.Fatalf("Delve output = stdout:%q stderr:%q", stdout.String(), stderr.String())
	}
}

func TestDAPHelperProcess(t *testing.T) {
	switch os.Getenv("NORNRUNE_DAP_HELPER") {
	case "":
		return
	case "sleep":
		if path := os.Getenv("NORNRUNE_DAP_READY"); path != "" {
			if err := os.WriteFile(path, []byte("ready"), 0o600); err != nil {
				t.Fatalf("write ready marker: %v", err)
			}
		}
		time.Sleep(30 * time.Second)
	case "graceful":
		interrupt := make(chan os.Signal, 1)
		signal.Notify(interrupt, os.Interrupt)
		defer signal.Stop(interrupt)
		if err := os.WriteFile(os.Getenv("NORNRUNE_DAP_READY"), []byte("ready"), 0o600); err != nil {
			t.Fatalf("write ready marker: %v", err)
		}
		select {
		case <-interrupt:
		case <-time.After(30 * time.Second):
			t.Fatal("helper did not receive interrupt")
		}
		if err := os.WriteFile(os.Getenv("NORNRUNE_DAP_CLEANED"), []byte("cleaned"), 0o600); err != nil {
			t.Fatalf("write cleanup marker: %v", err)
		}
	case "output":
		if _, err := os.Stdout.Write([]byte("delve stdout\n")); err != nil {
			t.Fatalf("write stdout: %v", err)
		}
		if _, err := os.Stderr.Write([]byte("delve stderr\n")); err != nil {
			t.Fatalf("write stderr: %v", err)
		}
	case "fail":
		os.Exit(23)
	default:
		t.Fatalf("unknown helper mode %q", os.Getenv("NORNRUNE_DAP_HELPER"))
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Stat(%q) error = %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSharedLaunchConfiguration(t *testing.T) {
	t.Parallel()

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() did not return the test path")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "..", ".vscode", "launch.json"))
	if err != nil {
		t.Fatalf("ReadFile(launch.json) error = %v", err)
	}
	var document struct {
		Version        string          `json:"version"`
		Configurations []Configuration `json:"configurations"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("Unmarshal(launch.json) error = %v", err)
	}
	if document.Version != "0.2.0" || len(document.Configurations) != 1 {
		t.Fatalf("launch.json header = version:%q configurations:%d", document.Version, len(document.Configurations))
	}
	configuration := document.Configurations[0]
	if configuration.Name != "Debug NornRune" || configuration.Type != "go" ||
		configuration.Request != "launch" || configuration.Mode != "debug" ||
		configuration.Program != "${workspaceFolder}/cmd/nornrune" ||
		configuration.WorkingDirectory != "${workspaceFolder}" ||
		configuration.BuildFlags != "-tags=debug -gcflags='all=-N -l'" ||
		!slices.Equal(configuration.Arguments, []string{
			"debug-worker", "--socket", "${workspaceFolder}/.nornrune/debug.sock",
		}) {
		t.Fatalf("launch.json configuration = %+v", configuration)
	}
}
