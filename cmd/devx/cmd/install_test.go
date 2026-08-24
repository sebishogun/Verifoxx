package cmd

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestInstallerDryRunSelectsPrefixAndPlansSymlink(t *testing.T) {
	t.Parallel()

	repository := installTestRepository(t)
	home := t.TempDir()
	prefix := filepath.Join(t.TempDir(), "prefix with spaces")
	output, err := runInstallTestScript(t, filepath.Join(repository, "cli", "install.sh"),
		[]string{"--dry-run", "--prefix", prefix},
		[]string{"HOME=" + home, "PATH=/usr/bin:/bin"},
	)
	if err != nil {
		t.Fatalf("install --dry-run error = %v\n%s", err, output)
	}
	wrapper := filepath.Join(repository, "cli", "devx")
	for _, line := range []string{
		"prefix: " + prefix,
		"[dry-run] mkdir -p " + filepath.Join(prefix, "bin"),
		"[dry-run] ln -s " + wrapper + " " + filepath.Join(prefix, "bin", "devx"),
		"PATH: " + filepath.Join(prefix, "bin") + " is not present",
		"Shell startup files were not modified.",
	} {
		if !strings.Contains(output, line+"\n") {
			t.Errorf("dry-run output missing %q:\n%s", line, output)
		}
	}
	if _, err := os.Stat(prefix); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run prefix stat error = %v", err)
	}
}

func TestInstallerPrefixPrecedence(t *testing.T) {
	t.Parallel()

	repository := installTestRepository(t)
	script := filepath.Join(repository, "cli", "install.sh")
	home := t.TempDir()
	environmentPrefix := filepath.Join(t.TempDir(), "environment")
	flagPrefix := filepath.Join(t.TempDir(), "flag")

	output, err := runInstallTestScript(t, script,
		[]string{"--dry-run", "--prefix", flagPrefix},
		[]string{"HOME=" + home, "DEVX_PREFIX=" + environmentPrefix, "PATH=/usr/bin:/bin"},
	)
	if err != nil {
		t.Fatalf("explicit prefix error = %v\n%s", err, output)
	}
	if !strings.Contains(output, "prefix: "+flagPrefix+"\n") {
		t.Fatalf("explicit prefix output:\n%s", output)
	}

	output, err = runInstallTestScript(t, script, []string{"--dry-run"},
		[]string{"HOME=" + home, "DEVX_PREFIX=" + environmentPrefix, "PATH=/usr/bin:/bin"})
	if err != nil {
		t.Fatalf("environment prefix error = %v\n%s", err, output)
	}
	if !strings.Contains(output, "prefix: "+environmentPrefix+"\n") {
		t.Fatalf("environment prefix output:\n%s", output)
	}

	output, err = runInstallTestScript(t, script, []string{"--dry-run"},
		[]string{"HOME=" + home, "PATH=/usr/bin:/bin"})
	if err != nil {
		t.Fatalf("default prefix error = %v\n%s", err, output)
	}
	if !strings.Contains(output, "prefix: "+filepath.Join(home, ".local")+"\n") {
		t.Fatalf("default prefix output:\n%s", output)
	}
}

func TestInstallerCreatesAndUninstallsOwnedLinkWithoutEditingShellFiles(t *testing.T) {
	t.Parallel()

	repository := installTestRepository(t)
	script := filepath.Join(repository, "cli", "install.sh")
	home := t.TempDir()
	prefix := filepath.Join(home, ".local")
	bashRC := filepath.Join(home, ".bashrc")
	zshRC := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(bashRC, []byte("bash sentinel\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.bashrc) error = %v", err)
	}
	if err := os.WriteFile(zshRC, []byte("zsh sentinel\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.zshrc) error = %v", err)
	}
	environment := []string{"HOME=" + home, "PATH=/usr/bin:/bin"}

	output, err := runInstallTestScript(t, script, []string{"--prefix", prefix}, environment)
	if err != nil {
		t.Fatalf("install error = %v\n%s", err, output)
	}
	link := filepath.Join(prefix, "bin", "devx")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink(devx) error = %v", err)
	}
	if target != filepath.Join(repository, "cli", "devx") {
		t.Fatalf("devx target = %q", target)
	}
	assertInstallTestFile(t, bashRC, "bash sentinel\n")
	assertInstallTestFile(t, zshRC, "zsh sentinel\n")

	output, err = runInstallTestScript(t, script, []string{"--uninstall", "--prefix", prefix}, environment)
	if err != nil {
		t.Fatalf("uninstall error = %v\n%s", err, output)
	}
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstalled link stat error = %v", err)
	}
	assertInstallTestFile(t, bashRC, "bash sentinel\n")
	assertInstallTestFile(t, zshRC, "zsh sentinel\n")
}

func TestInstallerRefusesToReplaceOrRemoveUnownedDestination(t *testing.T) {
	t.Parallel()

	repository := installTestRepository(t)
	script := filepath.Join(repository, "cli", "install.sh")
	home := t.TempDir()
	prefix := filepath.Join(home, ".local")
	bin := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	destination := filepath.Join(bin, "devx")
	if err := os.WriteFile(destination, []byte("user file\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(destination) error = %v", err)
	}
	environment := []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	if output, err := runInstallTestScript(t, script, []string{"--dry-run", "--prefix", prefix}, environment); err == nil {
		t.Fatalf("install dry-run planned replacement of unowned destination:\n%s", output)
	}
	if output, err := runInstallTestScript(t, script, []string{"--prefix", prefix}, environment); err == nil {
		t.Fatalf("install replaced unowned destination:\n%s", output)
	}
	if output, err := runInstallTestScript(t, script, []string{"--uninstall", "--prefix", prefix}, environment); err == nil {
		t.Fatalf("uninstall removed unowned destination:\n%s", output)
	}
	assertInstallTestFile(t, destination, "user file\n")
}

func TestDevxWrapperSelectsOSAndArchitecture(t *testing.T) {
	t.Parallel()

	wrapper := filepath.Join(installTestRepository(t), "cli", "devx")
	tests := []struct {
		kernel  string
		machine string
		binary  string
	}{
		{kernel: "Linux", machine: "x86_64", binary: "devx-linux-amd64"},
		{kernel: "Linux", machine: "aarch64", binary: "devx-linux-arm64"},
		{kernel: "Darwin", machine: "arm64", binary: "devx-darwin-arm64"},
		{kernel: "MINGW64_NT-10.0", machine: "x86_64", binary: "devx-windows-amd64.exe"},
	}
	for _, test := range tests {
		output, err := runInstallTestScript(t, wrapper, []string{"--print-host-binary"}, []string{
			"DEVX_UNAME_S=" + test.kernel, "DEVX_UNAME_M=" + test.machine, "PATH=/usr/bin:/bin",
		})
		if err != nil {
			t.Errorf("wrapper %s/%s error = %v\n%s", test.kernel, test.machine, err, output)
			continue
		}
		if filepath.Base(strings.TrimSpace(output)) != test.binary {
			t.Errorf("wrapper %s/%s output = %q, want %q", test.kernel, test.machine, output, test.binary)
		}
	}
}

func TestDevxWrapperExecutesPrebuiltHostBinary(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	cliDirectory := filepath.Join(repository, "cli")
	binDirectory := filepath.Join(repository, "bin")
	if err := os.MkdirAll(cliDirectory, 0o700); err != nil {
		t.Fatalf("MkdirAll(cli) error = %v", err)
	}
	if err := os.MkdirAll(binDirectory, 0o700); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	wrapperSource, err := os.ReadFile(filepath.Join(installTestRepository(t), "cli", "devx"))
	if err != nil {
		t.Fatalf("ReadFile(wrapper) error = %v", err)
	}
	wrapper := filepath.Join(cliDirectory, "devx")
	writeInstallTestExecutable(t, wrapper, wrapperSource)
	binary := filepath.Join(binDirectory, "devx-linux-amd64")
	writeInstallTestExecutable(t, binary, []byte("#!/bin/sh\nprintf 'cwd:%s\\nprebuilt:%s\\n' \"$PWD\" \"$1\"\n"))
	output, err := runInstallTestScript(t, wrapper, []string{"status"}, []string{
		"DEVX_UNAME_S=Linux", "DEVX_UNAME_M=x86_64", "PATH=/usr/bin:/bin",
	})
	if err != nil {
		t.Fatalf("wrapper prebuilt error = %v\n%s", err, output)
	}
	want := "cwd:" + repository + "\nprebuilt:status\n"
	if output != want {
		t.Fatalf("wrapper prebuilt output = %q", output)
	}
}

func TestDevxWrapperFallbackRunsGoFromRepositoryRoot(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	cliDirectory := filepath.Join(repository, "cli")
	toolsDirectory := filepath.Join(repository, "tools")
	if err := os.MkdirAll(cliDirectory, 0o700); err != nil {
		t.Fatalf("MkdirAll(cli) error = %v", err)
	}
	if err := os.MkdirAll(toolsDirectory, 0o700); err != nil {
		t.Fatalf("MkdirAll(tools) error = %v", err)
	}
	wrapperSource, err := os.ReadFile(filepath.Join(installTestRepository(t), "cli", "devx"))
	if err != nil {
		t.Fatalf("ReadFile(wrapper) error = %v", err)
	}
	wrapper := filepath.Join(cliDirectory, "devx")
	writeInstallTestExecutable(t, wrapper, wrapperSource)
	fakeGo := filepath.Join(toolsDirectory, "go")
	writeInstallTestExecutable(t, fakeGo, []byte("#!/bin/sh\nprintf 'cwd:%s\\nargs:%s\\n' \"$PWD\" \"$*\"\n"))
	output, err := runInstallTestScript(t, wrapper, []string{"status"}, []string{
		"DEVX_UNAME_S=Linux", "DEVX_UNAME_M=x86_64", "PATH=" + toolsDirectory + ":/usr/bin:/bin",
	})
	if err != nil {
		t.Fatalf("wrapper fallback error = %v\n%s", err, output)
	}
	want := "cwd:" + repository + "\nargs:run ./cmd/devx status\n"
	if output != want {
		t.Fatalf("wrapper fallback output = %q, want %q", output, want)
	}
}

func TestUninstallCommandRunsPackagedInstallerWithExactArguments(t *testing.T) {
	t.Parallel()

	repository := installTestRepository(t)
	prefix := filepath.Join(t.TempDir(), "prefix")
	runner := &recordingRunner{}
	root := newRoot(dependencies{
		getwd: func() (string, error) { return repository, nil }, readFile: os.ReadFile,
		stat: os.Stat, runner: runner,
	})
	root.SetArgs([]string{"uninstall", "--dry-run", "--prefix", prefix})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute(uninstall) error = %v", err)
	}
	if len(runner.runs) != 1 {
		t.Fatalf("runner calls = %d", len(runner.runs))
	}
	run := runner.runs[0]
	wantExecutable := filepath.Join(repository, "cli", "install.sh")
	wantArguments := []string{"--uninstall", "--dry-run", "--prefix", prefix}
	if run.spec.executable != wantExecutable || !slices.Equal(run.spec.arguments, wantArguments) {
		t.Fatalf("uninstall run = %+v, want %s %v", run.spec, wantExecutable, wantArguments)
	}
	if !errors.Is(run.ctx.Err(), context.Canceled) {
		t.Fatalf("uninstall context after command = %v", run.ctx.Err())
	}
}

func installTestRepository(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller did not return the test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "../../.."))
}

func runInstallTestScript(t *testing.T, script string, arguments, environment []string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, script, arguments...)
	command.Dir = installTestRepository(t)
	command.Env = environment
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%s timed out: %v\n%s", script, ctx.Err(), output)
	}
	return string(output), err
}

func assertInstallTestFile(t *testing.T, path, want string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	if string(contents) != want {
		t.Fatalf("%s contents = %q, want %q", path, contents, want)
	}
}

func writeInstallTestExecutable(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("Chmod(%s) error = %v", path, err)
	}
}
