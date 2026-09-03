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

func moduleABIVersion() (value uint32) {
	defer recoverModuleUint32(&value)
	return uint32(wasmruntime.CurrentABIVersion)
}

func moduleSchemaVersion() (value uint32) {
	defer recoverModuleUint32(&value)
	return uint32(wasmruntime.CurrentSchemaVersion)
}

func moduleMetadataLength() (value uint32) {
	defer recoverModuleUint32(&value)
	return wasmruntime.MetadataBytes
}

func moduleReadMetadata(pointer *byte, length uint32) (code int32) {
	defer recoverModuleCode(&code)
	destination, ok := moduleBytes(pointer, length)
	if !ok {
		return int32(wasmruntime.ErrorInvalidArgument)
	}
	_, result := moduleRuntime.ReadMetadata(destination)
	return int32(result)
}

func moduleAllocate(length uint32) (pointer uint32) {
	defer recoverModuleUint32(&pointer)
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

func moduleLoadProgram(pointer *byte, length uint32) (code int32) {
	defer recoverModuleCode(&code)
	data, ok := moduleBytes(pointer, length)
	if !ok {
		return int32(wasmruntime.ErrorInvalidArgument)
	}
	return int32(moduleRuntime.LoadProgram(data))
}

func moduleUploadInput(pointer *byte, length uint32) (code int32) {
	defer recoverModuleCode(&code)
	data, ok := moduleBytes(pointer, length)
	if !ok {
		return int32(wasmruntime.ErrorInvalidArgument)
	}
	return int32(moduleRuntime.UploadInput(data))
}

func moduleEvaluate() (code int32) {
	defer recoverModuleCode(&code)
	return int32(moduleRuntime.Evaluate())
}

func moduleResultLength() (length uint32) {
	defer recoverModuleUint32(&length)
	return moduleRuntime.ResultLength()
}

func moduleReadResult(pointer *byte, length uint32) (code int32) {
	defer recoverModuleCode(&code)
	destination, ok := moduleBytes(pointer, length)
	if !ok {
		return int32(wasmruntime.ErrorInvalidArgument)
	}
	_, result := moduleRuntime.ReadResult(destination)
	return int32(result)
}

func moduleReset() (code int32) {
	defer recoverModuleCode(&code)
	return int32(moduleRuntime.Reset())
}

func moduleCancel() (code int32) {
	defer recoverModuleCode(&code)
	moduleRuntime.Cancel()
	return int32(wasmruntime.ErrorNone)
}

func moduleSetFuel(fuel uint64) (code int32) {
	defer recoverModuleCode(&code)
	return int32(moduleRuntime.SetFuel(fuel))
}

func moduleLastErrorLength() (length uint32) {
	defer recoverModuleUint32(&length)
	return uint32(len(moduleRuntime.LastError()))
}

func moduleReadLastError(pointer *byte, length uint32) (code int32) {
	defer recoverModuleCode(&code)
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

func recoverModuleCode(code *int32) {
	if recover() == nil {
		return
	}
	result := wasmruntime.ErrorInternal
	if moduleRuntime != nil {
		result = moduleRuntime.RecordTrap()
	}
	*code = int32(result)
}

func recoverModuleUint32(value *uint32) {
	if recover() == nil {
		return
	}
	*value = 0
	if moduleRuntime != nil {
		moduleRuntime.RecordTrap()
	}
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
