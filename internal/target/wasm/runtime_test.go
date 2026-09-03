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
	if metadata.ProgramHash != compiled.ContentHash || metadata.ArtifactHash == [32]byte{} || metadata.Limits != manifest.Limits {
		t.Fatalf("Metadata = %+v", metadata)
	}
	if code := runtime.UploadInput(inputFrame); code != ErrorNone {
		t.Fatalf("UploadInput = %v: %s", code, runtime.LastError())
	}

	cost := wasmTestFuelCost(t, compiled, input)
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

func TestRuntimeFuelChargesEvidenceAndResultWork(t *testing.T) {
	compiled := compileWASMTestProgram(t)
	manifest := testManifest()
	artifact, err := EncodeProgram(nil, compiled, manifest)
	if err != nil {
		t.Fatal(err)
	}
	input := buildWASMTestBatch(t, compiled)
	input.EvidenceOffsets = []uint32{0, 1}
	input.EvidenceRefs = []uint32{0}
	input.Evidence = eval.EvidenceBatch{
		IDs: []schema.EvidenceID{1}, Kinds: []schema.EvidenceKindID{1}, States: []schema.EvidenceStateID{1},
		Subjects: []schema.SymbolID{0}, Scopes: []schema.SymbolID{0}, Reviewers: []schema.SymbolID{0},
		Timings: []schema.SymbolID{0}, Timestamps: []int64{0},
	}
	frame, err := EncodeInputFrame(nil, input, manifest.Limits)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.LoadProgram(artifact) != ErrorNone || runtime.UploadInput(frame) != ErrorNone {
		t.Fatal(runtime.LastError())
	}
	instructionOnlyFuel := uint64(input.Rows) * uint64(compiled.InstructionCount())
	if runtime.SetFuel(instructionOnlyFuel) != ErrorNone {
		t.Fatal(runtime.LastError())
	}
	if code := runtime.Evaluate(); code != ErrorFuelExhausted {
		t.Fatalf("Evaluate() with instruction-only fuel = %v, want %v", code, ErrorFuelExhausted)
	}
	if runtime.SetFuel(wasmTestFuelCost(t, compiled, input)) != ErrorNone {
		t.Fatal(runtime.LastError())
	}
	if code := runtime.Evaluate(); code != ErrorNone {
		t.Fatalf("Evaluate() with complete fuel = %v: %s", code, runtime.LastError())
	}
}

func TestEvaluationFuelChargesInListItemsPerRow(t *testing.T) {
	input := eval.Batch{Rows: 3}
	small := &program.Program{Opcodes: []program.Opcode{program.OpcodeIn}, ListCounts: []uint16{1}}
	large := &program.Program{Opcodes: []program.Opcode{program.OpcodeIn}, ListCounts: []uint16{5}}
	smallCost := wasmTestFuelCost(t, small, input)
	largeCost := wasmTestFuelCost(t, large, input)
	if want := smallCost + 12; largeCost != want {
		t.Fatalf("five-item IN fuel = %d, want %d", largeCost, want)
	}
}

func TestEvaluationFuelChargesBooleanOperandEdgesPerRow(t *testing.T) {
	small := &program.Program{Opcodes: []program.Opcode{program.OpcodeAll}, OperandCounts: []uint16{2}}
	large := &program.Program{Opcodes: []program.Opcode{program.OpcodeAll}, OperandCounts: []uint16{5}}
	for _, test := range []struct {
		name  string
		rows  uint32
		delta uint64
	}{
		{name: "rows", rows: 3, delta: 9},
		{name: "empty batch", rows: 0, delta: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			smallCost := wasmTestFuelCost(t, small, eval.Batch{Rows: test.rows})
			largeCost := wasmTestFuelCost(t, large, eval.Batch{Rows: test.rows})
			if want := smallCost + test.delta; largeCost != want {
				t.Fatalf("five-operand All fuel = %d, want %d", largeCost, want)
			}
		})
	}
}

func TestRuntimeRecoveredPanicClearsEvaluationState(t *testing.T) {
	compiled := compileWASMTestProgram(t)
	manifest := testManifest()
	artifact, err := EncodeProgram(nil, compiled, manifest)
	if err != nil {
		t.Fatal(err)
	}
	input := buildWASMTestBatch(t, compiled)
	frame, err := EncodeInputFrame(nil, input, manifest.Limits)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(manifest)
	if err != nil {
		t.Fatal(err)
	}
	cost := wasmTestFuelCost(t, compiled, input)
	if runtime.LoadProgram(artifact) != ErrorNone || runtime.UploadInput(frame) != ErrorNone || runtime.SetFuel(cost) != ErrorNone || runtime.Evaluate() != ErrorNone {
		t.Fatal(runtime.LastError())
	}
	if runtime.UploadInput(frame) != ErrorNone || runtime.SetFuel(cost) != ErrorNone {
		t.Fatal(runtime.LastError())
	}
	runtime.program.TruthSlots[0] = 0
	if code := runtime.Evaluate(); code != ErrorInternal {
		t.Fatalf("Evaluate() after corruption = %v, want %v", code, ErrorInternal)
	}
	if runtime.hasInput || runtime.hasResult || runtime.fuel != 0 || runtime.cancelled.Load() || runtime.ResultLength() != 0 {
		t.Fatalf("recovered panic retained evaluation state: input=%v result=%v fuel=%d cancelled=%v bytes=%d",
			runtime.hasInput, runtime.hasResult, runtime.fuel, runtime.cancelled.Load(), runtime.ResultLength())
	}
	if code := runtime.Evaluate(); code != ErrorInvalidState {
		t.Fatalf("Evaluate() after recovered panic = %v, want %v", code, ErrorInvalidState)
	}
}

func TestRuntimeMetadataRecordIncludesLimitsAndLoadedHashes(t *testing.T) {
	manifest := testManifest()
	compiled := compileWASMTestProgram(t)
	artifact, err := EncodeProgram(nil, compiled, manifest)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(manifest)
	if err != nil {
		t.Fatal(err)
	}
	record := make([]byte, MetadataBytes)
	written, code := runtime.ReadMetadata(record)
	if code != ErrorNone || written != MetadataBytes {
		t.Fatalf("ReadMetadata before load = %d/%v", written, code)
	}
	metadata, err := DecodeMetadata(record)
	if err != nil || metadata.Limits != manifest.Limits || metadata.Profile != manifest.Profile || metadata.ProgramHash != [32]byte{} {
		t.Fatalf("metadata before load = %+v, %v", metadata, err)
	}
	if code := runtime.LoadProgram(artifact); code != ErrorNone {
		t.Fatalf("LoadProgram = %v: %s", code, runtime.LastError())
	}
	if _, code := runtime.ReadMetadata(record); code != ErrorNone {
		t.Fatalf("ReadMetadata after load = %v", code)
	}
	metadata, err = DecodeMetadata(record)
	if err != nil || metadata.ProgramHash != compiled.ContentHash || metadata.ArtifactHash == [32]byte{} || metadata.Limits != manifest.Limits {
		t.Fatalf("metadata after load = %+v, %v", metadata, err)
	}
	if _, code := runtime.ReadMetadata(record[:MetadataBytes-1]); code != ErrorOutputTooSmall {
		t.Fatalf("short metadata buffer = %v", code)
	}
}

func TestRuntimeRejectsArtifactFromDifferentBaseProfile(t *testing.T) {
	browserManifest := testManifest()
	browserManifest.Profile = ProfileBrowser
	artifact, err := EncodeProgram(nil, compileWASMTestProgram(t), browserManifest)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(testManifest())
	if err != nil {
		t.Fatal(err)
	}
	if code := runtime.LoadProgram(artifact); code != ErrorIncompatibleVersion {
		t.Fatalf("WASI runtime loaded Browser artifact: %v", code)
	}
}

func TestRuntimeRejectsArtifactWithDifferentLimits(t *testing.T) {
	artifactManifest := testManifest()
	artifact, err := EncodeProgram(nil, compileWASMTestProgram(t), artifactManifest)
	if err != nil {
		t.Fatal(err)
	}
	hostManifest := artifactManifest
	hostManifest.Limits.MaxInputBytes++
	runtime, err := NewRuntime(hostManifest)
	if err != nil {
		t.Fatal(err)
	}
	if code := runtime.LoadProgram(artifact); code != ErrorIncompatibleVersion {
		t.Fatalf("LoadProgram limit mismatch = %v, want %v", code, ErrorIncompatibleVersion)
	}
	if runtime.program != nil || runtime.Metadata().ProgramHash != [32]byte{} {
		t.Fatal("limit mismatch published a Program")
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

func wasmTestFuelCost(t testing.TB, compiled *program.Program, input eval.Batch) uint64 {
	t.Helper()
	cost, ok := evaluationFuelCost(newEvaluationFuelProfile(compiled), input)
	if !ok {
		t.Fatal("evaluation fuel cost overflow")
	}
	return cost
}
