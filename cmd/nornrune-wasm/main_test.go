package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCommandBuildsWASIReactorWithVersionedExports(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	module := filepath.Join(t.TempDir(), "nornrune.wasm")
	command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-buildvcs=false", "-buildmode=c-shared", "-o", module, ".")
	command.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build reactor: %v\n%s", err, output)
	}
	if ctx.Err() != nil {
		t.Fatal(ctx.Err())
	}
	contents, err := os.ReadFile(module)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"nornrune_abi_version", "nornrune_schema_version", "nornrune_alloc",
		"nornrune_load_program", "nornrune_upload_input", "nornrune_evaluate",
		"nornrune_result_length", "nornrune_read_result", "nornrune_reset",
		"nornrune_cancel", "nornrune_set_fuel", "nornrune_last_error_length",
		"nornrune_read_last_error",
	} {
		if !bytes.Contains(contents, []byte(name)) {
			t.Fatalf("reactor does not export %q", name)
		}
	}
}
