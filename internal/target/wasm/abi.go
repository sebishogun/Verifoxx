package wasm

const (
	ArtifactMagic uint32 = 0x4e525750
	FrameMagic    uint32 = 0x4e525746
)

type ABIVersion uint16

const CurrentABIVersion ABIVersion = 1

type SchemaVersion uint16

const CurrentSchemaVersion SchemaVersion = 1

type Operation uint8

const (
	OperationInvalid Operation = iota
	OperationMetadata
	OperationAllocate
	OperationLoadProgram
	OperationUploadInput
	OperationEvaluate
	OperationResultLength
	OperationReadResult
	OperationReset
	OperationCancel
	OperationSetFuel
	OperationLastError
)

func (operation Operation) Valid() bool {
	return operation >= OperationMetadata && operation <= OperationLastError
}

type ErrorCode uint8

const (
	ErrorNone ErrorCode = iota
	ErrorInvalidArgument
	ErrorIncompatibleVersion
	ErrorLimitExceeded
	ErrorInvalidArtifact
	ErrorInvalidFrame
	ErrorInvalidState
	ErrorCancelled
	ErrorFuelExhausted
	ErrorOutputTooSmall
	ErrorInternal
)

func (code ErrorCode) Valid() bool { return code <= ErrorInternal }
