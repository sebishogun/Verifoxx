//go:build wasm

package main

//go:wasmexport nornrune_abi_version
func wasmABIVersion() uint32 { return moduleABIVersion() }

//go:wasmexport nornrune_schema_version
func wasmSchemaVersion() uint32 { return moduleSchemaVersion() }

//go:wasmexport nornrune_metadata_length
func wasmMetadataLength() uint32 { return moduleMetadataLength() }

//go:wasmexport nornrune_read_metadata
func wasmReadMetadata(pointer *byte, length uint32) int32 {
	return moduleReadMetadata(pointer, length)
}

//go:wasmexport nornrune_alloc
func wasmAllocate(length uint32) uint32 { return moduleAllocate(length) }

//go:wasmexport nornrune_load_program
func wasmLoadProgram(pointer *byte, length uint32) int32 {
	return moduleLoadProgram(pointer, length)
}

//go:wasmexport nornrune_upload_input
func wasmUploadInput(pointer *byte, length uint32) int32 {
	return moduleUploadInput(pointer, length)
}

//go:wasmexport nornrune_evaluate
func wasmEvaluate() int32 { return moduleEvaluate() }

//go:wasmexport nornrune_result_length
func wasmResultLength() uint32 { return moduleResultLength() }

//go:wasmexport nornrune_read_result
func wasmReadResult(pointer *byte, length uint32) int32 {
	return moduleReadResult(pointer, length)
}

//go:wasmexport nornrune_reset
func wasmReset() int32 { return moduleReset() }

//go:wasmexport nornrune_cancel
func wasmCancel() int32 { return moduleCancel() }

//go:wasmexport nornrune_set_fuel
func wasmSetFuel(fuel uint64) int32 { return moduleSetFuel(fuel) }

//go:wasmexport nornrune_last_error_length
func wasmLastErrorLength() uint32 { return moduleLastErrorLength() }

//go:wasmexport nornrune_read_last_error
func wasmReadLastError(pointer *byte, length uint32) int32 {
	return moduleReadLastError(pointer, length)
}
