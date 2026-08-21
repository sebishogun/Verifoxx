// Package ast provides the pointerless source representation of a policy.
package ast

// NodeKind selects the typed payload table referenced by Document.NodeRefs.
type NodeKind uint8

const (
	NodeKindInvalid NodeKind = iota
	NodeKindCompare
	NodeKindAll
	NodeKindAny
	NodeKindNot
	NodeKindEvidence
)

// Valid reports whether k identifies a supported AST node kind.
func (k NodeKind) Valid() bool {
	return k >= NodeKindCompare && k <= NodeKindEvidence
}

func (k NodeKind) group() bool {
	return k == NodeKindAll || k == NodeKindAny
}

// CompareOp is a bounded comparison operation.
type CompareOp uint8

const (
	CompareOpInvalid CompareOp = iota
	CompareOpEqual
	CompareOpNotEqual
	CompareOpIn
	CompareOpExists
	CompareOpLess
	CompareOpLessEqual
	CompareOpGreater
	CompareOpGreaterEqual
)

// Valid reports whether op is supported by the policy language.
func (op CompareOp) Valid() bool {
	return op >= CompareOpEqual && op <= CompareOpGreaterEqual
}

// RequiresValue reports whether op requires a nonzero literal ValueID.
func (op CompareOp) RequiresValue() bool {
	return op.Valid() && op != CompareOpExists
}
