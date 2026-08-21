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

	// Instruction-stage scratch (Task 10 slice 3). nodeTemp is the
	// node-to-temporary-InstructionID remap indexed by NodeID-1, kept for
	// later slices that renumber the temporary space. nodeState is the
	// white/gray/black traversal state byte per source node; nodeRoots is the
	// ORed semantic root-role flags per source node; rootNodes is the
	// collected root ID list sorted ascending and deduplicated; stack holds
	// the explicit lowerFrame stack. All are reused across calls.
	nodeTemp  []schema.InstructionID
	nodeState []uint8
	nodeRoots []uint8
	rootNodes []schema.NodeID
	stack     []lowerFrame
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
	dst.RootFlags = dst.RootFlags[:0]
	dst.InstructionNodes = dst.InstructionNodes[:0]
	dst.InstructionSourceStarts = dst.InstructionSourceStarts[:0]
	dst.InstructionSourceEnds = dst.InstructionSourceEnds[:0]
	dst.ListValues = dst.ListValues[:0]
	dst.Operands = dst.Operands[:0]
}

// resetInstructionScratch clears the instruction-stage Lowerer scratch so
// stale traversal state from a previous document never leaks into the next
// lowering. Capacity is retained and re-established by the sizing calls that
// follow.
func (l *Lowerer) resetInstructionScratch() {
	l.nodeTemp = l.nodeTemp[:0]
	l.nodeState = l.nodeState[:0]
	l.nodeRoots = l.nodeRoots[:0]
	l.rootNodes = l.rootNodes[:0]
	l.stack = l.stack[:0]
}

// lowerInstructions emits the iterative topological instruction DAG into dst:
// one temporary instruction per reachable source node, seeded in ascending
// source NodeID order, with every operand already translated to a temporary
// InstructionID strictly lower than its consumer's. It owns exactly the
// instruction-stage destination columns reset by resetInstructionColumns plus
// the ListValues and Operands CSR backings; constant columns stay intact.
//
// The stage assumes validator-clean input, but every accessor failure and
// widened start, count, or address conversion still returns a bounded error
// instead of panicking. It performs no same-kind flattening, no
// common-subexpression reuse, no dead-result removal, and no opcode-run or
// semantic scheduling; later slices own those rewrites and renumber the
// temporary IDs. The node-to-temp remap column is retained in the Lowerer
// for those slices.
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
	l.nodeTemp = resizeSlots(l.nodeTemp, nodeCount)

	dst.Opcodes = resizeSlots(dst.Opcodes, nodeCount)
	dst.Fields = resizeSlots(dst.Fields, nodeCount)
	dst.Values = resizeSlots(dst.Values, nodeCount)
	dst.ListStarts = resizeSlots(dst.ListStarts, nodeCount)
	dst.ListCounts = resizeSlots(dst.ListCounts, nodeCount)
	dst.OperandStarts = resizeSlots(dst.OperandStarts, nodeCount)
	dst.OperandCounts = resizeSlots(dst.OperandCounts, nodeCount)
	dst.EvidenceKinds = resizeSlots(dst.EvidenceKinds, nodeCount)
	dst.EvidenceStates = resizeSlots(dst.EvidenceStates, nodeCount)
	dst.RootFlags = resizeSlots(dst.RootFlags, nodeCount)
	dst.InstructionNodes = resizeSlots(dst.InstructionNodes, nodeCount)
	dst.InstructionSourceStarts = resizeSlots(dst.InstructionSourceStarts, nodeCount)
	dst.InstructionSourceEnds = resizeSlots(dst.InstructionSourceEnds, nodeCount)
	dst.ListValues = resizeSlots(dst.ListValues, len(doc.ListValueIDs))
	dst.Operands = resizeSlots(dst.Operands, len(doc.ChildNodeIDs)+len(doc.NotChildren))

	if err := l.markRootFlags(doc, nodeCount); err != nil {
		return err
	}
	l.collectRoots(doc)

	emitCount := 0
	var listPos, operandPos uint32
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
			var emitErr error
			listPos, operandPos, emitErr = l.emitInstruction(dst, doc, node, emitCount, listPos, operandPos)
			if emitErr != nil {
				return emitErr
			}
			emitCount++
			l.nodeState[node-1] = nodeBlack
			l.stack = l.stack[:top]
		}
	}

	dst.Opcodes = dst.Opcodes[:emitCount]
	dst.Fields = dst.Fields[:emitCount]
	dst.Values = dst.Values[:emitCount]
	dst.ListStarts = dst.ListStarts[:emitCount]
	dst.ListCounts = dst.ListCounts[:emitCount]
	dst.OperandStarts = dst.OperandStarts[:emitCount]
	dst.OperandCounts = dst.OperandCounts[:emitCount]
	dst.EvidenceKinds = dst.EvidenceKinds[:emitCount]
	dst.EvidenceStates = dst.EvidenceStates[:emitCount]
	dst.RootFlags = dst.RootFlags[:emitCount]
	dst.InstructionNodes = dst.InstructionNodes[:emitCount]
	dst.InstructionSourceStarts = dst.InstructionSourceStarts[:emitCount]
	dst.InstructionSourceEnds = dst.InstructionSourceEnds[:emitCount]
	dst.ListValues = dst.ListValues[:listPos]
	dst.Operands = dst.Operands[:operandPos]
	return nil
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

// emitInstruction appends the temporary instruction for one source node at
// row (0-based) and returns the updated CSR append positions. The temporary
// ID space is ascending emission order, so the remap column entry equals
// row+1 and every operand emitted here is lower than its consumer's ID by
// construction: a frame completes only after every child is black, and black
// children were already emitted. Fill-by-index keeps every untouched column
// slot zero per the row shape; unused scalar columns stay zero because the
// pre-sized destination columns were cleared on entry.
func (l *Lowerer) emitInstruction(dst *program.Program, doc *ast.Document, node schema.NodeID, row int, listPos, operandPos uint32) (uint32, uint32, error) {
	kind, ok := doc.Kind(node)
	if !ok {
		return listPos, operandPos, ErrInvalidDocument
	}
	span, ok := doc.Span(node)
	if !ok {
		return listPos, operandPos, ErrInvalidDocument
	}
	l.nodeTemp[node-1] = schema.InstructionID(row + 1)
	dst.RootFlags[row] = program.RootFlags(l.nodeRoots[node-1])
	dst.InstructionNodes[row] = node
	dst.InstructionSourceStarts[row] = span.Start
	dst.InstructionSourceEnds[row] = span.End
	switch kind {
	case ast.NodeKindCompare:
		field, op, valueID, ok := doc.Compare(node)
		if !ok {
			return listPos, operandPos, ErrInvalidDocument
		}
		opcode, ok := compareOpcode(op)
		if !ok {
			return listPos, operandPos, ErrInvalidDocument
		}
		dst.Opcodes[row] = opcode
		dst.Fields[row] = field
		if op == ast.CompareOpIn {
			values, ok := doc.InValues(node)
			if !ok {
				return listPos, operandPos, ErrInvalidDocument
			}
			count := uint32(len(values))
			if uint64(listPos)+uint64(count) > uint64(len(dst.ListValues)) {
				return listPos, operandPos, ErrInvalidDocument
			}
			dst.ListStarts[row] = listPos
			dst.ListCounts[row] = uint16(count)
			for j, value := range values {
				canonical, err := l.canonicalValue(value)
				if err != nil {
					return listPos, operandPos, err
				}
				dst.ListValues[int(listPos)+j] = canonical
			}
			return listPos + count, operandPos, nil
		}
		if valueID != 0 {
			canonical, err := l.canonicalValue(valueID)
			if err != nil {
				return listPos, operandPos, err
			}
			dst.Values[row] = canonical
		}
		return listPos, operandPos, nil
	case ast.NodeKindAll, ast.NodeKindAny:
		children, ok := doc.GroupChildren(node)
		if !ok {
			return listPos, operandPos, ErrInvalidDocument
		}
		count := uint32(len(children))
		if uint64(operandPos)+uint64(count) > uint64(len(dst.Operands)) {
			return listPos, operandPos, ErrInvalidDocument
		}
		if kind == ast.NodeKindAll {
			dst.Opcodes[row] = program.OpcodeAll
		} else {
			dst.Opcodes[row] = program.OpcodeAny
		}
		dst.OperandStarts[row] = operandPos
		dst.OperandCounts[row] = uint16(count)
		for j, child := range children {
			operand := l.nodeTemp[child-1]
			if operand == 0 {
				return listPos, operandPos, ErrInvalidDocument
			}
			dst.Operands[int(operandPos)+j] = operand
		}
		return listPos, operandPos + count, nil
	case ast.NodeKindNot:
		child, ok := doc.NotChild(node)
		if !ok {
			return listPos, operandPos, ErrInvalidDocument
		}
		if uint64(operandPos)+1 > uint64(len(dst.Operands)) {
			return listPos, operandPos, ErrInvalidDocument
		}
		operand := l.nodeTemp[child-1]
		if operand == 0 {
			return listPos, operandPos, ErrInvalidDocument
		}
		dst.Opcodes[row] = program.OpcodeNot
		dst.OperandStarts[row] = operandPos
		dst.OperandCounts[row] = 1
		dst.Operands[int(operandPos)] = operand
		return listPos, operandPos + 1, nil
	case ast.NodeKindEvidence:
		kindID, stateID, ok := doc.Evidence(node)
		if !ok {
			return listPos, operandPos, ErrInvalidDocument
		}
		dst.Opcodes[row] = program.OpcodeEvidence
		dst.EvidenceKinds[row] = kindID
		dst.EvidenceStates[row] = stateID
		return listPos, operandPos, nil
	}
	return listPos, operandPos, ErrInvalidDocument
}
