// Package wasm defines the portable NornRune WebAssembly artifact and host ABI.
package wasm

import internalwasm "github.com/sebishogun/nornrune/internal/target/wasm"

const (
	ArtifactMagic = internalwasm.ArtifactMagic
	FrameMagic    = internalwasm.FrameMagic
)

type ABIVersion = internalwasm.ABIVersion

const CurrentABIVersion = internalwasm.CurrentABIVersion

type SchemaVersion = internalwasm.SchemaVersion

const CurrentSchemaVersion = internalwasm.CurrentSchemaVersion

type Operation = internalwasm.Operation

const (
	OperationInvalid      = internalwasm.OperationInvalid
	OperationMetadata     = internalwasm.OperationMetadata
	OperationAllocate     = internalwasm.OperationAllocate
	OperationLoadProgram  = internalwasm.OperationLoadProgram
	OperationUploadInput  = internalwasm.OperationUploadInput
	OperationEvaluate     = internalwasm.OperationEvaluate
	OperationResultLength = internalwasm.OperationResultLength
	OperationReadResult   = internalwasm.OperationReadResult
	OperationReset        = internalwasm.OperationReset
	OperationCancel       = internalwasm.OperationCancel
	OperationSetFuel      = internalwasm.OperationSetFuel
	OperationLastError    = internalwasm.OperationLastError
)

type ErrorCode = internalwasm.ErrorCode

const (
	ErrorNone                = internalwasm.ErrorNone
	ErrorInvalidArgument     = internalwasm.ErrorInvalidArgument
	ErrorIncompatibleVersion = internalwasm.ErrorIncompatibleVersion
	ErrorLimitExceeded       = internalwasm.ErrorLimitExceeded
	ErrorInvalidArtifact     = internalwasm.ErrorInvalidArtifact
	ErrorInvalidFrame        = internalwasm.ErrorInvalidFrame
	ErrorInvalidState        = internalwasm.ErrorInvalidState
	ErrorCancelled           = internalwasm.ErrorCancelled
	ErrorFuelExhausted       = internalwasm.ErrorFuelExhausted
	ErrorOutputTooSmall      = internalwasm.ErrorOutputTooSmall
	ErrorInternal            = internalwasm.ErrorInternal
)
