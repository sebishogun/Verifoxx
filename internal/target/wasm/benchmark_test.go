//go:build !race

package wasm

import (
	"context"
	"testing"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

func TestRuntimeWarmEvaluateDoesNotAllocate(t *testing.T) {
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
	run := func() {
		if runtime.SetFuel(manifest.Limits.MaxFuel) != ErrorNone || runtime.Evaluate() != ErrorNone {
			panic(runtime.LastError())
		}
	}
	run()
	if allocations := testing.AllocsPerRun(100, run); allocations != 0 {
		t.Fatalf("warm Evaluate allocations = %.2f, want 0", allocations)
	}
}

func BenchmarkWASMWarmRuntimeEvaluate(b *testing.B) {
	compiled := compileWASMTestProgram(b)
	manifest := testManifest()
	artifact, err := EncodeProgram(nil, compiled, manifest)
	if err != nil {
		b.Fatal(err)
	}
	input, err := EncodeInputFrame(nil, buildWASMTestBatch(b, compiled), manifest.Limits)
	if err != nil {
		b.Fatal(err)
	}
	runtime, err := NewRuntime(manifest)
	if err != nil {
		b.Fatal(err)
	}
	if runtime.LoadProgram(artifact) != ErrorNone || runtime.UploadInput(input) != ErrorNone {
		b.Fatal(runtime.LastError())
	}
	runtime.SetFuel(manifest.Limits.MaxFuel)
	runtime.Evaluate()
	b.ReportAllocs()
	b.SetBytes(int64(buildWASMTestBatch(b, compiled).Rows))
	b.ResetTimer()
	for range b.N {
		runtime.SetFuel(manifest.Limits.MaxFuel)
		if runtime.Evaluate() != ErrorNone {
			b.Fatal(runtime.LastError())
		}
	}
}

func BenchmarkWASMProgramExport(b *testing.B) {
	compiled := compileWASMTestProgram(b)
	manifest := testManifest()
	destination, err := EncodeProgram(nil, compiled, manifest)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(destination)))
	b.ResetTimer()
	for range b.N {
		if _, err := EncodeProgram(destination[:0], compiled, manifest); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWASMProgramLoad(b *testing.B) {
	compiled := compileWASMTestProgram(b)
	manifest := testManifest()
	artifact, err := EncodeProgram(nil, compiled, manifest)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(artifact)))
	b.ResetTimer()
	for range b.N {
		if _, _, err := DecodeProgram(artifact, manifest.Limits); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWASMInputFrameDecode(b *testing.B) {
	compiled := compileWASMTestProgram(b)
	manifest := testManifest()
	frame, err := EncodeInputFrame(nil, buildWASMTestBatch(b, compiled), manifest.Limits)
	if err != nil {
		b.Fatal(err)
	}
	var destination eval.Batch
	if err := DecodeInputFrame(&destination, frame, manifest.Limits); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(frame)))
	b.ResetTimer()
	for range b.N {
		if err := DecodeInputFrame(&destination, frame, manifest.Limits); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWASMWazeroWarmEvaluate(b *testing.B) {
	ctx := context.Background()
	moduleBytes := buildWASMTestModule(b, ctx)
	engine := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithMemoryLimitPages(2048))
	b.Cleanup(func() { _ = engine.Close(ctx) })
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, engine); err != nil {
		b.Fatal(err)
	}
	compiledModule, err := engine.CompileModule(ctx, moduleBytes)
	if err != nil {
		b.Fatal(err)
	}
	module, err := engine.InstantiateModule(ctx, compiledModule, wazero.NewModuleConfig())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = module.Close(ctx) })
	if _, err := module.ExportedFunction("_initialize").Call(ctx); err != nil {
		b.Fatal(err)
	}
	compiled := compileWASMTestProgram(b)
	manifest := testManifest()
	artifact, _ := EncodeProgram(nil, compiled, manifest)
	input := buildWASMTestBatch(b, compiled)
	inputFrame, _ := EncodeInputFrame(nil, input, manifest.Limits)
	callWithBytes(b, ctx, module, "nornrune_load_program", artifact, ErrorNone)
	callWithBytes(b, ctx, module, "nornrune_upload_input", inputFrame, ErrorNone)
	cost := uint64(input.Rows) * uint64(compiled.InstructionCount())
	fuel := module.ExportedFunction("nornrune_set_fuel")
	evaluate := module.ExportedFunction("nornrune_evaluate")
	b.ReportAllocs()
	b.SetBytes(int64(input.Rows))
	b.ResetTimer()
	for range b.N {
		if !wasmCallReturns(fuel, ctx, cost, ErrorNone) || !wasmCallReturns(evaluate, ctx, 0, ErrorNone) {
			b.Fatal("wazero evaluation failed")
		}
	}
}

func wasmCallReturns(function api.Function, ctx context.Context, argument uint64, want ErrorCode) bool {
	var (
		values []uint64
		err    error
	)
	if len(function.Definition().ParamTypes()) == 0 {
		values, err = function.Call(ctx)
	} else {
		values, err = function.Call(ctx, argument)
	}
	return err == nil && len(values) == 1 && ErrorCode(values[0]) == want
}
