//go:build !wasm

package main

func wasmABIVersion() uint32 { return moduleABIVersion() }

func wasmSchemaVersion() uint32 { return moduleSchemaVersion() }

func wasmMetadataLength() uint32 { return moduleMetadataLength() }

func wasmReadMetadata(pointer *byte, length uint32) int32 {
	return moduleReadMetadata(pointer, length)
}

func wasmAllocate(length uint32) uint32 { return moduleAllocate(length) }

func wasmLoadProgram(pointer *byte, length uint32) int32 {
	return moduleLoadProgram(pointer, length)
}

func wasmUploadInput(pointer *byte, length uint32) int32 {
	return moduleUploadInput(pointer, length)
}

func wasmEvaluate() int32 { return moduleEvaluate() }

func wasmResultLength() uint32 { return moduleResultLength() }

func wasmReadResult(pointer *byte, length uint32) int32 {
	return moduleReadResult(pointer, length)
}

func wasmReset() int32 { return moduleReset() }

func wasmCancel() int32 { return moduleCancel() }

func wasmSetFuel(fuel uint64) int32 { return moduleSetFuel(fuel) }

func wasmLastErrorLength() uint32 { return moduleLastErrorLength() }

func wasmReadLastError(pointer *byte, length uint32) int32 {
	return moduleReadLastError(pointer, length)
}
