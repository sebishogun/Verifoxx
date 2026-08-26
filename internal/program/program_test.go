package program

import (
	"reflect"
	"testing"

	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
)

func TestOpcodeInvalidZeroAndAppendOnlyValues(t *testing.T) {
	if OpcodeInvalid != 0 {
		t.Fatalf("OpcodeInvalid = %d, want 0", OpcodeInvalid)
	}
	valid := []Opcode{
		OpcodeEqual, OpcodeNotEqual, OpcodeIn, OpcodeExists,
		OpcodeLess, OpcodeLessEqual, OpcodeGreater, OpcodeGreaterEqual,
		OpcodeEvidence, OpcodeAll, OpcodeAny, OpcodeNot, OpcodeBoolean, OpcodeDefined,
	}
	for _, op := range valid {
		if !op.Valid() {
			t.Fatalf("opcode %d must be valid", op)
		}
	}
	count := 0
	for i := 0; i < 256; i++ {
		if Opcode(i).Valid() {
			count++
		}
	}
	if OpcodeNot != 12 || OpcodeBoolean != 13 || OpcodeDefined != 14 {
		t.Fatalf("append-only opcode values changed: not=%d boolean=%d defined=%d", OpcodeNot, OpcodeBoolean, OpcodeDefined)
	}
	if count != 14 {
		t.Fatalf("valid opcode count = %d, want exactly 14", count)
	}
	if OpcodeInvalid.Valid() {
		t.Fatal("OpcodeInvalid must not be valid")
	}
}

func TestOpcodeIsGroup(t *testing.T) {
	for _, op := range []Opcode{OpcodeAll, OpcodeAny} {
		if !op.IsGroup() {
			t.Fatalf("opcode %d must be a group", op)
		}
	}
	for i := 0; i < 256; i++ {
		op := Opcode(i)
		if op == OpcodeAll || op == OpcodeAny {
			continue
		}
		if op.IsGroup() {
			t.Fatalf("opcode %d must not be a group", op)
		}
	}
}

func TestRootFlagsMarkIndependentlyAndCombine(t *testing.T) {
	flags := []RootFlags{RootApplicability, RootAssertion, RootEvidence}
	var combined RootFlags
	for i, f := range flags {
		if !f.Has(f) {
			t.Fatalf("flag %d must contain itself", f)
		}
		if RootFlags(0).Has(f) {
			t.Fatalf("zero flags must not contain %d", f)
		}
		for j, g := range flags {
			if i != j && f.Has(g) {
				t.Fatalf("flag %d must not contain independent flag %d", f, g)
			}
		}
		combined |= f
	}
	if combined != RootApplicability|RootAssertion|RootEvidence {
		t.Fatalf("combined = %d, want %d", combined, RootApplicability|RootAssertion|RootEvidence)
	}
	for _, f := range flags {
		if !combined.Has(f) {
			t.Fatalf("combined flags must contain %d", f)
		}
	}
}

// fillSlots places frozen hash/ID slot entries with open addressing, the same
// placement the compiler uses when freezing a symbol probe table. Probing is
// bounded: a fixture table with no empty slot fails the test instead of
// looping.
func fillSlots(t *testing.T, p *Program, entries []struct {
	hash uint64
	id   schema.SymbolID
}) {
	t.Helper()
	n := len(p.SymbolHashes)
	if n == 0 {
		return
	}
	mask := uint64(n - 1)
	for _, e := range entries {
		slot := int(e.hash & mask)
		for probes := 0; probes < n; probes++ {
			if p.SymbolIDs[slot] == 0 {
				break
			}
			slot = (slot + 1) & int(mask)
		}
		if p.SymbolIDs[slot] != 0 {
			t.Fatal("fixture: symbol slot table has no empty slot")
		}
		p.SymbolHashes[slot] = e.hash
		p.SymbolIDs[slot] = e.id
	}
}

// frozenTable returns a hand-built frozen symbol table over the given words
// in one-based ID order. All words must share one masked slot so the table
// exercises a probe chain; the fixture asserts that property.
func frozenTable(t *testing.T, words ...string) Program {
	t.Helper()
	var p Program
	var bytes []byte
	var starts, lengths []uint32
	for _, w := range words {
		starts = append(starts, uint32(len(bytes)))
		lengths = append(lengths, uint32(len(w)))
		bytes = append(bytes, w...)
	}
	p.SymbolBytes = bytes
	p.SymbolStarts = starts
	p.SymbolLengths = lengths
	p.SymbolHashes = make([]uint64, 4)
	p.SymbolIDs = make([]schema.SymbolID, 4)
	mask := uint64(len(p.SymbolHashes) - 1)
	slot := schema.HashSymbol([]byte(words[0])) & mask
	for _, w := range words {
		if schema.HashSymbol([]byte(w))&mask != slot {
			t.Fatalf("fixture: %q must share masked slot %d with %q", w, slot, words[0])
		}
	}
	entries := make([]struct {
		hash uint64
		id   schema.SymbolID
	}, len(words))
	for i, w := range words {
		entries[i] = struct {
			hash uint64
			id   schema.SymbolID
		}{schema.HashSymbol([]byte(w)), schema.SymbolID(i + 1)}
	}
	fillSlots(t, &p, entries)
	return p
}

func TestProgramSymbolFrozenTable(t *testing.T) {
	p := frozenTable(t, "alpha", "beta")
	if got, ok := p.Symbol(1); !ok || string(got) != "alpha" {
		t.Fatalf("Symbol(1) = (%q, %v), want (alpha, true)", got, ok)
	}
	if got, ok := p.Symbol(2); !ok || string(got) != "beta" {
		t.Fatalf("Symbol(2) = (%q, %v), want (beta, true)", got, ok)
	}
	if _, ok := p.Symbol(0); ok {
		t.Fatal("Symbol(0) must be invalid")
	}
	if _, ok := p.Symbol(3); ok {
		t.Fatal("Symbol(3) out of range must be invalid")
	}
	if id, ok := p.LookupSymbol([]byte("alpha")); !ok || id != 1 {
		t.Fatalf("LookupSymbol(alpha) = (%d, %v), want (1, true)", id, ok)
	}
	if id, ok := p.LookupSymbol([]byte("beta")); !ok || id != 2 {
		t.Fatalf("LookupSymbol(beta) = (%d, %v), want (2, true)", id, ok)
	}
}

func TestProgramSymbolCollidingMissReturnsFalse(t *testing.T) {
	p := frozenTable(t, "alpha", "beta")
	if schema.HashSymbol([]byte("zeta"))&3 != schema.HashSymbol([]byte("alpha"))&3 {
		t.Fatal("fixture: zeta must collide with alpha in the masked slot")
	}
	if id, ok := p.LookupSymbol([]byte("zeta")); ok {
		t.Fatalf("LookupSymbol(zeta) = (%d, true), want (0, false)", id)
	}
	if id, ok := p.LookupSymbol([]byte("eta")); ok {
		t.Fatalf("LookupSymbol(eta) = (%d, true), want (0, false)", id)
	}
}

func TestProgramSymbolRejectsMalformedSlots(t *testing.T) {
	var p Program
	p.SymbolHashes = make([]uint64, 4)
	p.SymbolIDs = make([]schema.SymbolID, 3)
	if _, ok := p.LookupSymbol([]byte("x")); ok {
		t.Fatal("mismatched slot column lengths must fail lookup")
	}
	p.SymbolHashes = make([]uint64, 3)
	p.SymbolIDs = make([]schema.SymbolID, 3)
	if _, ok := p.LookupSymbol([]byte("x")); ok {
		t.Fatal("non-power-of-two slot count must fail lookup")
	}
	p.SymbolHashes = nil
	p.SymbolIDs = make([]schema.SymbolID, 4)
	if _, ok := p.LookupSymbol([]byte("x")); ok {
		t.Fatal("nil hash column must fail lookup")
	}
	h := schema.HashSymbol([]byte("x"))
	p.SymbolHashes = make([]uint64, 4)
	p.SymbolIDs = make([]schema.SymbolID, 4)
	for i := range p.SymbolHashes {
		p.SymbolHashes[i] = h
		p.SymbolIDs[i] = 99
	}
	if _, ok := p.LookupSymbol([]byte("x")); ok {
		t.Fatal("full malformed table must fail lookup")
	}
	if _, ok := p.LookupSymbol([]byte("y")); ok {
		t.Fatal("full malformed table must fail lookup for any bytes")
	}
}

func TestInstructionCount(t *testing.T) {
	var p Program
	if got := p.InstructionCount(); got != 0 {
		t.Fatalf("empty InstructionCount = %d, want 0", got)
	}
	p.Opcodes = make([]Opcode, 3)
	if got := p.InstructionCount(); got != 3 {
		t.Fatalf("InstructionCount = %d, want 3", got)
	}
}

func TestEmptyProgramLookupSafe(t *testing.T) {
	var p Program
	if got := p.InstructionCount(); got != 0 {
		t.Fatalf("InstructionCount = %d, want 0", got)
	}
	if _, ok := p.Symbol(1); ok {
		t.Fatal("Symbol on empty program must fail")
	}
	if id, ok := p.LookupSymbol([]byte("anything")); ok {
		t.Fatalf("LookupSymbol on empty program = (%d, true), want (0, false)", id)
	}
	if kind, ok := p.ValueKind(1); ok {
		t.Fatalf("ValueKind on empty program = (%v, true), want (invalid, false)", kind)
	}
}

func TestProgramExplanationCatalogBorrowsProgramStorage(t *testing.T) {
	p := Program{
		Templates:                   result.TemplateTable{LiteralBytes: []byte("literal")},
		Explanations:                result.ExplanationTable{RationaleTemplateIDs: []schema.TemplateID{1}},
		Outcomes:                    result.OutcomeTable{Names: []schema.SymbolID{3}},
		Remediations:                result.RemediationTable{Kinds: []result.RemediationKind{result.RemediationAddEvidence}},
		SymbolBytes:                 []byte("symbols"),
		SymbolStarts:                []uint32{0},
		SymbolLengths:               []uint32{7},
		ValueKinds:                  []schema.ValueKind{schema.ValueKindInteger},
		ValueRefs:                   []uint32{1},
		IntegerValues:               []int64{-1},
		BooleanValues:               []uint64{1},
		TimestampValues:             []int64{2},
		FieldNames:                  []schema.SymbolID{1},
		FieldKinds:                  []schema.ValueKind{schema.ValueKindInteger},
		EvidenceKindNames:           []schema.SymbolID{2},
		EvidenceStateNames:          []schema.SymbolID{3},
		RequirementIDs:              []schema.RequirementID{1},
		EvidenceIssueNodeIDs:        []schema.NodeID{4},
		EvidenceIssueTemplateIDs:    []schema.TemplateID{1},
		ClauseEvidenceSourceNodeIDs: []schema.NodeID{4},
		ClauseEvidenceIDs:           []schema.InstructionID{1},
		EvidenceKinds:               []schema.EvidenceKindID{1},
		EvidenceStates:              []schema.EvidenceStateID{1},
		PolicyName:                  1,
		PolicyVersion:               2,
	}
	catalog := p.ExplanationCatalog()
	if !reflect.DeepEqual(catalog.Templates, p.Templates) || !reflect.DeepEqual(catalog.Explanations, p.Explanations) ||
		!reflect.DeepEqual(catalog.Outcomes, p.Outcomes) || !reflect.DeepEqual(catalog.Remediations, p.Remediations) ||
		catalog.PolicyName != p.PolicyName || catalog.PolicyVersion != p.PolicyVersion {
		t.Fatalf("ExplanationCatalog scalar/table projection = %+v", catalog)
	}
	if &catalog.SymbolBytes[0] != &p.SymbolBytes[0] || &catalog.ValueKinds[0] != &p.ValueKinds[0] ||
		&catalog.FieldNames[0] != &p.FieldNames[0] || &catalog.EvidenceKindNames[0] != &p.EvidenceKindNames[0] ||
		&catalog.RequirementIDs[0] != &p.RequirementIDs[0] || &catalog.EvidenceIssueNodeIDs[0] != &p.EvidenceIssueNodeIDs[0] ||
		&catalog.EvidenceSourceNodes[0] != &p.ClauseEvidenceSourceNodeIDs[0] ||
		&catalog.EvidenceInstructionIDs[0] != &p.ClauseEvidenceIDs[0] ||
		&catalog.InstructionEvidenceKinds[0] != &p.EvidenceKinds[0] ||
		&catalog.InstructionEvidenceStates[0] != &p.EvidenceStates[0] {
		t.Fatal("ExplanationCatalog copied Program storage")
	}
	var nilProgram *Program
	if got := nilProgram.ExplanationCatalog(); !reflect.DeepEqual(got, result.ExplanationCatalog{}) {
		t.Fatalf("nil Program catalog = %+v", got)
	}
}
