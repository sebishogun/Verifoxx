package wasm

import (
	"bytes"
	"testing"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
)

func TestRuntimeEnforcesStateFuelCancellationAndResultOwnership(t *testing.T) {
	compiled := compileWASMTestProgram(t)
	manifest := testManifest()
	artifact, err := EncodeProgram(nil, compiled, manifest)
	if err != nil {
		t.Fatal(err)
	}
	input := buildWASMTestBatch(t, compiled)
	inputFrame, err := EncodeInputFrame(nil, input, manifest.Limits)
	if err != nil {
		t.Fatal(err)
	}

	runtime, err := NewRuntime(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if code := runtime.UploadInput(inputFrame); code != ErrorInvalidState {
		t.Fatalf("UploadInput before load = %v", code)
	}
	if code := runtime.LoadProgram(artifact); code != ErrorNone {
		t.Fatalf("LoadProgram = %v: %s", code, runtime.LastError())
	}
	metadata := runtime.Metadata()
	if metadata.ProgramHash != compiled.ContentHash || metadata.ArtifactHash == [32]byte{} {
		t.Fatalf("Metadata = %+v", metadata)
	}
	if code := runtime.UploadInput(inputFrame); code != ErrorNone {
		t.Fatalf("UploadInput = %v: %s", code, runtime.LastError())
	}

	cost := uint64(input.Rows) * uint64(compiled.InstructionCount())
	if code := runtime.SetFuel(cost - 1); code != ErrorNone {
		t.Fatal(code)
	}
	if code := runtime.Evaluate(); code != ErrorFuelExhausted {
		t.Fatalf("Evaluate low fuel = %v", code)
	}
	if code := runtime.SetFuel(cost); code != ErrorNone {
		t.Fatal(code)
	}
	runtime.Cancel()
	if code := runtime.Evaluate(); code != ErrorCancelled {
		t.Fatalf("Evaluate cancelled = %v", code)
	}
	if code := runtime.Reset(); code != ErrorNone {
		t.Fatal(code)
	}
	if code := runtime.UploadInput(inputFrame); code != ErrorNone {
		t.Fatal(code)
	}
	if code := runtime.SetFuel(cost); code != ErrorNone {
		t.Fatal(code)
	}
	if code := runtime.Evaluate(); code != ErrorNone {
		t.Fatalf("Evaluate = %v: %s", code, runtime.LastError())
	}

	length := runtime.ResultLength()
	if length == 0 {
		t.Fatal("ResultLength = 0")
	}
	if _, code := runtime.ReadResult(make([]byte, length-1)); code != ErrorOutputTooSmall {
		t.Fatalf("ReadResult short buffer = %v", code)
	}
	encoded := make([]byte, length)
	written, code := runtime.ReadResult(encoded)
	if code != ErrorNone || written != len(encoded) {
		t.Fatalf("ReadResult = %d/%v", written, code)
	}
	var got result.Batch
	if err := DecodeResultFrame(&got, encoded, manifest.Limits); err != nil {
		t.Fatal(err)
	}
	var executor eval.Executor
	var want result.Batch
	if err := executor.Execute(&want, compiled, input); err != nil {
		t.Fatal(err)
	}
	nativeFrame, err := EncodeResultFrame(nil, want, manifest.Limits)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded, nativeFrame) {
		t.Fatalf("module result = %#v, native = %#v", got, want)
	}
	encoded[0] ^= 0xff
	second := make([]byte, length)
	if _, code := runtime.ReadResult(second); code != ErrorNone || second[0] == encoded[0] {
		t.Fatal("ReadResult exposed mutable runtime storage")
	}
}

func TestRuntimeRejectsInvalidManifestAndStaleResults(t *testing.T) {
	manifest := testManifest()
	invalid := manifest
	invalid.ABI++
	if _, err := NewRuntime(invalid); err == nil {
		t.Fatal("NewRuntime accepted incompatible ABI")
	}
	runtime, err := NewRuntime(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.ResultLength() != 0 {
		t.Fatal("new runtime exposed a result")
	}
	if _, code := runtime.ReadResult(nil); code != ErrorInvalidState {
		t.Fatalf("ReadResult before evaluation = %v", code)
	}
	if code := runtime.Reset(); code != ErrorNone {
		t.Fatalf("Reset before load = %v", code)
	}
}

func TestRuntimeFailedLoadAndUploadInvalidatePreviousState(t *testing.T) {
	compiled := compileWASMTestProgram(t)
	manifest := testManifest()
	artifact, err := EncodeProgram(nil, compiled, manifest)
	if err != nil {
		t.Fatal(err)
	}
	input, err := EncodeInputFrame(nil, buildWASMTestBatch(t, compiled), manifest.Limits)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.LoadProgram(artifact) != ErrorNone || runtime.UploadInput(input) != ErrorNone {
		t.Fatal(runtime.LastError())
	}
	badInput := append([]byte(nil), input...)
	badInput[len(badInput)-1] ^= 0xff
	if code := runtime.UploadInput(badInput); code != ErrorInvalidFrame {
		t.Fatalf("UploadInput corrupt = %v", code)
	}
	if code := runtime.SetFuel(manifest.Limits.MaxFuel); code != ErrorNone {
		t.Fatal(code)
	}
	if code := runtime.Evaluate(); code != ErrorInvalidState {
		t.Fatalf("Evaluate after failed upload = %v", code)
	}

	if runtime.UploadInput(input) != ErrorNone {
		t.Fatal(runtime.LastError())
	}
	badArtifact := append([]byte(nil), artifact...)
	badArtifact[len(badArtifact)-1] ^= 0xff
	if code := runtime.LoadProgram(badArtifact); code != ErrorInvalidArtifact {
		t.Fatalf("LoadProgram corrupt = %v", code)
	}
	if runtime.Metadata().ProgramHash != [32]byte{} {
		t.Fatal("failed load retained old Program metadata")
	}
	if code := runtime.UploadInput(input); code != ErrorInvalidState {
		t.Fatalf("UploadInput after failed load = %v", code)
	}
}

func TestRuntimeResetClearsCancellationBeforeProgramLoad(t *testing.T) {
	runtime, err := NewRuntime(testManifest())
	if err != nil {
		t.Fatal(err)
	}
	runtime.Cancel()
	if code := runtime.Reset(); code != ErrorNone {
		t.Fatalf("Reset before load = %v", code)
	}
	if runtime.cancelled.Load() {
		t.Fatal("Reset retained cancellation")
	}
}

func buildWASMTestBatch(t testing.TB, compiled *program.Program) eval.Batch {
	return buildWASMTestBatchRows(t, compiled, 1)
}

func buildWASMTestBatchRows(t testing.TB, compiled *program.Program, rows uint32) eval.Batch {
	t.Helper()
	var builder eval.Builder
	if err := builder.Begin(compiled, rows, 0, 0); err != nil {
		t.Fatal(err)
	}
	values := []string{"tēam-雪", "trusted", "read", "aggregate", "public", "approved-local", "standard"}
	ids := make([]schema.SymbolID, len(values))
	for row, value := range values {
		id, err := builder.InternSymbol([]byte(value))
		if err != nil {
			t.Fatal(err)
		}
		ids[row] = id
	}
	for requestRow := range rows {
		if err := builder.SetRequestID(requestRow, schema.RequestID(requestRow+1)); err != nil {
			t.Fatal(err)
		}
		for fieldRow, id := range ids {
			if err := builder.SetSymbol(requestRow, schema.FieldID(fieldRow+1), id); err != nil {
				t.Fatal(err)
			}
		}
	}
	offsets := make([]uint32, int(rows)+1)
	if err := builder.SetEvidenceCSR(offsets, nil); err != nil {
		t.Fatal(err)
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return batch
}
