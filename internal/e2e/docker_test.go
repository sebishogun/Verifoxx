//go:build docker

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestReleaseImageEvaluatesEmbeddedInputs(t *testing.T) {
	repository := repositoryRoot(t)
	image := fmt.Sprintf("nornrune:e2e-%d", os.Getpid())
	removeImageOnCleanup(t, image)

	buildContext, cancelBuild := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildContext, "docker", "build", "--tag", image, ".")
	build.Dir = repository
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("docker build error = %v\n%s", err, output)
	}

	runContext, cancelRun := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelRun()
	run := exec.CommandContext(runContext, "docker", "run", "--rm", image)
	got, err := run.Output()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			t.Fatalf("docker run error = %v\n%s", err, exitError.Stderr)
		}
		t.Fatalf("docker run error = %v", err)
	}
	want, err := os.ReadFile(filepath.Join(repository, "testdata", "golden", "requests.json"))
	if err != nil {
		t.Fatalf("read golden results: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("container output differs from golden\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestDebugImageContainsDelveAndDebugSymbols(t *testing.T) {
	repository := repositoryRoot(t)
	image := fmt.Sprintf("nornrune:debug-e2e-%d", os.Getpid())
	removeImageOnCleanup(t, image)

	buildContext, cancelBuild := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildContext, "docker", "build", "--file", "Dockerfile.debug", "--tag", image, ".")
	build.Dir = repository
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("docker build debug image error = %v\n%s", err, output)
	}

	delveContext, cancelDelve := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelDelve()
	delveOutput, err := exec.CommandContext(delveContext, "docker", "run", "--rm", "--entrypoint", "dlv", image, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("run Delve version error = %v\n%s", err, delveOutput)
	}
	if !bytes.Contains(delveOutput, []byte("Version: 1.27.1\n")) {
		t.Fatalf("Delve version output = %q", delveOutput)
	}

	symbolContext, cancelSymbol := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelSymbol()
	symbolOutput, err := exec.CommandContext(symbolContext, "docker", "run", "--rm", "--entrypoint", "/bin/sh", image,
		"-c", "go tool nm /usr/local/bin/nornrune | grep -q ' main.main$'").CombinedOutput()
	if err != nil {
		t.Fatalf("inspect debug symbols error = %v\n%s", err, symbolOutput)
	}
}

func removeImageOnCleanup(t *testing.T, image string) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "docker", "image", "rm", "--force", image).Run()
	})
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}
