package wasm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/result"
)

func TestConformanceNodeMatchesNativeResultFrame(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	module := buildWASMTestModule(t, ctx)
	compiled := compileWASMTestProgram(t)
	manifest := testManifest()
	artifact, err := EncodeProgram(nil, compiled, manifest)
	if err != nil {
		t.Fatal(err)
	}
	input := buildWASMTestBatchRows(t, compiled, 129)
	inputFrame, err := EncodeInputFrame(nil, input, manifest.Limits)
	if err != nil {
		t.Fatal(err)
	}
	var executor eval.Executor
	var output result.Batch
	if err := executor.Execute(&output, compiled, input); err != nil {
		t.Fatal(err)
	}
	want, err := EncodeResultFrame(nil, output, manifest.Limits)
	if err != nil {
		t.Fatal(err)
	}
	mismatchManifest := manifest
	mismatchManifest.Limits.MaxInputBytes--
	mismatch, err := EncodeProgram(nil, compiled, mismatchManifest)
	if err != nil {
		t.Fatal(err)
	}

	directory := t.TempDir()
	paths := []string{
		filepath.Join(directory, "nornrune.wasm"),
		filepath.Join(directory, "program.bin"),
		filepath.Join(directory, "input.bin"),
		filepath.Join(directory, "result.bin"),
		filepath.Join(directory, "mismatched-program.bin"),
	}
	for row, contents := range [][]byte{module, artifact, inputFrame, want, mismatch} {
		if err := os.WriteFile(paths[row], contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	repository, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, scriptName := range []string{"conformance.mjs", "browser-conformance.mjs"} {
		t.Run(scriptName, func(t *testing.T) {
			script := filepath.Join(repository, "testdata", "wasm", scriptName)
			command := exec.CommandContext(ctx, node, "--no-warnings", script, paths[0], paths[1], paths[2], paths[3], paths[4])
			command.Dir = repository
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("node conformance: %v\n%s", err, output)
			}
		})
	}
}
