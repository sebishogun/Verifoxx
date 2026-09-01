//go:build wasm

package main

import (
	"math"
	"unsafe"

	wasmruntime "github.com/sebishogun/nornrune/internal/target/wasm"
)

const moduleMemoryLimit = 64 << 20

var (
	moduleArena   []byte
	moduleRuntime = mustModuleRuntime()
)

func mustModuleRuntime() *wasmruntime.Runtime {
	runtime, err := wasmruntime.NewRuntime(wasmruntime.Manifest{
		ABI: wasmruntime.CurrentABIVersion, Schema: wasmruntime.CurrentSchemaVersion,
		Profile: wasmruntime.ProfileWASI,
		Limits: wasmruntime.Limits{
			MaxArtifactBytes: 16 << 20, MaxInputBytes: 16 << 20, MaxOutputBytes: moduleMemoryLimit,
			MaxFuel: math.MaxUint32, MaxRows: 1 << 16, MaxProgramColumns: 256,
		},
	})
	if err != nil {
		panic("nornrune-wasm: invalid static manifest")
	}
	return runtime
}

//go:wasmexport nornrune_abi_version
func wasmABIVersion() uint32 { return uint32(wasmruntime.CurrentABIVersion) }

//go:wasmexport nornrune_schema_version
func wasmSchemaVersion() uint32 { return uint32(wasmruntime.CurrentSchemaVersion) }

//go:wasmexport nornrune_alloc
func wasmAllocate(length uint32) (pointer uint32) {
	defer func() {
		if recover() != nil {
			pointer = 0
		}
	}()
	if length == 0 || length > moduleMemoryLimit {
		return 0
	}
	if cap(moduleArena) < int(length) {
		moduleArena = make([]byte, int(length))
	} else {
		moduleArena = moduleArena[:int(length)]
		clear(moduleArena)
	}
	return uint32(uintptr(unsafe.Pointer(unsafe.SliceData(moduleArena))))
}

//go:wasmexport nornrune_load_program
func wasmLoadProgram(pointer *byte, length uint32) int32 {
	data, ok := moduleBytes(pointer, length)
	if !ok {
		return int32(wasmruntime.ErrorInvalidArgument)
	}
	return int32(moduleRuntime.LoadProgram(data))
}

//go:wasmexport nornrune_upload_input
func wasmUploadInput(pointer *byte, length uint32) int32 {
	data, ok := moduleBytes(pointer, length)
	if !ok {
		return int32(wasmruntime.ErrorInvalidArgument)
	}
	return int32(moduleRuntime.UploadInput(data))
}

//go:wasmexport nornrune_evaluate
func wasmEvaluate() int32 { return int32(moduleRuntime.Evaluate()) }

//go:wasmexport nornrune_result_length
func wasmResultLength() uint32 { return moduleRuntime.ResultLength() }

//go:wasmexport nornrune_read_result
func wasmReadResult(pointer *byte, length uint32) int32 {
	destination, ok := moduleBytes(pointer, length)
	if !ok {
		return int32(wasmruntime.ErrorInvalidArgument)
	}
	_, code := moduleRuntime.ReadResult(destination)
	return int32(code)
}

//go:wasmexport nornrune_reset
func wasmReset() int32 { return int32(moduleRuntime.Reset()) }

//go:wasmexport nornrune_cancel
func wasmCancel() { moduleRuntime.Cancel() }

//go:wasmexport nornrune_set_fuel
func wasmSetFuel(fuel uint64) int32 { return int32(moduleRuntime.SetFuel(fuel)) }

//go:wasmexport nornrune_last_error_length
func wasmLastErrorLength() uint32 { return uint32(len(moduleRuntime.LastError())) }

//go:wasmexport nornrune_read_last_error
func wasmReadLastError(pointer *byte, length uint32) int32 {
	destination, ok := moduleBytes(pointer, length)
	if !ok {
		return int32(wasmruntime.ErrorInvalidArgument)
	}
	message := moduleRuntime.LastError()
	if len(destination) < len(message) {
		return int32(wasmruntime.ErrorOutputTooSmall)
	}
	copy(destination, message)
	return int32(wasmruntime.ErrorNone)
}

func moduleBytes(pointer *byte, length uint32) ([]byte, bool) {
	if length == 0 {
		return nil, pointer == nil
	}
	if pointer == nil || uint64(length) > uint64(len(moduleArena)) || len(moduleArena) == 0 {
		return nil, false
	}
	base := uintptr(unsafe.Pointer(unsafe.SliceData(moduleArena)))
	start := uintptr(unsafe.Pointer(pointer))
	end := start + uintptr(length)
	limit := base + uintptr(len(moduleArena))
	if start < base || end < start || end > limit {
		return nil, false
	}
	return unsafe.Slice(pointer, int(length)), true
}
