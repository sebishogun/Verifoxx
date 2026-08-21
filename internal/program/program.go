package program

import (
	"bytes"

	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// Program is the frozen, self-contained compiled policy. All instruction,
// value, symbol, semantic, and result data are parallel numeric columns and
// CSR edges owned by the Program; nothing borrows compiler or AST memory.
// InstructionID n indexes the instruction columns at offset n-1; ID zero is
// invalid everywhere.
//
// Layout contract: pointer-bearing slice and table fields precede the fixed
// scalar tail (content hash, policy symbol IDs, symbol count) so the GC can
// stop scanning before the scalars, and hot parallel columns stay together.
type Program struct {
	Opcodes    []Opcode
	Fields     []schema.FieldID
	Values     []schema.ValueID
	ListStarts []uint32
	ListCounts []uint16
	// OperandStarts/OperandCounts are the per-instruction CSR range in
	// Operands for Boolean group and Not instructions.
	OperandStarts []uint32
	OperandCounts []uint16
	// EvidenceKinds/EvidenceStates are the typed operands of Evidence
	// instructions.
	EvidenceKinds  []schema.EvidenceKindID
	EvidenceStates []schema.EvidenceStateID
	RootFlags      []RootFlags
	// TruthSlots/ReasonSlots hold liveness-assigned evaluator scratch IDs.
	// TruthSlots is nonzero for every instruction; a zero ReasonSlot means the
	// row cannot contribute to a retained semantic reason.
	TruthSlots  []schema.SlotID
	ReasonSlots []schema.SlotID
	// InstructionNodes and InstructionSourceStarts/Ends record the canonical
	// first source NodeID and span for each instruction.
	InstructionNodes        []schema.NodeID
	InstructionSourceStarts []uint32
	InstructionSourceEnds   []uint32

	// ListValues is the CSR backing array for In literal lists; Operands is
	// the CSR backing array for instruction operands.
	ListValues []schema.ValueID
	Operands   []schema.InstructionID

	// OpcodeRunOpcodes/Starts/Counts record contiguous compatible topological
	// runs over the instruction columns; runs are in final InstructionID
	// order and cover every instruction exactly once.
	OpcodeRunOpcodes []Opcode
	OpcodeRunStarts  []uint32
	OpcodeRunCounts  []uint32

	// NodeInstructionStarts/Counts/IDs map each source NodeID to its ordered
	// canonical instruction range.
	NodeInstructionStarts []uint32
	NodeInstructionCounts []uint16
	NodeInstructionIDs    []schema.InstructionID

	// SymbolBytes is the frozen symbol slab; SymbolStarts/Lengths are indexed
	// by SymbolID-1. SymbolHashes/SymbolIDs are the power-of-two open-address
	// probe table over the same symbol space; a zero SymbolID marks an empty
	// slot.
	SymbolBytes   []byte
	SymbolStarts  []uint32
	SymbolLengths []uint32
	SymbolHashes  []uint64
	SymbolIDs     []schema.SymbolID

	// Canonical value table: ValueKinds/ValueRefs are parallel by ValueID-1.
	// A symbol ref is a SymbolID; integer, Boolean, and timestamp refs are
	// one-based indices into the packed payload columns.
	ValueKinds      []schema.ValueKind
	ValueRefs       []uint32
	IntegerValues   []int64
	BooleanValues   []uint64
	TimestampValues []int64

	// Copied field schema in FieldID order.
	FieldNames  []schema.SymbolID
	FieldKinds  []schema.ValueKind
	FieldGroups []schema.FieldGroup

	// Translated evidence-kind and evidence-state catalog names in
	// EvidenceKindID/EvidenceStateID order.
	EvidenceKindNames         []schema.SymbolID
	EvidenceStateNames        []schema.SymbolID
	EvidenceKindSourceStarts  []uint32
	EvidenceKindSourceEnds    []uint32
	EvidenceStateSourceStarts []uint32
	EvidenceStateSourceEnds   []uint32

	// Outcome and remediation catalog source spans in OutcomeID/
	// RemediationID order; catalog rows themselves live in the result tables.
	OutcomeSourceStarts     []uint32
	OutcomeSourceEnds       []uint32
	RemediationSourceStarts []uint32
	RemediationSourceEnds   []uint32

	// Requirements in requirement-row order.
	RequirementIDs          []schema.RequirementID
	RequirementRoots        []schema.InstructionID
	RequirementClauseStarts []uint32
	RequirementClauseCounts []uint16
	RequirementClauseIDs    []schema.ClauseID
	RequirementSourceStarts []uint32
	RequirementSourceEnds   []uint32

	// Clauses in ClauseID order. RuleSetID == ClauseID for the resolution
	// table. ClauseEvidenceStarts/Counts range over ClauseEvidenceIDs;
	// ClauseRemediationStarts/Counts range over ClauseRemediationIDs.
	ClauseAssertionRoots    []schema.InstructionID
	ClauseEvidenceStarts    []uint32
	ClauseEvidenceCounts    []uint16
	ClauseEvidenceIDs       []schema.InstructionID
	ClauseOnSatisfied       []schema.OutcomeID
	ClauseOnFalse           []schema.OutcomeID
	ClauseRemediationStarts []uint32
	ClauseRemediationCounts []uint16
	ClauseRemediationIDs    []schema.RemediationID
	ClauseSourceStarts      []uint32
	ClauseSourceEnds        []uint32

	// Result tables borrow the program-owned slices above; the Resolver is
	// validated once at lowering time.
	Outcomes     result.OutcomeTable
	Remediations result.RemediationTable
	Resolutions  result.ResolutionTable
	resolver     result.Resolver

	// Retained source bytes of the policy document.
	InputBytes []byte

	// Fixed scalar tail. ContentHash is the SHA-256 of the retained source;
	// PolicyName/PolicyVersion are canonical symbol IDs; ProgramSymbolCount is
	// the number of frozen program symbols; slot counts size evaluator scratch.
	ContentHash        [32]byte
	PolicyName         schema.SymbolID
	PolicyVersion      schema.SymbolID
	ProgramSymbolCount uint32
	TruthSlotCount     uint32
	ReasonSlotCount    uint32
}

// InstructionCount returns the number of compiled instructions.
func (p *Program) InstructionCount() int {
	return len(p.Opcodes)
}

// ClearResultResolver invalidates construction-time Resolver state before the
// owning result columns are rebuilt.
func (p *Program) ClearResultResolver() {
	p.resolver = result.Resolver{}
}

// ValidateResultTables validates the Program-owned result columns and stores
// the resulting immutable resolver. It is used while constructing a Program,
// before publication.
func (p *Program) ValidateResultTables() error {
	p.ClearResultResolver()
	resolver, err := result.NewResolver(p.Outcomes, p.Remediations, p.Resolutions)
	if err != nil {
		return err
	}
	p.resolver = resolver
	return nil
}

// ResultResolver returns a read-only copy of the validated resolver over this
// Program's immutable result tables.
func (p *Program) ResultResolver() result.Resolver {
	if p == nil {
		return result.Resolver{}
	}
	return p.resolver
}

// Symbol returns the frozen bytes for id, or ok=false for the invalid zero
// ID and any out-of-range or malformed range. The returned slice is a
// read-only view into the program's symbol slab.
func (p *Program) Symbol(id schema.SymbolID) ([]byte, bool) {
	if id == 0 {
		return nil, false
	}
	i := uint64(id - 1)
	if i >= uint64(len(p.SymbolStarts)) || i >= uint64(len(p.SymbolLengths)) {
		return nil, false
	}
	start := uint64(p.SymbolStarts[i])
	length := uint64(p.SymbolLengths[i])
	if start+length > uint64(len(p.SymbolBytes)) {
		return nil, false
	}
	return p.SymbolBytes[int(start):int(start+length)], true
}

// LookupSymbol returns the frozen SymbolID for value by hashing the bytes
// through the shared schema.HashSymbol primitive and probing the
// power-of-two open-address table. It verifies the stored hash and the exact
// slab bytes on every probe, never converts bytes to a string, and never
// allocates. Malformed or mismatched slot columns are rejected safely, and
// the probe is bounded so a full table cannot hang.
func (p *Program) LookupSymbol(value []byte) (schema.SymbolID, bool) {
	hashes := p.SymbolHashes
	ids := p.SymbolIDs
	n := len(hashes)
	if n == 0 || n != len(ids) || n&(n-1) != 0 {
		return 0, false
	}
	mask := uint64(n - 1)
	h := schema.HashSymbol(value)
	slot := int(h & mask)
	for probes := 0; probes < n; probes++ {
		id := ids[slot]
		if id == 0 {
			return 0, false
		}
		if hashes[slot] == h {
			if b, ok := p.Symbol(id); ok && bytes.Equal(b, value) {
				return id, true
			}
		}
		slot = (slot + 1) & int(mask)
	}
	return 0, false
}

// ValueKind returns the kind of canonical value id, or ok=false for the
// invalid zero ID and any ID outside the parallel value columns.
func (p *Program) ValueKind(id schema.ValueID) (schema.ValueKind, bool) {
	if id == 0 {
		return schema.ValueKindInvalid, false
	}
	i := uint64(id - 1)
	if i >= uint64(len(p.ValueKinds)) || i >= uint64(len(p.ValueRefs)) {
		return schema.ValueKindInvalid, false
	}
	return p.ValueKinds[i], true
}
