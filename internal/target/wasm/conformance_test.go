package wasm

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

func TestConformanceWazeroMatchesNativeResultFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	moduleBytes := buildWASMTestModule(t, ctx)
	engine := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(2048))
	t.Cleanup(func() { _ = engine.Close(ctx) })
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, engine); err != nil {
		t.Fatal(err)
	}
	compiledModule, err := engine.CompileModule(ctx, moduleBytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range compiledModule.ImportedFunctions() {
		moduleName, name, imported := definition.Import()
		if !imported || moduleName != "wasi_snapshot_preview1" || forbiddenWASIImport(name) {
			t.Fatalf("unexpected host import %q.%q", moduleName, name)
		}
	}
	if len(compiledModule.ImportedMemories()) != 0 {
		t.Fatal("reactor imports host memory")
	}
	module, err := engine.InstantiateModule(ctx, compiledModule, wazero.NewModuleConfig().WithName("nornrune"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Close(ctx) })
	initializer := module.ExportedFunction("_initialize")
	if initializer == nil {
		t.Fatal("reactor does not export _initialize")
	}
	if _, err := initializer.Call(ctx); err != nil {
		t.Fatalf("initialize reactor: %v", err)
	}
	assertScalarExport(t, ctx, module, "nornrune_abi_version", uint64(CurrentABIVersion))
	assertScalarExport(t, ctx, module, "nornrune_schema_version", uint64(CurrentSchemaVersion))
	assertScalarExport(t, ctx, module, "nornrune_metadata_length", MetadataBytes)
	values, err := module.ExportedFunction("nornrune_load_program").Call(ctx, 1, 16)
	if err != nil || len(values) != 1 || ErrorCode(values[0]) != ErrorInvalidArgument {
		t.Fatalf("invalid host pointer = %v/%v", values, err)
	}

	compiled := compileWASMTestProgram(t)
	manifest := testManifest()
	moduleMetadata := readModuleMetadata(t, ctx, module)
	if moduleMetadata.Limits != manifest.Limits || moduleMetadata.Profile != manifest.Profile {
		t.Fatalf("module manifest = %+v, fixture manifest = %+v", moduleMetadata, manifest)
	}
	mismatchManifest := manifest
	mismatchManifest.Limits.MaxInputBytes--
	mismatch, err := EncodeProgram(nil, compiled, mismatchManifest)
	if err != nil {
		t.Fatal(err)
	}
	callWithBytes(t, ctx, module, "nornrune_load_program", mismatch, ErrorIncompatibleVersion)
	artifact, err := EncodeProgram(nil, compiled, manifest)
	if err != nil {
		t.Fatal(err)
	}
	callWithBytes(t, ctx, module, "nornrune_load_program", artifact, ErrorNone)
	metadata := readModuleMetadata(t, ctx, module)
	if metadata.ABI != CurrentABIVersion || metadata.Schema != CurrentSchemaVersion || metadata.Profile != ProfileWASI ||
		metadata.ProgramHash != compiled.ContentHash || metadata.ArtifactHash == [32]byte{} || metadata.Limits.Validate() != nil {
		t.Fatalf("module metadata = %+v", metadata)
	}
	input := buildWASMTestBatchRows(t, compiled, 129)
	inputFrame, err := EncodeInputFrame(nil, input, manifest.Limits)
	if err != nil {
		t.Fatal(err)
	}
	callWithBytes(t, ctx, module, "nornrune_upload_input", inputFrame, ErrorNone)
	cost := wasmTestFuelCost(t, compiled, input)
	callCode(t, ctx, module, "nornrune_set_fuel", ErrorNone, cost-1)
	callCode(t, ctx, module, "nornrune_evaluate", ErrorFuelExhausted)
	assertLastError(t, ctx, module)
	callCode(t, ctx, module, "nornrune_set_fuel", ErrorNone, cost)
	if _, err := module.ExportedFunction("nornrune_cancel").Call(ctx); err != nil {
		t.Fatal(err)
	}
	callCode(t, ctx, module, "nornrune_evaluate", ErrorCancelled)
	callCode(t, ctx, module, "nornrune_reset", ErrorNone)
	callWithBytes(t, ctx, module, "nornrune_upload_input", inputFrame, ErrorNone)
	callCode(t, ctx, module, "nornrune_set_fuel", ErrorNone, cost)
	callCode(t, ctx, module, "nornrune_evaluate", ErrorNone)

	lengthResult, err := module.ExportedFunction("nornrune_result_length").Call(ctx)
	if err != nil || len(lengthResult) != 1 || lengthResult[0] == 0 {
		t.Fatalf("result length = %v, %v", lengthResult, err)
	}
	got := readModuleResult(t, ctx, module, uint32(lengthResult[0]))
	var executor eval.Executor
	var native result.Batch
	if err := executor.Execute(&native, compiled, input); err != nil {
		t.Fatal(err)
	}
	want, err := EncodeResultFrame(nil, native, manifest.Limits)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("wazero result differs from native: got %x, want %x", got, want)
	}

	badArtifact := append([]byte(nil), artifact...)
	badArtifact[len(badArtifact)-1] ^= 0xff
	callWithBytes(t, ctx, module, "nornrune_load_program", badArtifact, ErrorInvalidArtifact)
	callCode(t, ctx, module, "nornrune_reset", ErrorNone)
}

func readModuleMetadata(t testing.TB, ctx context.Context, module api.Module) Metadata {
	t.Helper()
	allocation, err := module.ExportedFunction("nornrune_alloc").Call(ctx, MetadataBytes)
	if err != nil || len(allocation) != 1 || allocation[0] == 0 {
		t.Fatalf("allocate metadata: %v, %v", allocation, err)
	}
	pointer := uint32(allocation[0])
	callCode(t, ctx, module, "nornrune_read_metadata", ErrorNone, uint64(pointer), MetadataBytes)
	view, ok := module.Memory().Read(pointer, MetadataBytes)
	if !ok {
		t.Fatal("read metadata memory")
	}
	metadata, err := DecodeMetadata(view)
	if err != nil {
		t.Fatal(err)
	}
	return metadata
}

func forbiddenWASIImport(name string) bool {
	return strings.HasPrefix(name, "sock_")
}

func assertScalarExport(t testing.TB, ctx context.Context, module api.Module, name string, want uint64) {
	t.Helper()
	values, err := module.ExportedFunction(name).Call(ctx)
	if err != nil || len(values) != 1 || values[0] != want {
		t.Fatalf("%s = %v/%v, want %d", name, values, err, want)
	}
}

func assertLastError(t testing.TB, ctx context.Context, module api.Module) {
	t.Helper()
	length, err := module.ExportedFunction("nornrune_last_error_length").Call(ctx)
	if err != nil || len(length) != 1 || length[0] == 0 {
		t.Fatalf("last error length = %v/%v", length, err)
	}
	allocation, err := module.ExportedFunction("nornrune_alloc").Call(ctx, length[0])
	if err != nil || len(allocation) != 1 || allocation[0] == 0 {
		t.Fatalf("allocate last error = %v/%v", allocation, err)
	}
	callCode(t, ctx, module, "nornrune_read_last_error", ErrorNone, allocation[0], length[0])
}

func buildWASMTestModule(t testing.TB, ctx context.Context) []byte {
	t.Helper()
	repository, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "nornrune.wasm")
	command := exec.CommandContext(ctx, filepath.Join(repository, "scripts", "build-wasm.sh"), path)
	command.Dir = repository
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build module: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func callWithBytes(t testing.TB, ctx context.Context, module api.Module, name string, data []byte, want ErrorCode) {
	t.Helper()
	allocation, err := module.ExportedFunction("nornrune_alloc").Call(ctx, uint64(len(data)))
	if err != nil || len(allocation) != 1 || allocation[0] == 0 {
		t.Fatalf("allocate %s: %v, %v", name, allocation, err)
	}
	pointer := uint32(allocation[0])
	if !module.Memory().Write(pointer, data) {
		t.Fatalf("write %s input", name)
	}
	callCode(t, ctx, module, name, want, uint64(pointer), uint64(len(data)))
}

func callCode(t testing.TB, ctx context.Context, module api.Module, name string, want ErrorCode, arguments ...uint64) {
	t.Helper()
	values, err := module.ExportedFunction(name).Call(ctx, arguments...)
	if err != nil || len(values) != 1 || ErrorCode(values[0]) != want {
		t.Fatalf("%s = %v/%v, want %v", name, values, err, want)
	}
}

func readModuleResult(t testing.TB, ctx context.Context, module api.Module, length uint32) []byte {
	t.Helper()
	allocation, err := module.ExportedFunction("nornrune_alloc").Call(ctx, uint64(length))
	if err != nil || len(allocation) != 1 || allocation[0] == 0 {
		t.Fatalf("allocate result: %v, %v", allocation, err)
	}
	pointer := uint32(allocation[0])
	callCode(t, ctx, module, "nornrune_read_result", ErrorNone, uint64(pointer), uint64(length))
	view, ok := module.Memory().Read(pointer, length)
	if !ok {
		t.Fatal("read result memory")
	}
	return append([]byte(nil), view...)
}
