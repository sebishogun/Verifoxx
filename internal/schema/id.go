// Package schema defines the strong handle types, bounded value kinds, and
// field schema shared by the policy compiler, evaluator, and adapters.
//
// ID contract: every handle type reserves zero as its invalid value. A zero
// ID never indexes a table and must be rejected by builders. Valid IDs are
// positive and assigned sequentially by the owning builder. The rule is
// uniform across RequirementID, RequestID, EvidenceID, NodeID, FieldID,
// ValueID, SymbolID, OutcomeID, RemediationID, EvidenceKindID,
// EvidenceStateID, ClauseID, InstructionID, SlotID, and ReasonID.
package schema

// RequirementID identifies a requirement in the policy pack's R1..Rn space.
type RequirementID uint32

// RequestID identifies a request in the candidate batch's R1..Rn space.
// Requirement and request IDs may share numeric values but are distinct
// namespaces by type.
type RequestID uint32

// EvidenceID identifies an evidence record.
type EvidenceID uint32

// NodeID identifies a node in the pointerless source AST.
type NodeID uint32

// FieldID identifies a schema field. It indexes the parallel FieldTable
// columns at offset f-1; FieldID 0 is invalid.
type FieldID uint32

// ValueID identifies an interned literal value in the AST and program.
type ValueID uint32

// SymbolID identifies an interned byte string (field name, token, scope).
type SymbolID uint32

// OutcomeID identifies a policy-pack outcome such as Approve or Reject.
type OutcomeID uint32

// RemediationID identifies a structured remediation record.
type RemediationID uint32

// EvidenceKindID identifies the kind of an evidence requirement or record.
type EvidenceKindID uint32

// EvidenceStateID identifies the state of an evidence requirement
// (missing, stale, unclear, and so on).
type EvidenceStateID uint32

// ClauseID identifies a clause in the source policy document.
type ClauseID uint32

// InstructionID identifies a compiled program instruction.
type InstructionID uint32

// SlotID identifies a reusable truth-plane scratch slot.
type SlotID uint32

// ReasonID identifies a reason-mask slot or explanation reason.
type ReasonID uint32
