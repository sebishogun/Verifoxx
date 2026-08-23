package compile

import (
	"errors"
	"math"

	"github.com/sebishogun/verifoxx/internal/ast"
	policyindex "github.com/sebishogun/verifoxx/internal/index"
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
	// resolve to a nonempty name in the supplied source Interner.
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
// pointer-bearing scratch precedes the fixed scheduler state.
type Lowerer struct {
	// validator and diagnostics are the reusable validation state the public
	// Lower orchestration runs before any stage reads unchecked columns.
	// Constant lowering itself never validates.
	validator   Validator
	diagnostics []Diagnostic

	// valueRemap and symbolRemap translate source AST ValueIDs (indexed by
	// ValueID-1) into canonical Program ValueIDs and, for symbol-kind values,
	// canonical Program SymbolIDs. Metadata and catalog names translate
	// through both, and later instruction lowering reads valueRemap for
	// compare and remediation values.
	valueRemap  []schema.ValueID
	symbolRemap []schema.SymbolID

	// symIDs is the open-address symbol intern table over AST symbol bytes;
	// exact comparison reads bytes back from the destination Program slab.
	// valHashes, valKinds, valRefs, and valIDs are the open-address canonical-
	// value intern table keyed by kind and payload. ID zero marks an empty slot.
	symIDs []schema.SymbolID

	valHashes []uint64
	valKinds  []schema.ValueKind
	valRefs   []uint32
	valIDs    []schema.ValueID

	// Instruction traversal and source-node remaps.
	nodeCanon     []schema.InstructionID
	nodeState     []uint8
	nodeRoots     []uint8
	nodeFlatStart []uint32
	nodeFlatCount []uint16
	rootNodes     []schema.NodeID
	stack         []lowerFrame

	// Canonical instruction rows before liveness compaction.
	candidateOpcodes          []program.Opcode
	candidateFields           []schema.FieldID
	candidateValues           []schema.ValueID
	candidateListStarts       []uint32
	candidateListCounts       []uint16
	candidateOperandStarts    []uint32
	candidateOperandCounts    []uint16
	candidateEvidenceKinds    []schema.EvidenceKindID
	candidateEvidenceState    []schema.EvidenceStateID
	candidateEvidenceSubjects []schema.SymbolID
	candidateEvidenceScopes   []schema.SymbolID
	candidateEvidenceTimings  []schema.SymbolID
	candidateRootFlags        []program.RootFlags
	candidateNodes            []schema.NodeID
	candidateSourceStarts     []uint32
	candidateSourceEnds       []uint32
	candidateListValues       []schema.ValueID
	candidateOperands         []schema.InstructionID

	// Open-address CSE, liveness, and stable compaction state.
	candidateHashes  []uint64
	candidateIDs     []schema.InstructionID
	candidateLive    []uint8
	candidateToFinal []schema.InstructionID

	// Deterministic opcode-run scheduling scratch.
	scheduleIndegree   []uint32
	scheduleUserStarts []uint32
	scheduleUsers      []uint32
	scheduleFill       []uint32
	scheduleReadyBits  []uint64
	scheduleOrder      []uint32
	scheduleOldToNew   []schema.InstructionID

	// Scratch-slot liveness, release buckets, free-slot bits, relevance, and
	// final per-instruction assignments. All slices survive Lower calls so a
	// warmed Lowerer plans without per-instruction allocation.
	slotLastUses    []uint32
	slotReleaseHead []schema.InstructionID
	slotReleaseNext []schema.InstructionID
	slotFreeWords   []uint64
	slotReasonLive  []uint8
	slotTruth       []schema.SlotID
	slotReasons     []schema.SlotID

	// Conservative applicability-index construction state. Constraints are
	// emitted as pointerless SoA/CSR columns and canonicalized by indexBuilder.
	indexBuilder         policyindex.PolicyBuilder
	indexStack           []schema.InstructionID
	indexVisited         []uint8
	indexFieldState      []uint8
	indexFieldValueStart []uint32
	indexFieldValueCount []uint32
	indexConstraintRows  []uint32
	indexConstraintField []schema.FieldID
	indexConstraintStart []uint32
	indexConstraintCount []uint32
	indexCandidateValue  []schema.SymbolID
	indexConstraintValue []schema.SymbolID
	factUseCounts        []uint32
	factValueFill        []uint32

	// output owns reusable stage output. It follows every scratch slice so its
	// scalar tail and the fixed scheduler arrays remain outside GC pointer scan.
	// Public Lower freezes exact copies before publication.
	output program.Program

	scheduleReadyCount  [13]uint32
	scheduleReadyCursor [13]uint32
}

// Lower validates and compiles doc into a frozen, self-contained Program.
// The returned Program owns all backing memory and remains valid after the
// source document, schema interner, and local compiler are reused.
func Lower(doc *ast.Document, fields *schema.Schema, symbols *schema.Interner) (*program.Program, error) {
	var lowerer Lowerer
	var dst program.Program
	if err := lowerer.Lower(&dst, doc, fields, symbols); err != nil {
		return nil, err
	}
	return &dst, nil
}

// Lower validates and compiles doc atomically into dst. On any error dst is
// unchanged. Lowerer retains validation, normalization, scheduling, and stage
// output buffers for reuse, but the published Program never borrows them.
func (l *Lowerer) Lower(dst *program.Program, doc *ast.Document, fields *schema.Schema, symbols *schema.Interner) error {
	if l == nil || dst == nil || doc == nil || fields == nil || symbols == nil {
		return ErrInvalidDocument
	}
	l.diagnostics = l.validator.Validate(l.diagnostics[:0], doc, fields)
	if len(l.diagnostics) != 0 {
		return ErrInvalidDocument
	}
	if len(doc.RequirementIDs) == 0 || len(doc.ClauseAssertionRoots) == 0 {
		return ErrEmptyPolicy
	}
	if err := l.lowerConstants(&l.output, doc, fields, symbols); err != nil {
		return err
	}
	if err := l.lowerInstructions(&l.output, doc); err != nil {
		return err
	}
	if err := l.lowerSemantics(&l.output, doc); err != nil {
		return err
	}
	if err := l.assignSlots(&l.output, slotReuse); err != nil {
		return err
	}
	if err := l.lowerIndexes(&l.output); err != nil {
		return err
	}
	frozen, err := program.Freeze(&l.output)
	if err != nil {
		return ErrInvalidGeneratedProgram
	}
	*dst = frozen
	return nil
}

// lowerFrame is one iterative lowering frame: the source node whose
// instruction is not yet emitted and the next child edge to process. A frame
// completes only after every AST child of its node is black, so children are
// always translated to temporary InstructionIDs before their consumer. The
// stack is reused across calls.
type lowerFrame struct {
	node schema.NodeID
	next uint32
}

// Instruction traversal state byte values. Zero is white (unvisited). Gray
// marks a node on the explicit stack; black marks a node whose children are
// all translated, so its own instruction is emitted when the frame completes.
const (
	nodeWhite uint8 = 0
	nodeGray  uint8 = 1 << 0
	nodeBlack uint8 = 1 << 1
)

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
// schema field-name SymbolID must resolve to nonempty bytes in the supplied
// interner; a missing or empty name fails with ErrInvalidSymbols because the
// interner is the caller's documented identity contract for field-name bytes.
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
		if !ok || len(b) == 0 {
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

func (l *Lowerer) optionalSymbolForValue(dst *program.Program, doc *ast.Document, id schema.ValueID) (schema.SymbolID, error) {
	if id == 0 {
		return 0, nil
	}
	return l.symbolForValue(dst, doc, id)
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

// resetInstructionColumns clears every destination instruction column this
// stage owns, retaining capacity for reuse. Constant, semantic, opcode-run,
// node-map, and result-table columns belong to other stages and are left
// untouched, so repeated lowering never disturbs the constant stage's output.
func resetInstructionColumns(dst *program.Program) {
	dst.Opcodes = dst.Opcodes[:0]
	dst.Fields = dst.Fields[:0]
	dst.Values = dst.Values[:0]
	dst.ListStarts = dst.ListStarts[:0]
	dst.ListCounts = dst.ListCounts[:0]
	dst.OperandStarts = dst.OperandStarts[:0]
	dst.OperandCounts = dst.OperandCounts[:0]
	dst.EvidenceKinds = dst.EvidenceKinds[:0]
	dst.EvidenceStates = dst.EvidenceStates[:0]
	dst.EvidenceSubjects = dst.EvidenceSubjects[:0]
	dst.EvidenceScopes = dst.EvidenceScopes[:0]
	dst.EvidenceTimings = dst.EvidenceTimings[:0]
	dst.RootFlags = dst.RootFlags[:0]
	dst.TruthSlots = dst.TruthSlots[:0]
	dst.ReasonSlots = dst.ReasonSlots[:0]
	dst.TruthSlotCount = 0
	dst.ReasonSlotCount = 0
	dst.InstructionNodes = dst.InstructionNodes[:0]
	dst.InstructionSourceStarts = dst.InstructionSourceStarts[:0]
	dst.InstructionSourceEnds = dst.InstructionSourceEnds[:0]
	dst.ListValues = dst.ListValues[:0]
	dst.Operands = dst.Operands[:0]
	dst.OpcodeRunOpcodes = dst.OpcodeRunOpcodes[:0]
	dst.OpcodeRunStarts = dst.OpcodeRunStarts[:0]
	dst.OpcodeRunCounts = dst.OpcodeRunCounts[:0]
	dst.NodeInstructionStarts = dst.NodeInstructionStarts[:0]
	dst.NodeInstructionCounts = dst.NodeInstructionCounts[:0]
	dst.NodeInstructionIDs = dst.NodeInstructionIDs[:0]
	dst.FieldIndex.Kinds = dst.FieldIndex.Kinds[:0]
	dst.FieldIndex.Columns = dst.FieldIndex.Columns[:0]
	dst.FieldIndex.Counts = [6]uint32{}
	dst.ApplicabilityIndex.FieldIDs = dst.ApplicabilityIndex.FieldIDs[:0]
	dst.ApplicabilityIndex.FieldValueStarts = dst.ApplicabilityIndex.FieldValueStarts[:0]
	dst.ApplicabilityIndex.FieldValueCounts = dst.ApplicabilityIndex.FieldValueCounts[:0]
	dst.ApplicabilityIndex.WildcardMasks = dst.ApplicabilityIndex.WildcardMasks[:0]
	dst.ApplicabilityIndex.Values = dst.ApplicabilityIndex.Values[:0]
	dst.ApplicabilityIndex.ValueMasks = dst.ApplicabilityIndex.ValueMasks[:0]
	dst.ApplicabilityIndex.AllMask = dst.ApplicabilityIndex.AllMask[:0]
	dst.ApplicabilityIndex.RequirementCount = 0
	dst.ApplicabilityIndex.WordCount = 0
	dst.FactIndexSpec.FieldIDs = dst.FactIndexSpec.FieldIDs[:0]
	dst.FactIndexSpec.Columns = dst.FactIndexSpec.Columns[:0]
	dst.FactIndexSpec.ValueStarts = dst.FactIndexSpec.ValueStarts[:0]
	dst.FactIndexSpec.ValueCounts = dst.FactIndexSpec.ValueCounts[:0]
	dst.FactIndexSpec.UseCounts = dst.FactIndexSpec.UseCounts[:0]
	dst.FactIndexSpec.Values = dst.FactIndexSpec.Values[:0]
}

// resetInstructionScratch clears the instruction-stage Lowerer scratch so
// stale traversal state from a previous document never leaks into the next
// lowering. Capacity is retained and re-established by the sizing calls that
// follow.
func (l *Lowerer) resetInstructionScratch() {
	l.nodeCanon = l.nodeCanon[:0]
	l.nodeState = l.nodeState[:0]
	l.nodeRoots = l.nodeRoots[:0]
	l.nodeFlatStart = l.nodeFlatStart[:0]
	l.nodeFlatCount = l.nodeFlatCount[:0]
	l.rootNodes = l.rootNodes[:0]
	l.stack = l.stack[:0]
	l.candidateOpcodes = l.candidateOpcodes[:0]
	l.candidateFields = l.candidateFields[:0]
	l.candidateValues = l.candidateValues[:0]
	l.candidateListStarts = l.candidateListStarts[:0]
	l.candidateListCounts = l.candidateListCounts[:0]
	l.candidateOperandStarts = l.candidateOperandStarts[:0]
	l.candidateOperandCounts = l.candidateOperandCounts[:0]
	l.candidateEvidenceKinds = l.candidateEvidenceKinds[:0]
	l.candidateEvidenceState = l.candidateEvidenceState[:0]
	l.candidateEvidenceSubjects = l.candidateEvidenceSubjects[:0]
	l.candidateEvidenceScopes = l.candidateEvidenceScopes[:0]
	l.candidateEvidenceTimings = l.candidateEvidenceTimings[:0]
	l.candidateRootFlags = l.candidateRootFlags[:0]
	l.candidateNodes = l.candidateNodes[:0]
	l.candidateSourceStarts = l.candidateSourceStarts[:0]
	l.candidateSourceEnds = l.candidateSourceEnds[:0]
	l.candidateListValues = l.candidateListValues[:0]
	l.candidateOperands = l.candidateOperands[:0]
	l.candidateHashes = l.candidateHashes[:0]
	l.candidateIDs = l.candidateIDs[:0]
	l.candidateLive = l.candidateLive[:0]
	l.candidateToFinal = l.candidateToFinal[:0]
	l.scheduleIndegree = l.scheduleIndegree[:0]
	l.scheduleUserStarts = l.scheduleUserStarts[:0]
	l.scheduleUsers = l.scheduleUsers[:0]
	l.scheduleFill = l.scheduleFill[:0]
	l.scheduleReadyBits = l.scheduleReadyBits[:0]
	l.scheduleOrder = l.scheduleOrder[:0]
	l.scheduleOldToNew = l.scheduleOldToNew[:0]
	l.scheduleReadyCount = [13]uint32{}
	l.scheduleReadyCursor = [13]uint32{}
}

// lowerInstructions emits the normalized topological instruction DAG into dst.
// It flattens nested same-kind groups, structurally interns pure candidates,
// removes dead flattened group results, compacts live rows without changing
// topological order, and builds the source-node-to-instruction CSR map.
//
// The stage assumes validator-clean input, but every accessor failure and
// widened start, count, or address conversion still returns a bounded error
// instead of panicking. Opcode-run scheduling and semantic-table lowering
// remain later stages.
func (l *Lowerer) lowerInstructions(dst *program.Program, doc *ast.Document) error {
	if doc == nil {
		return ErrInvalidDocument
	}
	nodeCount := doc.Len()
	if uint64(nodeCount) >= uint64(math.MaxUint32) {
		return ErrProgramTooLarge
	}
	if uint64(len(doc.ListValueIDs)) >= uint64(math.MaxUint32) ||
		uint64(len(doc.ChildNodeIDs))+uint64(len(doc.NotChildren)) >= uint64(math.MaxUint32) {
		return ErrProgramTooLarge
	}

	resetInstructionColumns(dst)
	l.resetInstructionScratch()

	l.nodeState = resizeSlots(l.nodeState, nodeCount)
	l.nodeRoots = resizeSlots(l.nodeRoots, nodeCount)
	l.nodeCanon = resizeSlots(l.nodeCanon, nodeCount)
	l.nodeFlatStart = resizeSlots(l.nodeFlatStart, nodeCount)
	l.nodeFlatCount = resizeSlots(l.nodeFlatCount, nodeCount)
	l.prepareCandidateScratch(nodeCount, len(doc.ListValueIDs), len(doc.ChildNodeIDs)+len(doc.NotChildren))

	if err := l.markRootFlags(doc, nodeCount); err != nil {
		return err
	}
	l.collectRoots(doc)

	for _, root := range l.rootNodes {
		if l.nodeState[root-1]&nodeBlack != 0 {
			continue
		}
		l.nodeState[root-1] |= nodeGray
		l.stack = append(l.stack, lowerFrame{node: root})
		for len(l.stack) > 0 {
			top := len(l.stack) - 1
			frame := &l.stack[top]
			node := frame.node
			count, err := l.edgeCount(doc, node)
			if err != nil {
				return err
			}
			if frame.next < count {
				child, err := l.childAt(doc, node, frame.next)
				if err != nil {
					return err
				}
				frame.next++
				state := l.nodeState[child-1]
				if state&nodeGray != 0 {
					// A gray target is a back edge; validator-clean input
					// has no cycles, so this document is corrupt.
					return ErrInvalidDocument
				}
				if state&nodeBlack == 0 {
					l.nodeState[child-1] |= nodeGray
					l.stack = append(l.stack, lowerFrame{node: child})
				}
				continue
			}
			if err := l.internInstructionCandidate(dst, doc, node); err != nil {
				return err
			}
			l.nodeState[node-1] = nodeBlack
			l.stack = l.stack[:top]
		}
	}
	if err := l.compactInstructions(dst, doc); err != nil {
		return err
	}
	return l.scheduleInstructions(dst)
}

// markRootFlags ORs the semantic root-role flags into one byte per source
// node before traversal: requirement applicability roots receive
// RootApplicability, clause assertion roots RootAssertion, and clause
// evidence nodes RootEvidence. A node may carry several roles; the ORed byte
// is copied into the emitted instruction's RootFlags column. Out-of-range
// root IDs are structurally corrupt input and fail the stage.
func (l *Lowerer) markRootFlags(doc *ast.Document, nodeCount int) error {
	for _, root := range doc.RequirementApplicabilityRoots {
		if root == 0 || uint64(root) > uint64(nodeCount) {
			return ErrInvalidDocument
		}
		l.nodeRoots[root-1] |= uint8(program.RootApplicability)
	}
	for _, root := range doc.ClauseAssertionRoots {
		if root == 0 || uint64(root) > uint64(nodeCount) {
			return ErrInvalidDocument
		}
		l.nodeRoots[root-1] |= uint8(program.RootAssertion)
	}
	for _, node := range doc.ClauseEvidenceNodeIDs {
		if node == 0 || uint64(node) > uint64(nodeCount) {
			return ErrInvalidDocument
		}
		l.nodeRoots[node-1] |= uint8(program.RootEvidence)
	}
	return nil
}

// collectRoots gathers every semantic root NodeID into l.rootNodes, sorts
// them ascending in place with an insertion sort, and compacts duplicates so
// traversal seeds source nodes in deterministic ascending order without
// per-node allocation. Root counts are bounded by requirement, clause, and
// evidence-edge row counts, so the quadratic sort is trivial in practice.
func (l *Lowerer) collectRoots(doc *ast.Document) {
	roots := l.rootNodes[:0]
	roots = append(roots, doc.RequirementApplicabilityRoots...)
	roots = append(roots, doc.ClauseAssertionRoots...)
	roots = append(roots, doc.ClauseEvidenceNodeIDs...)
	for i := 1; i < len(roots); i++ {
		x := roots[i]
		j := i
		for j > 0 && roots[j-1] > x {
			roots[j] = roots[j-1]
			j--
		}
		roots[j] = x
	}
	out := 0
	for i := 0; i < len(roots); i++ {
		if i > 0 && roots[i] == roots[i-1] {
			continue
		}
		roots[out] = roots[i]
		out++
	}
	l.rootNodes = roots[:out]
}

// edgeCount returns the number of outgoing AST edges of a structurally safe
// node: zero for Compare and Evidence leaves, one for Not, and the child CSR
// count for All and Any groups. An accessor failure reports corrupt input.
func (l *Lowerer) edgeCount(doc *ast.Document, node schema.NodeID) (uint32, error) {
	kind, ok := doc.Kind(node)
	if !ok {
		return 0, ErrInvalidDocument
	}
	switch kind {
	case ast.NodeKindNot:
		return 1, nil
	case ast.NodeKindAll, ast.NodeKindAny:
		_, count, ok := doc.GroupRange(node)
		if !ok {
			return 0, ErrInvalidDocument
		}
		return count, nil
	}
	return 0, nil
}

// childAt returns the j-th outgoing child of a structurally safe node: the
// Not child for negations and the j-th CSR child for All and Any groups. An
// accessor failure or an out-of-range edge index reports corrupt input.
func (l *Lowerer) childAt(doc *ast.Document, node schema.NodeID, j uint32) (schema.NodeID, error) {
	kind, ok := doc.Kind(node)
	if !ok {
		return 0, ErrInvalidDocument
	}
	switch kind {
	case ast.NodeKindNot:
		child, ok := doc.NotChild(node)
		if !ok {
			return 0, ErrInvalidDocument
		}
		return child, nil
	case ast.NodeKindAll, ast.NodeKindAny:
		children, ok := doc.GroupChildren(node)
		if !ok {
			return 0, ErrInvalidDocument
		}
		if uint64(j) >= uint64(len(children)) {
			return 0, ErrInvalidDocument
		}
		return children[j], nil
	}
	return 0, ErrInvalidDocument
}
