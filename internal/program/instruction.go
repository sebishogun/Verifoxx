// Package program defines the immutable compiled policy representation: a
// pointerless struct-of-arrays instruction schedule, canonical symbols and
// values, semantic roots, and validated result tables. A Program is mutable
// only while lowering; after publication every exported slice is read-only.
package program

// Opcode is the compiled instruction kind. The zero value is invalid and
// never indexes an instruction row.
type Opcode uint8

const (
	OpcodeInvalid Opcode = iota
	OpcodeEqual
	OpcodeNotEqual
	OpcodeIn
	OpcodeExists
	OpcodeLess
	OpcodeLessEqual
	OpcodeGreater
	OpcodeGreaterEqual
	OpcodeEvidence
	OpcodeAll
	OpcodeAny
	OpcodeNot
)

// Valid reports whether op is one of the twelve defined opcodes.
func (op Opcode) Valid() bool {
	return op >= OpcodeEqual && op <= OpcodeNot
}

// IsGroup reports whether op is a variadic Boolean group (All or Any).
func (op Opcode) IsGroup() bool {
	return op == OpcodeAll || op == OpcodeAny
}

// RootFlags marks the semantic roles an instruction serves as a root.
// Flags are ORed when structural common-subexpression merging combines source
// nodes with different roles.
type RootFlags uint8

const (
	RootApplicability RootFlags = 1 << iota
	RootAssertion
	RootEvidence
)

// Has reports whether f carries every flag in flag.
func (f RootFlags) Has(flag RootFlags) bool {
	return f&flag == flag
}
