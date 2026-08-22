package jsonbatch

import (
	"errors"
	"fmt"
)

// ErrInvalidProgram reports incompatible immutable Program metadata.
var ErrInvalidProgram = errors.New("jsonbatch: invalid program metadata")

// Input identifies which JSON document contains an error.
type Input uint8

const (
	InputInvalid Input = iota
	InputRequests
	InputEvidence
)

func (i Input) String() string {
	switch i {
	case InputRequests:
		return "requests"
	case InputEvidence:
		return "evidence"
	default:
		return "invalid"
	}
}

// ErrorCode is a stable machine-readable decode failure class.
type ErrorCode uint8

const (
	CodeMalformed ErrorCode = iota + 1
	CodeTruncated
	CodeTrailing
	CodeInvalidUTF8
	CodeLimit
	CodeUnknownKey
	CodeDuplicateKey
	CodeMissingKey
	CodeInvalidVersion
	CodeInvalidType
	CodeInvalidReference
	CodeDuplicateID
	CodeDuplicateField
	CodeInvalidID
)

var errorCodeNames = [...]string{
	"malformed", "truncated", "trailing_data", "invalid_utf8",
	"limit_exceeded", "unknown_key", "duplicate_key", "missing_key",
	"invalid_version", "invalid_type", "invalid_reference", "duplicate_id",
	"duplicate_field", "invalid_id",
}

func (c ErrorCode) String() string {
	i := int(c) - 1
	if i < 0 || i >= len(errorCodeNames) {
		return "invalid"
	}
	return errorCodeNames[i]
}

// Error is one positional JSON decode error.
type Error struct {
	Message string
	Input   Input
	Code    ErrorCode
	Offset  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("jsonbatch: %s: %s at offset %d: %s", e.Input, e.Code, e.Offset, e.Message)
}

// Limits bounds one Decode call. Zero disables a bound.
type Limits struct {
	MaxRequestBytes       int
	MaxEvidenceBytes      int
	MaxStringBytes        int
	MaxRequests           uint32
	MaxEvidence           uint32
	MaxEvidenceRefs       uint32
	MaxFactsPerRequest    uint32
	MaxEvidenceAttributes uint32
	MaxDepth              int
}
