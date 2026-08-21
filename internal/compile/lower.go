package compile

import (
	"errors"
	"math"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// Bounded lowering-stage errors. Detailed validation diagnostics remain the
// caller's responsibility through Validator; lowering returns one bounded
// stage error.
var (
	// ErrInvalidDocument reports nil inputs or a structurally corrupt AST
	// that the private lowering stages cannot safely read.
	ErrInvalidDocument = errors.New("compile: invalid policy document")
	// ErrInvalidSymbols reports a schema field-name SymbolID that does not
	// resolve in the supplied source Interner.
	ErrInvalidSymbols = errors.New("compile: field symbol bytes missing from source interner")
	// ErrEmptyPolicy reports a policy with no requirements or clauses. The
	// public Lower orchestration owns this check; constant lowering itself
	// accepts value-only documents.
	ErrEmptyPolicy = errors.New("compile: policy has no requirements or clauses")
	// ErrProgramTooLarge reports a fixed-width count or address overflow.
	ErrProgramTooLarge = errors.New("compile: program exceeds fixed-width limits")
	// ErrInvalidGeneratedProgram reports an internally inconsistent generated
	// program, such as a rejected result-table constructor.
	ErrInvalidGeneratedProgram = errors.New("compile: generated program is internally invalid")
)

// Lowerer owns reusable lowering scratch. A zero value is usable, and its
// buffers are retained and reused across calls. It is not safe for concurrent
// use; separate compiler workers use separate Lowerers.
//
// Field order follows fieldalignment while keeping hot scratch locality:
// pointer-bearing slice and table fields precede the fixed Validator state.
type Lowerer struct {
	// validator and diagnostics are the reusable validation state the public
	// Lower orchestration (Task 7) runs before any stage reads unchecked
	// columns. Constant lowering itself never validates.
	validator   Validator
	diagnostics []Diagnostic

	// valueRemap and symbolRemap translate source AST ValueIDs (indexed by
	// ValueID-1) into canonical Program ValueIDs and, for symbol-kind values,
	// canonical Program SymbolIDs. Metadata and catalog names translate
	// through both, and later instruction lowering reads valueRemap for
	// compare and remediation values.
	valueRemap  []schema.ValueID
	symbolRemap []schema.SymbolID

	// symHashes and symIDs are the open-address symbol intern table over AST
	// symbol bytes; exact comparison reads the bytes back from the
	// destination Program slab through Program.Symbol. valHashes, valKinds,
	// valRefs, and valIDs are the open-address canonical-value intern table
	// keyed by kind and payload. Slot zero marks an empty entry everywhere.
	symHashes []uint64
	symIDs    []schema.SymbolID

	valHashes []uint64
	valKinds  []schema.ValueKind
	valRefs   []uint32
	valIDs    []schema.ValueID
}

// lowerConstants canonicalizes the constant program columns into dst: the
// program symbol space, canonical values, copied field schema, translated
// policy and catalog names, retained catalog source spans, the retained
// source bytes, and the frozen symbol probe table. It owns no instruction,
// semantic, or result-table columns; those belong to later lowering stages.
//
// The symbols argument must be the exact schema.Interner that assigned the
// schema's field-name SymbolIDs. Interner identity cannot be inferred from a
// numeric ID, so a different valid interner holding different bytes at the
// same numeric ID is not detectable; only a missing ID is, and it fails with
// ErrInvalidSymbols.
//
// The destination constant columns and all Lowerer scratch are reset on
// entry, so both are reusable across calls while retaining capacity.
func (l *Lowerer) lowerConstants(dst *program.Program, doc *ast.Document, fields *schema.Schema, symbols *schema.Interner) error {
	if doc == nil || fields == nil || symbols == nil {
		return ErrInvalidDocument
	}
	valueCount := len(doc.ValueKinds)
	if len(doc.ValueRefs) != valueCount {
		return ErrInvalidDocument
	}
	fieldCount := fields.Len()
	symCap, err := addCounts(fieldCount, valueCount)
	if err != nil {
		return err
	}

	resetConstantColumns(dst)
	l.resetScratch()

	symSlots := slotSize(symCap)
	valSlots := slotSize(valueCount)
	if symSlots == 0 || valSlots == 0 {
		return ErrProgramTooLarge
	}
	l.symHashes = resizeSlots(l.symHashes, symSlots)
	l.symIDs = resizeSlots(l.symIDs, symSlots)
	l.valHashes = resizeSlots(l.valHashes, valSlots)
	l.valKinds = resizeSlots(l.valKinds, valSlots)
	l.valRefs = resizeSlots(l.valRefs, valSlots)
	l.valIDs = resizeSlots(l.valIDs, valSlots)
	l.valueRemap = resizeSlots(l.valueRemap, valueCount)
	l.symbolRemap = resizeSlots(l.symbolRemap, valueCount)

	if err := l.lowerFieldSchema(dst, fields, symbols); err != nil {
		return err
	}
	if err := l.canonicalizeValues(dst, doc); err != nil {
		return err
	}
	if err := l.lowerCatalogNames(dst, doc); err != nil {
		return err
	}
	if err := l.lowerPolicyIdentity(dst, doc); err != nil {
		return err
	}
	preserveSourceBytes(dst, doc)
	return freezeSymbolSlots(dst)
}

// resetConstantColumns clears every destination constant column this stage
// owns, retaining capacity for reuse. Instruction, semantic, and result-table
// columns belong to later lowering stages and are left untouched.
func resetConstantColumns(dst *program.Program) {
	dst.SymbolBytes = dst.SymbolBytes[:0]
	dst.SymbolStarts = dst.SymbolStarts[:0]
	dst.SymbolLengths = dst.SymbolLengths[:0]
	dst.SymbolHashes = dst.SymbolHashes[:0]
	dst.SymbolIDs = dst.SymbolIDs[:0]
	dst.ValueKinds = dst.ValueKinds[:0]
	dst.ValueRefs = dst.ValueRefs[:0]
	dst.IntegerValues = dst.IntegerValues[:0]
	dst.BooleanValues = dst.BooleanValues[:0]
	dst.TimestampValues = dst.TimestampValues[:0]
	dst.FieldNames = dst.FieldNames[:0]
	dst.FieldKinds = dst.FieldKinds[:0]
	dst.FieldGroups = dst.FieldGroups[:0]
	dst.EvidenceKindNames = dst.EvidenceKindNames[:0]
	dst.EvidenceStateNames = dst.EvidenceStateNames[:0]
	dst.EvidenceKindSourceStarts = dst.EvidenceKindSourceStarts[:0]
	dst.EvidenceKindSourceEnds = dst.EvidenceKindSourceEnds[:0]
	dst.EvidenceStateSourceStarts = dst.EvidenceStateSourceStarts[:0]
	dst.EvidenceStateSourceEnds = dst.EvidenceStateSourceEnds[:0]
	dst.InputBytes = dst.InputBytes[:0]
	dst.ContentHash = [32]byte{}
	dst.PolicyName = 0
	dst.PolicyVersion = 0
	dst.ProgramSymbolCount = 0
}

// resetScratch clears every Lowerer scratch column so stale entries from a
// previous document never leak into the next lowering. Capacity is retained
// and re-established by the sizing calls that follow.
func (l *Lowerer) resetScratch() {
	l.symHashes = l.symHashes[:0]
	l.symIDs = l.symIDs[:0]
	l.valHashes = l.valHashes[:0]
	l.valKinds = l.valKinds[:0]
	l.valRefs = l.valRefs[:0]
	l.valIDs = l.valIDs[:0]
	l.valueRemap = l.valueRemap[:0]
	l.symbolRemap = l.symbolRemap[:0]
	l.diagnostics = l.diagnostics[:0]
}

// lowerFieldSchema copies the field schema in FieldID order, interning each
// field name through the source interner into the program symbol space. Every
// schema field-name SymbolID must resolve in the supplied interner; a missing
// ID fails with ErrInvalidSymbols because the interner is the caller's
// documented identity contract for field-name bytes.
func (l *Lowerer) lowerFieldSchema(dst *program.Program, fields *schema.Schema, symbols *schema.Interner) error {
	n := fields.Len()
	dst.FieldNames = resizeSlots(dst.FieldNames, n)
	dst.FieldKinds = resizeSlots(dst.FieldKinds, n)
	dst.FieldGroups = resizeSlots(dst.FieldGroups, n)
	for i := 0; i < n; i++ {
		id := schema.FieldID(i + 1)
		name, ok := fields.Name(id)
		if !ok {
			return ErrInvalidDocument
		}
		b, ok := symbols.Bytes(name)
		if !ok {
			return ErrInvalidSymbols
		}
		sym, err := l.internSymbol(dst, b)
		if err != nil {
			return err
		}
		dst.FieldNames[i] = sym
		kind, ok := fields.Kind(id)
		if !ok {
			return ErrInvalidDocument
		}
		dst.FieldKinds[i] = kind
		group, ok := fields.Group(id)
		if !ok {
			return ErrInvalidDocument
		}
		dst.FieldGroups[i] = group
	}
	return nil
}

// lowerCatalogNames translates the evidence-kind and evidence-state name
// columns into canonical Program SymbolIDs and preserves their source-span
// peers for the Task 6 semantic lowering. Outcome rows live in the Task 6
// result tables, so their spans are not owned here; the outcome-name bytes
// are already canonical symbols through the value walk.
func (l *Lowerer) lowerCatalogNames(dst *program.Program, doc *ast.Document) error {
	if err := l.translateCatalogNames(dst, doc,
		doc.EvidenceKindNames, doc.EvidenceKindSourceStarts, doc.EvidenceKindSourceEnds,
		&dst.EvidenceKindNames, &dst.EvidenceKindSourceStarts, &dst.EvidenceKindSourceEnds); err != nil {
		return err
	}
	return l.translateCatalogNames(dst, doc,
		doc.EvidenceStateNames, doc.EvidenceStateSourceStarts, doc.EvidenceStateSourceEnds,
		&dst.EvidenceStateNames, &dst.EvidenceStateSourceStarts, &dst.EvidenceStateSourceEnds)
}

// translateCatalogNames translates one symbol-named catalog's name column
// into canonical Program SymbolIDs and copies its source-span peers, writing
// into the destination columns. It enforces the validated symbol-kind
// contract on every name ValueID.
func (l *Lowerer) translateCatalogNames(dst *program.Program, doc *ast.Document, names []schema.ValueID, starts, ends []uint32, dstNames *[]schema.SymbolID, dstStarts, dstEnds *[]uint32) error {
	if len(starts) != len(names) || len(ends) != len(names) {
		return ErrInvalidDocument
	}
	if uint64(len(names)) >= uint64(math.MaxUint32) {
		return ErrProgramTooLarge
	}
	n := len(names)
	*dstNames = resizeSlots(*dstNames, n)
	*dstStarts = resizeSlots(*dstStarts, n)
	*dstEnds = resizeSlots(*dstEnds, n)
	for i := 0; i < n; i++ {
		sym, err := l.symbolForValue(dst, doc, names[i])
		if err != nil {
			return err
		}
		(*dstNames)[i] = sym
		(*dstStarts)[i] = starts[i]
		(*dstEnds)[i] = ends[i]
	}
	return nil
}

// symbolForValue returns the canonical Program SymbolID of a symbol-kind AST
// value, enforcing the validated symbol-kind contract for metadata and
// catalog names. The value walk interns every AST ValueID, so the value and
// symbol remaps must agree on the canonical SymbolID; disagreement is an
// internal defect, not a caller error.
func (l *Lowerer) symbolForValue(dst *program.Program, doc *ast.Document, id schema.ValueID) (schema.SymbolID, error) {
	if id == 0 || uint64(id) > uint64(len(l.valueRemap)) {
		return 0, ErrInvalidDocument
	}
	valueID := l.valueRemap[id-1]
	if valueID == 0 {
		return 0, ErrInvalidDocument
	}
	kind, ok := dst.ValueKind(valueID)
	if !ok || kind != schema.ValueKindSymbol {
		return 0, ErrInvalidDocument
	}
	sym := schema.SymbolID(dst.ValueRefs[valueID-1])
	if sym != l.symbolRemap[id-1] {
		return 0, ErrInvalidDocument
	}
	return sym, nil
}

// lowerPolicyIdentity translates the policy name and version through the
// canonical remap and copies the retained source content hash.
func (l *Lowerer) lowerPolicyIdentity(dst *program.Program, doc *ast.Document) error {
	meta, ok := doc.PolicyMetadata()
	if !ok {
		return ErrInvalidDocument
	}
	name, err := l.symbolForValue(dst, doc, meta.Name)
	if err != nil {
		return err
	}
	version, err := l.symbolForValue(dst, doc, meta.Version)
	if err != nil {
		return err
	}
	dst.PolicyName = name
	dst.PolicyVersion = version
	dst.ContentHash = meta.ContentHash
	return nil
}

// preserveSourceBytes retains the policy source bytes so every later
// source-span column indexes program-owned storage rather than the AST.
func preserveSourceBytes(dst *program.Program, doc *ast.Document) {
	dst.InputBytes = append(dst.InputBytes[:0], doc.InputBytes...)
}
