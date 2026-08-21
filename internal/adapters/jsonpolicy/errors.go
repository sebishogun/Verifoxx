package jsonpolicy

import "fmt"

// ErrorCode is a stable machine-readable classification of one decode
// failure. Existing codes never change meaning; new codes are appended.
type ErrorCode uint8

const (
	// CodeUnknownKey reports an object key not recognized by the policy schema.
	CodeUnknownKey ErrorCode = iota + 1
	// CodeDuplicateKey reports the same object key appearing twice in one object.
	CodeDuplicateKey
	// CodeMissingKey reports a required object key that never appeared.
	CodeMissingKey
	// CodeMalformed reports input that violates the JSON grammar.
	CodeMalformed
	// CodeTruncated reports input that ends in the middle of a token.
	CodeTruncated
	// CodeTrailing reports non-whitespace bytes after the root object.
	CodeTrailing
	// CodeInvalidVersion reports an unsupported schema_version value.
	CodeInvalidVersion
	// CodeInvalidType reports a value whose JSON type does not fit its slot.
	CodeInvalidType
	// CodeInvalidUTF8 reports invalid UTF-8 inside a string literal.
	CodeInvalidUTF8
	// CodeLimit reports input exceeding one of the Limits bounds.
	CodeLimit
	// CodeUnsupported reports a construct this decoder milestone does not handle.
	CodeUnsupported
	// CodeInvalidReference reports a name reference that does not resolve:
	// an unknown field, evidence kind, or evidence state.
	CodeInvalidReference
	// CodeInvalidArity reports an expression whose key set or operand count
	// does not match its operation.
	CodeInvalidArity
	// CodeDuplicateID reports a catalog or outcome name that decodes to the
	// same bytes as an earlier row.
	CodeDuplicateID
)

var errorCodeNames = [...]string{
	"unknown_key", "duplicate_key", "missing_key", "malformed", "truncated",
	"trailing_data", "invalid_version", "invalid_type", "invalid_utf8",
	"limit_exceeded", "unsupported", "invalid_reference", "invalid_arity",
	"duplicate_id",
}

// String returns the stable name of the code, or "invalid" when out of range.
func (c ErrorCode) String() string {
	i := int(c) - 1
	if i < 0 || i >= len(errorCodeNames) {
		return "invalid"
	}
	return errorCodeNames[i]
}

// Error is one decode failure at a byte offset into the source. Offset is the
// position where the problem was detected and always lies within the source
// bounds, except for a source-limit rejection, where it is the limit itself.
type Error struct {
	Code    ErrorCode
	Offset  int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("jsonpolicy: %s at offset %d: %s", e.Code, e.Offset, e.Message)
}
