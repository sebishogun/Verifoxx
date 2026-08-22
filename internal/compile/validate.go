package compile

import (
	"bytes"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// nodeStateUnsafe marks a node row that later graph traversal must skip: its
// peer columns, node kind, or payload reference are missing or invalid.
const nodeStateUnsafe uint8 = 1 << 0

// nodeStateGray marks a node currently on the graph traversal stack; reaching
// it again through an outgoing edge is a cycle.
const nodeStateGray uint8 = 1 << 1

// nodeStateBlack marks a node whose outgoing edges are fully processed.
const nodeStateBlack uint8 = 1 << 2

// nodeStateReachable marks a node reached from a semantic root (requirement
// applicability, clause assertion, or clause evidence). Nodes without it after
// traversal are unreachable.
const nodeStateReachable uint8 = 1 << 3

// clauseStateUnsafe marks a clause row that later graph traversal must skip:
// its assertion root or an evidence/remediation CSR range is missing or
// invalid. A bad edge target ID alone never marks the clause unsafe because
// the graph can skip the individual edge.
const clauseStateUnsafe uint8 = 1 << 0

// clauseStateReferenced marks a clause referenced by a valid, in-range edge in
// some requirement's clause CSR. Only referenced clauses seed assertion and
// evidence roots; clauses are never seeded globally.
const clauseStateReferenced uint8 = 1 << 1

// visitFrame is one iterative graph-traversal frame: the node being visited and
// the next outgoing edge index to process. The stack is reused across calls.
type visitFrame struct {
	node schema.NodeID
	next uint32
}

// Validator owns reusable validation state. A zero value is usable, and its
// buffers are reused across Validate calls. It is not safe for concurrent use.
type Validator struct {
	nodeState   []uint8
	clauseState []uint8
	stack       []visitFrame
}

// Validate is the cold convenience path: it validates doc with a fresh local
// validator and returns the appended diagnostics.
func Validate(dst []Diagnostic, doc *ast.Document, fields *schema.Schema) []Diagnostic {
	var v Validator
	return v.Validate(dst, doc, fields)
}

// Validate appends diagnostics for doc into dst and returns the extended
// slice. A nil document or nil field schema appends exactly one
// CodeInvalidDocument. Otherwise it runs the structural phase, then the
// semantic phase, then the graph phase. The structural phase checks parallel
// column lengths, payload-table rows, node rows, catalog rows, clause rows,
// and requirement rows. The semantic phase validates the Compare operation,
// arity, and value-kind compatibility, ordered-field restrictions, nonempty
// All/Any groups, the two clause rules (every evidence edge targets a node
// whose declared kind is NodeKindEvidence, and every clause resolves all seven
// outcome slots), symbol-typed and byte-unique catalog and outcome names,
// remediation kind, shape, and set-field type compatibility, and nonzero
// unique requirement IDs with nonempty clause ranges. The graph phase traverses
// requirement applicability roots and the assertion and evidence roots of
// requirement-referenced clauses, reporting one CodeCycle per back edge and
// one CodeUnreachableNode per safe node never reached from a semantic root.
// Validate never mutates doc or fields, and it allocates only when appending
// exceeds dst's capacity or a reusable state buffer must grow.
func (v *Validator) Validate(dst []Diagnostic, doc *ast.Document, fields *schema.Schema) []Diagnostic {
	if doc == nil || fields == nil {
		return append(dst, Diagnostic{Code: CodeInvalidDocument, Table: TableDocument})
	}
	dst = v.validateStructure(dst, doc, fields)
	dst = v.validateSemantics(dst, doc, fields)
	return v.validateGraph(dst, doc)
}

// validateStructure runs the phase-1 bounds-safe structural checks: reusable
// node and clause state is resized and cleared, the traversal stack is
// truncated, and row checks run in fixed order over value, compare, group, not,
// evidence, node, evidence-kind, evidence-state, outcome, remediation, clause,
// and requirement tables. Later phases append semantic and graph diagnostics
// after this method returns, so callers inside the package can run only this
// phase to assert isolated structural diagnostics.
func (v *Validator) validateStructure(dst []Diagnostic, doc *ast.Document, fields *schema.Schema) []Diagnostic {
	v.nodeState = resizeBytes(v.nodeState, len(doc.NodeKinds))
	v.clauseState = resizeBytes(v.clauseState, len(doc.ClauseAssertionRoots))
	v.stack = v.stack[:0]
	dst = v.checkColumnLengths(dst, doc)
	dst = v.checkValueRows(dst, doc)
	dst = v.checkCompareRows(dst, doc, fields)
	dst = v.checkGroupRows(dst, doc)
	dst = v.checkNotRows(dst, doc)
	dst = v.checkEvidenceRows(dst, doc)
	dst = v.checkNodeRows(dst, doc)
	dst = v.checkEvidenceKindRows(dst, doc)
	dst = v.checkEvidenceStateRows(dst, doc)
	dst = v.checkOutcomeRows(dst, doc)
	dst = v.checkRemediationRows(dst, doc, fields)
	dst = v.checkClauseRows(dst, doc)
	return v.checkRequirementRows(dst, doc)
}

// validateSemantics runs the semantic checks over structurally safe rows. It
// validates expression nodes and the symbol typing of the three
// catalog name columns: Compare operation, scalar/list arity, value-kind
// compatibility of Equal, NotEqual, and In operands, the ordered field/kind
// restrictions of Less, LessEqual, Greater, and GreaterEqual, the
// at-least-one-child arity of All and Any groups, the evidence-kind,
// evidence-state, and outcome names, which must be symbols unique by bytes
// within each table, the remediation kind, payload shape, and set-field
// field/value type compatibility, the two clause rules, which require every
// evidence edge to target a node whose declared kind is NodeKindEvidence and
// every clause to resolve all seven outcome slots, and the requirement IDs
// and clause ranges, which must be nonzero and unique with a nonempty clause
// CSR per row. Applicability-reference and graph semantics belong to the
// graph phase, which runs after this method returns.
// Diagnostics are owned by the payload table row (Row = ref+1) with the node
// owner and its exact valid source span, and unreferenced payload rows stay
// inert. The scans are deterministic; per-row work and allocation behavior are
// documented on each helper rather than claimed globally.
func (v *Validator) validateSemantics(dst []Diagnostic, doc *ast.Document, fields *schema.Schema) []Diagnostic {
	dst = v.checkExpressionSemantics(dst, doc, fields)
	dst = v.checkEvidenceKindNameSemantics(dst, doc)
	dst = v.checkEvidenceStateNameSemantics(dst, doc)
	dst = v.checkOutcomeNameSemantics(dst, doc)
	dst = v.checkRemediationSemantics(dst, doc, fields)
	dst = v.checkClauseSemantics(dst, doc)
	return v.checkRequirementSemantics(dst, doc)
}

// checkExpressionSemantics scans nodes in ascending NodeID order, skips rows
// marked unsafe by validateStructure, and dispatches each structurally safe
// expression node to its payload-table rule: Compare rows are validated by
// checkCompareRowSemantics and All/Any group rows require at least one child.
// The node span is attached only when it is an exact valid range over the
// input; otherwise a zero span is used. Each payload row is re-checked against
// the safe parallel length and CSR bounds before use, so structural defects
// never cascade into semantic diagnostics: an invalid node peer, payload ref,
// or CSR range keeps the node structural-only, and invalid child IDs never
// create an extra semantic diagnostic.
func (v *Validator) checkExpressionSemantics(dst []Diagnostic, doc *ast.Document, fields *schema.Schema) []Diagnostic {
	compareRows := safeMin(len(doc.CompareFields), len(doc.CompareOps), len(doc.CompareValues),
		len(doc.CompareListStarts), len(doc.CompareListCounts))
	groupRows := minInt(len(doc.GroupChildStarts), len(doc.GroupChildCounts))
	for i := range doc.NodeKinds {
		if v.nodeState[i]&nodeStateUnsafe != 0 {
			continue
		}
		if i >= len(doc.NodeRefs) {
			continue
		}
		ref := uint64(doc.NodeRefs[i])
		id := schema.NodeID(i + 1)
		span := ast.SourceSpan{}
		if i < len(doc.SourceStarts) && i < len(doc.SourceEnds) {
			s := ast.SourceSpan{Start: doc.SourceStarts[i], End: doc.SourceEnds[i]}
			if s.Start <= s.End && uint64(s.End) <= uint64(len(doc.InputBytes)) {
				span = s
			}
		}
		switch doc.NodeKinds[i] {
		case ast.NodeKindCompare:
			if ref < uint64(compareRows) {
				dst = checkCompareRowSemantics(dst, doc, fields, id, uint32(ref), span)
			}
		case ast.NodeKindAll, ast.NodeKindAny:
			if ref < uint64(groupRows) {
				r := uint32(ref)
				start := doc.GroupChildStarts[r]
				count := uint32(doc.GroupChildCounts[r])
				if count == 0 && validRange(start, count, len(doc.ChildNodeIDs)) {
					dst = append(dst, Diagnostic{Code: CodeInvalidArity, Table: TableGroup, Member: MemberChildren, Row: r + 1, Node: id, Span: span})
				}
			}
		}
	}
	return dst
}

// checkCompareRowSemantics validates one structurally safe Compare payload
// row ref (0-based): the operation, the scalar/list arity, and the
// value-kind compatibility, including the ordered-field restrictions of Less,
// LessEqual, Greater, and GreaterEqual. The node span is attached only when
// it is an exact valid range over the input; otherwise a zero span is used.
// The payload row was already re-checked against the safe parallel length and
// CSR bounds by the caller, so structural defects never cascade into semantic
// diagnostics: type checks run only on arity-valid rows whose field and
// operand IDs are structurally valid.
func checkCompareRowSemantics(dst []Diagnostic, doc *ast.Document, fields *schema.Schema, id schema.NodeID, ref uint32, span ast.SourceSpan) []Diagnostic {
	row := ref + 1
	op := doc.CompareOps[ref]
	if !op.Valid() {
		dst = append(dst, Diagnostic{Code: CodeInvalidArity, Table: TableCompare, Member: MemberOperation, Row: row, Node: id, Span: span})
		return dst
	}
	value := doc.CompareValues[ref]
	count := uint32(doc.CompareListCounts[ref])
	listOK := validRange(doc.CompareListStarts[ref], count, len(doc.ListValueIDs))
	switch op {
	case ast.CompareOpExists:
		if value != 0 {
			dst = append(dst, Diagnostic{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: row, Node: id, Span: span, Value: value})
		}
		if listOK && count != 0 {
			dst = append(dst, Diagnostic{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValues, Row: row, Node: id, Span: span})
		}
	case ast.CompareOpIn:
		if value != 0 {
			dst = append(dst, Diagnostic{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: row, Node: id, Span: span, Value: value})
		}
		if listOK && count == 0 {
			dst = append(dst, Diagnostic{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValues, Row: row, Node: id, Span: span})
		}
	default:
		if value == 0 {
			dst = append(dst, Diagnostic{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValue, Row: row, Node: id, Span: span})
		}
		if listOK && count != 0 {
			dst = append(dst, Diagnostic{Code: CodeInvalidArity, Table: TableCompare, Member: MemberValues, Row: row, Node: id, Span: span})
		}
	}
	if op == ast.CompareOpEqual || op == ast.CompareOpNotEqual {
		if value != 0 && listOK && count == 0 {
			// Scalar kind compatibility runs only on an arity-valid row
			// with a structurally valid field and literal; any out-of-range
			// field or value ID, a shortened value peer column, a
			// non-literal kind, or a missing scalar is already diagnosed
			// above and never cascades into a type diagnostic.
			field := doc.CompareFields[ref]
			if fieldKind, ok := fields.Kind(field); ok {
				if kind, ok := literalValueKind(doc, value); ok && kind != fieldKind {
					dst = append(dst, Diagnostic{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberValue, Row: row, Node: id, Span: span, Field: field, Value: value})
				}
			}
		}
	}
	if op == ast.CompareOpIn {
		if value == 0 && listOK && count > 0 {
			// In kind compatibility runs only on an arity-valid row: a
			// presence field is incompatible with the operation as a
			// whole, so it emits exactly one MemberField diagnostic and
			// skips the element checks; any other field kind is compared
			// against each structurally valid literal in CSR order, and
			// invalid entries are skipped without cascading.
			field := doc.CompareFields[ref]
			if fieldKind, ok := fields.Kind(field); ok {
				if fieldKind == schema.ValueKindPresence {
					dst = append(dst, Diagnostic{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberField, Row: row, Node: id, Span: span, Field: field})
				} else {
					start := doc.CompareListStarts[ref]
					for j := uint32(0); j < count; j++ {
						e := doc.ListValueIDs[int(start)+int(j)]
						if kind, ok := literalValueKind(doc, e); ok && kind != fieldKind {
							dst = append(dst, Diagnostic{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberValues, Row: row, Node: id, Span: span, Field: field, Value: e})
						}
					}
				}
			}
		}
	}
	if op >= ast.CompareOpLess && op <= ast.CompareOpGreaterEqual {
		if value != 0 && listOK && count == 0 {
			// Ordered comparisons run only on an arity-valid row. A field
			// that is not integer or timestamp is incompatible with the
			// operation as a whole: exactly one MemberField diagnostic is
			// emitted and the scalar check is skipped. An ordered field
			// requires a structurally valid literal of the same kind;
			// out-of-range IDs, shortened value peers, and non-literal
			// kinds were already diagnosed above and never cascade.
			field := doc.CompareFields[ref]
			if fieldKind, ok := fields.Kind(field); ok {
				if !ordered(fieldKind) {
					dst = append(dst, Diagnostic{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberField, Row: row, Node: id, Span: span, Field: field})
				} else if kind, ok := literalValueKind(doc, value); ok && kind != fieldKind {
					dst = append(dst, Diagnostic{Code: CodeTypeMismatch, Table: TableCompare, Member: MemberValue, Row: row, Node: id, Span: span, Field: field, Value: value})
				}
			}
		}
	}
	return dst
}

// checkCatalogNameSemantics validates the name ValueIDs of one symbol-named
// catalog table (evidence kind, evidence state, or outcome) up to the safe
// parallel row count of its name and source-span columns, scanning rows
// ascending. A structurally valid symbol value row names the kind cleanly
// unless it duplicates an earlier structurally valid symbol name: names are
// unique by exact symbol bytes within the table, so the current row emits
// exactly one CodeDuplicateName on MemberName at the first byte-equal
// predecessor and the scan stops there; the first row is never diagnosed. A
// structurally valid non-symbol literal (integer, boolean, or timestamp) emits
// exactly one CodeTypeMismatch on MemberName instead and never participates in
// the duplicate scan. Both diagnostics carry the valid owner span and the exact
// per-row strong owner ID selected by table. Zero or out-of-range names,
// truncated value peer columns, invalid or non-literal stored kinds, and
// invalid payload refs/ranges stay structural-only and never cascade, and such
// rows are not duplicate targets. An invalid owner span does not suppress an
// independent diagnostic; it attaches a zero span instead. Exact-byte
// uniqueness uses deterministic predecessor scans: no map or scratch table is
// allocated, but a table of n unique valid symbols costs O(n^2) comparisons.
func checkCatalogNameSemantics(dst []Diagnostic, doc *ast.Document, table TableKind, names []schema.ValueID, starts, ends []uint32) []Diagnostic {
	rows := safeMin(len(names), len(starts), len(ends))
	for i := 0; i < rows; i++ {
		span, spanOK := validOwnerSpan(starts[i], ends[i], len(doc.InputBytes))
		attach := span
		if !spanOK {
			attach = ast.SourceSpan{}
		}
		if name := names[i]; name != 0 {
			kind, ok := structuralLiteralKind(doc, name)
			if !ok {
				continue
			}
			row := uint32(i + 1)
			if kind != schema.ValueKindSymbol {
				d := Diagnostic{Code: CodeTypeMismatch, Table: table, Member: MemberName, Row: row, Span: attach, Value: name}
				catalogNameOwner(&d, table, row)
				dst = append(dst, d)
				continue
			}
			cur := symbolBytes(doc, name)
			for j := 0; j < i; j++ {
				prev := names[j]
				if prev == 0 {
					continue
				}
				equal := false
				if prev == name {
					// Current validity proves the same ValueID is a valid
					// symbol, so no revalidation is needed.
					equal = true
				} else if pkind, pok := structuralLiteralKind(doc, prev); pok && pkind == schema.ValueKindSymbol {
					equal = bytes.Equal(symbolBytes(doc, prev), cur)
				}
				if equal {
					d := Diagnostic{Code: CodeDuplicateName, Table: table, Member: MemberName, Row: row, Span: attach, Value: name}
					catalogNameOwner(&d, table, row)
					dst = append(dst, d)
					break
				}
			}
		}
	}
	return dst
}

// catalogNameOwner stamps the per-row strong owner ID selected by table onto d.
func catalogNameOwner(d *Diagnostic, table TableKind, row uint32) {
	switch table {
	case TableEvidenceKind:
		d.EvidenceKind = schema.EvidenceKindID(row)
	case TableEvidenceState:
		d.EvidenceState = schema.EvidenceStateID(row)
	case TableOutcome:
		d.Outcome = schema.OutcomeID(row)
	}
}

// symbolBytes returns the symbol payload bytes of the value row named by id.
// Precondition: structuralLiteralKind already returned ValueKindSymbol for id, which
// guarantees id is nonzero, both value peer columns cover it, the stored kind
// is symbol, and the symbol payload range is valid. The helper performs no
// bounds checks and allocates nothing; the returned slice is a view of
// SymbolBytes.
func symbolBytes(doc *ast.Document, id schema.ValueID) []byte {
	ref := doc.ValueRefs[id-1]
	s := doc.SymbolStarts[ref]
	return doc.SymbolBytes[int(s) : int(s)+int(doc.SymbolLengths[ref])]
}

// checkEvidenceKindNameSemantics types the evidence-kind name column.
func (v *Validator) checkEvidenceKindNameSemantics(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	return checkCatalogNameSemantics(dst, doc, TableEvidenceKind, doc.EvidenceKindNames, doc.EvidenceKindSourceStarts, doc.EvidenceKindSourceEnds)
}

// checkEvidenceStateNameSemantics types the evidence-state name column.
func (v *Validator) checkEvidenceStateNameSemantics(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	return checkCatalogNameSemantics(dst, doc, TableEvidenceState, doc.EvidenceStateNames, doc.EvidenceStateSourceStarts, doc.EvidenceStateSourceEnds)
}

// checkOutcomeNameSemantics types the outcome name column.
func (v *Validator) checkOutcomeNameSemantics(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	return checkCatalogNameSemantics(dst, doc, TableOutcome, doc.OutcomeNames, doc.OutcomeSourceStarts, doc.OutcomeSourceEnds)
}

// checkRemediationSemantics validates the remediation kind, payload shape,
// and set-field type compatibility of every row up to the safe parallel length
// of its six peer columns, scanning rows ascending after the outcome-name
// scan. An invalid kind (zero or out of enum) emits exactly one
// CodeInvalidRemediation on MemberRecordKind carrying the one-based row, the
// Remediation strong ID, and the valid owner span, and stops all further
// semantic checks for that row. A valid kind enforces its exact payload shape,
// accumulating every independent defect in fixed member order MemberField,
// MemberValue, then MemberEvidenceKind: SetField requires a nonzero field and
// value and a zero evidence kind, while AddEvidence requires zero field and
// value and a nonzero evidence kind. Each shape diagnostic carries the
// offending member value. Nonzero IDs count as present even when structurally
// out of range, so a required high ID receives only its structural reference
// diagnostic while a forbidden nonzero high ID also receives its independent
// shape diagnostic. A SetField row with a nonzero field and value additionally
// requires identical field and literal value kinds: when fields.Kind succeeds
// and the ValueID names a structurally valid literal row including a valid
// payload ref/range, a kind mismatch emits exactly one CodeTypeMismatch on
// MemberValue after all shape diagnostics for the row, carrying the row,
// Remediation ID, valid owner span, field, and value. Zero or high field/value
// IDs, shortened value peer columns, non-literal stored kinds, and invalid
// value payload refs/ranges suppress type output without suppressing shape
// diagnostics, and AddEvidence never performs field/value type checks. An
// invalid owner span does not suppress these diagnostics; it attaches a zero
// span. The kind lookups are bounds-safe by construction: fields.Kind returns
// false for a zero or out-of-schema field, and structuralLiteralKind returns
// false for a zero, out-of-range, or structurally invalid value row, so type
// checking never indexes beyond the schema or the value peer columns and a
// corrupt row cannot cascade or panic.
func (v *Validator) checkRemediationSemantics(dst []Diagnostic, doc *ast.Document, fields *schema.Schema) []Diagnostic {
	rows := safeMin(len(doc.RemediationKinds), len(doc.RemediationFields), len(doc.RemediationValues),
		len(doc.RemediationEvidenceKinds), len(doc.RemediationSourceStarts), len(doc.RemediationSourceEnds))
	for i := 0; i < rows; i++ {
		row := uint32(i + 1)
		id := schema.RemediationID(i + 1)
		span, spanOK := validOwnerSpan(doc.RemediationSourceStarts[i], doc.RemediationSourceEnds[i], len(doc.InputBytes))
		attach := span
		if !spanOK {
			attach = ast.SourceSpan{}
		}
		kind := doc.RemediationKinds[i]
		if !kind.Valid() {
			dst = append(dst, Diagnostic{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberRecordKind, Row: row, Span: attach, Remediation: id})
			continue
		}
		field := doc.RemediationFields[i]
		value := doc.RemediationValues[i]
		evidence := doc.RemediationEvidenceKinds[i]
		switch kind {
		case ast.RemediationKindSetField:
			if field == 0 {
				dst = append(dst, Diagnostic{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberField, Row: row, Span: attach, Remediation: id})
			}
			if value == 0 {
				dst = append(dst, Diagnostic{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberValue, Row: row, Span: attach, Remediation: id})
			}
			if evidence != 0 {
				dst = append(dst, Diagnostic{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberEvidenceKind, Row: row, Span: attach, Remediation: id, EvidenceKind: evidence})
			}
			if field != 0 && value != 0 {
				// Type compatibility runs only on an otherwise eligible
				// SetField row after every shape diagnostic for the row: a
				// zero or structurally high field or value ID, a shortened
				// value peer column, a non-literal stored kind, and an
				// invalid value payload ref/range are already diagnosed
				// elsewhere and never cascade into a type diagnostic.
				if fieldKind, ok := fields.Kind(field); ok {
					if kind, ok := structuralLiteralKind(doc, value); ok && kind != fieldKind {
						dst = append(dst, Diagnostic{Code: CodeTypeMismatch, Table: TableRemediation, Member: MemberValue, Row: row, Span: attach, Remediation: id, Field: field, Value: value})
					}
				}
			}
		case ast.RemediationKindAddEvidence:
			if field != 0 {
				dst = append(dst, Diagnostic{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberField, Row: row, Span: attach, Remediation: id, Field: field})
			}
			if value != 0 {
				dst = append(dst, Diagnostic{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberValue, Row: row, Span: attach, Remediation: id, Value: value})
			}
			if evidence == 0 {
				dst = append(dst, Diagnostic{Code: CodeInvalidRemediation, Table: TableRemediation, Member: MemberEvidenceKind, Row: row, Span: attach, Remediation: id})
			}
		}
	}
	return dst
}

// checkClauseSemantics validates the two clause rules of every row up to the
// safe parallel length of its fourteen peer columns, scanning rows ascending
// after the remediation scan. The evidence-node-kind rule inspects every edge
// of a structurally valid evidence CSR in CSR order: a zero or high target ID
// is structural-only and skipped, an in-range target whose own node kind is
// invalid is skipped because structural node-kind validation owns that row,
// and an in-range target with any valid kind other than NodeKindEvidence emits
// exactly one CodeInvalidEvidence on MemberEvidence carrying the one-based
// clause row, the Clause ID, the target Node ID, and the valid clause owner
// span. A valid NodeKindEvidence target is clean even when that target's
// payload or reference peers are structurally defective, because this rule
// checks only the declared node kind, and empty evidence ranges are valid and
// clean. The resolution rule then inspects the seven outcome columns in
// declared order (satisfied, false, missing, stale, unclear, unverifiable,
// conflict): a zero OutcomeID emits exactly one CodeMissingResolution on the
// matching Member carrying the one-based clause row, the Clause ID, the valid
// clause owner span, and a zero Outcome; a nonzero in-range OutcomeID is
// clean; and a nonzero high OutcomeID is structural-only because the
// structural phase already emitted its CodeInvalidOutcome for that slot. The
// two rules are independent of each other and of the assertion root,
// evidence/remediation CSR validity and edge validity, and every other outcome
// slot of the same safe row: an invalid evidence CSR skips only the
// evidence-edge checks, never the resolution checks, and a clause marked
// unsafe by structural defects still receives its evidence-kind and
// resolution diagnostics. Truncated clause peer tail rows beyond the safe
// minimum stay structural-only, and an invalid owner span does not suppress a
// diagnostic; it attaches a zero span. Each clause row is a fixed bounded set
// of typed direct checks with no map, string, fmt, recursion, closure,
// interface, or new persistent state; appending allocates only when the
// destination capacity is exceeded on a warm caller.
func (v *Validator) checkClauseSemantics(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	rows := safeMin(
		len(doc.ClauseAssertionRoots),
		len(doc.ClauseEvidenceStarts), len(doc.ClauseEvidenceCounts),
		len(doc.ClauseRemediationStarts), len(doc.ClauseRemediationCounts),
		len(doc.ClauseOnSatisfied), len(doc.ClauseOnFalse), len(doc.ClauseOnMissing),
		len(doc.ClauseOnStale), len(doc.ClauseOnUnclear), len(doc.ClauseOnUnverifiable),
		len(doc.ClauseOnConflict), len(doc.ClauseSourceStarts), len(doc.ClauseSourceEnds))
	nodeCount := len(doc.NodeKinds)
	for i := 0; i < rows; i++ {
		span, spanOK := validOwnerSpan(doc.ClauseSourceStarts[i], doc.ClauseSourceEnds[i], len(doc.InputBytes))
		attach := span
		if !spanOK {
			attach = ast.SourceSpan{}
		}
		row := uint32(i + 1)
		clause := schema.ClauseID(i + 1)
		start := doc.ClauseEvidenceStarts[i]
		count := uint32(doc.ClauseEvidenceCounts[i])
		if validRange(start, count, len(doc.ClauseEvidenceNodeIDs)) {
			for j := uint32(0); j < count; j++ {
				target := doc.ClauseEvidenceNodeIDs[int(start)+int(j)]
				if target == 0 || uint64(target) > uint64(nodeCount) {
					continue
				}
				kind := doc.NodeKinds[target-1]
				if !kind.Valid() {
					continue
				}
				if kind != ast.NodeKindEvidence {
					dst = append(dst, Diagnostic{Code: CodeInvalidEvidence, Table: TableClause, Member: MemberEvidence, Row: row, Span: attach, Clause: clause, Node: target})
				}
			}
		}
		if doc.ClauseOnSatisfied[i] == 0 {
			dst = append(dst, Diagnostic{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeSatisfied, Row: row, Span: attach, Clause: clause})
		}
		if doc.ClauseOnFalse[i] == 0 {
			dst = append(dst, Diagnostic{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeFalse, Row: row, Span: attach, Clause: clause})
		}
		if doc.ClauseOnMissing[i] == 0 {
			dst = append(dst, Diagnostic{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeMissing, Row: row, Span: attach, Clause: clause})
		}
		if doc.ClauseOnStale[i] == 0 {
			dst = append(dst, Diagnostic{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeStale, Row: row, Span: attach, Clause: clause})
		}
		if doc.ClauseOnUnclear[i] == 0 {
			dst = append(dst, Diagnostic{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeUnclear, Row: row, Span: attach, Clause: clause})
		}
		if doc.ClauseOnUnverifiable[i] == 0 {
			dst = append(dst, Diagnostic{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeUnverifiable, Row: row, Span: attach, Clause: clause})
		}
		if doc.ClauseOnConflict[i] == 0 {
			dst = append(dst, Diagnostic{Code: CodeMissingResolution, Table: TableClause, Member: MemberOutcomeConflict, Row: row, Span: attach, Clause: clause})
		}
	}
	return dst
}

// checkRequirementSemantics validates the RequirementID column and the clause
// CSR arity of every row up to the safe parallel length of its six peer
// columns, scanning rows ascending. A zero
// RequirementID emits exactly one CodeInvalidID on MemberID carrying the
// one-based row, Requirement 0, and the valid owner span, and never
// participates in the duplicate comparison. Nonzero RequirementIDs must be
// unique: a later row matching any earlier nonzero ID emits exactly one
// CodeDuplicateID on MemberID with the current row, the Requirement ID, and
// the current valid owner span, stopping at its first equal predecessor; the
// first occurrence is never diagnosed. Any nonzero uint32 ID, including
// MaxUint32, is otherwise valid. The ID diagnostics are independent of the
// clause arity check: within one row the ID diagnostic is appended first, then
// a structurally valid clause CSR range with count zero emits exactly one
// CodeInvalidArity on MemberClauses with the one-based row, the row's
// Requirement ID (zero included), and the current valid owner span; a valid
// nonempty range is clean even when an individual ClauseID edge is
// structurally invalid, and an invalid CSR range stays structural-only and
// never cascades into an arity diagnostic. The scan is independent of the
// applicability root and clause edge columns of the same safe row, which the
// structural phase owns, so a structurally invalid edge never suppresses a
// semantic diagnostic and truncated peer tail rows beyond the safe minimum
// stay structural-only. An invalid owner span does not suppress a semantic
// diagnostic; it attaches a zero span. The deterministic predecessor scan uses
// no map, string, fmt, recursion, closure, interface, or persistent state, but
// n unique requirement IDs cost O(n^2) comparisons. Appending allocates only
// when the destination capacity is exceeded.
func (v *Validator) checkRequirementSemantics(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	rows := safeMin(
		len(doc.RequirementIDs), len(doc.RequirementApplicabilityRoots),
		len(doc.RequirementClauseStarts), len(doc.RequirementClauseCounts),
		len(doc.RequirementSourceStarts), len(doc.RequirementSourceEnds))
	for i := 0; i < rows; i++ {
		row := uint32(i + 1)
		span, spanOK := validOwnerSpan(doc.RequirementSourceStarts[i], doc.RequirementSourceEnds[i], len(doc.InputBytes))
		attach := span
		if !spanOK {
			attach = ast.SourceSpan{}
		}
		id := doc.RequirementIDs[i]
		if id == 0 {
			dst = append(dst, Diagnostic{Code: CodeInvalidID, Table: TableRequirement, Member: MemberID, Row: row, Span: attach})
		} else {
			for j := 0; j < i; j++ {
				prev := doc.RequirementIDs[j]
				if prev != 0 && prev == id {
					dst = append(dst, Diagnostic{Code: CodeDuplicateID, Table: TableRequirement, Member: MemberID, Row: row, Span: attach, Requirement: id})
					break
				}
			}
		}
		start := doc.RequirementClauseStarts[i]
		count := uint32(doc.RequirementClauseCounts[i])
		if validRange(start, count, len(doc.RequirementClauseIDs)) && count == 0 {
			dst = append(dst, Diagnostic{Code: CodeInvalidArity, Table: TableRequirement, Member: MemberClauses, Row: row, Span: attach, Requirement: id})
		}
	}
	return dst
}

// resizeBytes sizes dst to n elements, reusing its capacity when sufficient.
// It always clears the active range so stale per-row state from a previous
// document never leaks into the next validation.
func resizeBytes(dst []uint8, n int) []uint8 {
	if cap(dst) < n {
		return make([]uint8, n)
	}
	dst = dst[:n]
	clear(dst)
	return dst
}

// validRange reports whether the half-open range [start, start+count) fits
// within [0, total) without uint32 overflow.
func validRange(start, count uint32, total int) bool {
	if total < 0 {
		return false
	}
	s, c, t := uint64(start), uint64(count), uint64(total)
	return s <= t && c <= t-s
}

// minInt returns the smaller of a and b.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// safeMin returns the smallest of vals. It is used for the safe row count of a
// parallel table so a corrupt peer column never makes a bound unsound.
func safeMin(vals ...int) int {
	m := vals[0]
	for _, n := range vals[1:] {
		if n < m {
			m = n
		}
	}
	return m
}

// checkColumnLengths emits one CodeColumnLength per inconsistent parallel
// column group in fixed table order: Node, Compare, Group, EvidenceNode,
// Value, EvidenceKind, EvidenceState, Outcome, Remediation, Clause, then
// Requirement.
func (v *Validator) checkColumnLengths(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	n := len(doc.NodeKinds)
	if len(doc.NodeRefs) != n || len(doc.SourceStarts) != n || len(doc.SourceEnds) != n {
		dst = append(dst, Diagnostic{Code: CodeColumnLength, Table: TableNode})
	}
	base := len(doc.CompareFields)
	if len(doc.CompareOps) != base || len(doc.CompareValues) != base ||
		len(doc.CompareListStarts) != base || len(doc.CompareListCounts) != base {
		dst = append(dst, Diagnostic{Code: CodeColumnLength, Table: TableCompare})
	}
	if len(doc.GroupChildStarts) != len(doc.GroupChildCounts) {
		dst = append(dst, Diagnostic{Code: CodeColumnLength, Table: TableGroup})
	}
	if len(doc.EvidenceKinds) != len(doc.EvidenceStates) ||
		(len(doc.EvidenceSubjects) != 0 && len(doc.EvidenceKinds) != len(doc.EvidenceSubjects)) ||
		(len(doc.EvidenceScopes) != 0 && len(doc.EvidenceKinds) != len(doc.EvidenceScopes)) ||
		(len(doc.EvidenceTimings) != 0 && len(doc.EvidenceKinds) != len(doc.EvidenceTimings)) {
		dst = append(dst, Diagnostic{Code: CodeColumnLength, Table: TableEvidenceNode})
	}
	if len(doc.ValueKinds) != len(doc.ValueRefs) || len(doc.SymbolStarts) != len(doc.SymbolLengths) {
		dst = append(dst, Diagnostic{Code: CodeColumnLength, Table: TableValue})
	}
	if len(doc.EvidenceKindNames) != len(doc.EvidenceKindSourceStarts) ||
		len(doc.EvidenceKindNames) != len(doc.EvidenceKindSourceEnds) {
		dst = append(dst, Diagnostic{Code: CodeColumnLength, Table: TableEvidenceKind})
	}
	if len(doc.EvidenceStateNames) != len(doc.EvidenceStateSourceStarts) ||
		len(doc.EvidenceStateNames) != len(doc.EvidenceStateSourceEnds) {
		dst = append(dst, Diagnostic{Code: CodeColumnLength, Table: TableEvidenceState})
	}
	if len(doc.OutcomeNames) != len(doc.OutcomePrecedence) ||
		len(doc.OutcomeNames) != len(doc.OutcomeTerminal) ||
		len(doc.OutcomeNames) != len(doc.OutcomeSourceStarts) ||
		len(doc.OutcomeNames) != len(doc.OutcomeSourceEnds) {
		dst = append(dst, Diagnostic{Code: CodeColumnLength, Table: TableOutcome})
	}
	if len(doc.RemediationKinds) != len(doc.RemediationFields) ||
		len(doc.RemediationKinds) != len(doc.RemediationValues) ||
		len(doc.RemediationKinds) != len(doc.RemediationEvidenceKinds) ||
		len(doc.RemediationKinds) != len(doc.RemediationSourceStarts) ||
		len(doc.RemediationKinds) != len(doc.RemediationSourceEnds) {
		dst = append(dst, Diagnostic{Code: CodeColumnLength, Table: TableRemediation})
	}
	if len(doc.ClauseEvidenceStarts) != len(doc.ClauseAssertionRoots) ||
		len(doc.ClauseEvidenceCounts) != len(doc.ClauseAssertionRoots) ||
		len(doc.ClauseRemediationStarts) != len(doc.ClauseAssertionRoots) ||
		len(doc.ClauseRemediationCounts) != len(doc.ClauseAssertionRoots) ||
		len(doc.ClauseOnSatisfied) != len(doc.ClauseAssertionRoots) ||
		len(doc.ClauseOnFalse) != len(doc.ClauseAssertionRoots) ||
		len(doc.ClauseOnMissing) != len(doc.ClauseAssertionRoots) ||
		len(doc.ClauseOnStale) != len(doc.ClauseAssertionRoots) ||
		len(doc.ClauseOnUnclear) != len(doc.ClauseAssertionRoots) ||
		len(doc.ClauseOnUnverifiable) != len(doc.ClauseAssertionRoots) ||
		len(doc.ClauseOnConflict) != len(doc.ClauseAssertionRoots) ||
		len(doc.ClauseSourceStarts) != len(doc.ClauseAssertionRoots) ||
		len(doc.ClauseSourceEnds) != len(doc.ClauseAssertionRoots) {
		dst = append(dst, Diagnostic{Code: CodeColumnLength, Table: TableClause})
	}
	if len(doc.RequirementApplicabilityRoots) != len(doc.RequirementIDs) ||
		len(doc.RequirementClauseStarts) != len(doc.RequirementIDs) ||
		len(doc.RequirementClauseCounts) != len(doc.RequirementIDs) ||
		len(doc.RequirementSourceStarts) != len(doc.RequirementIDs) ||
		len(doc.RequirementSourceEnds) != len(doc.RequirementIDs) {
		dst = append(dst, Diagnostic{Code: CodeColumnLength, Table: TableRequirement})
	}
	return dst
}

// literalKind reports whether k is a value literal an expression can store.
// Presence is a field column, not a literal.
func literalKind(k schema.ValueKind) bool {
	return k >= schema.ValueKindSymbol && k <= schema.ValueKindTimestamp
}

// ordered reports whether k supports the ordered comparisons: only integer
// and timestamp columns have a total order in the batch layout.
func ordered(k schema.ValueKind) bool {
	return k == schema.ValueKindInteger || k == schema.ValueKindTimestamp
}

// literalValueKind returns the stored literal kind of a structurally valid
// value row, or ok=false when id is zero, out of range of either value peer
// column, or its stored kind is not a literal kind. Rows rejected here were
// already diagnosed structurally, so type checks never cascade onto them.
func literalValueKind(doc *ast.Document, id schema.ValueID) (schema.ValueKind, bool) {
	if id == 0 || uint64(id) > uint64(len(doc.ValueKinds)) || uint64(id) > uint64(len(doc.ValueRefs)) {
		return 0, false
	}
	kind := doc.ValueKinds[id-1]
	if !literalKind(kind) {
		return 0, false
	}
	return kind, true
}

// structuralLiteralKind returns the stored literal kind of a structurally valid
// value row named by id, or ok=false when the row is not a structurally valid
// literal: a zero or out-of-range ID, a truncated value peer column, a
// non-literal stored kind, or an invalid payload ref/range all yield false so
// a catalog name or remediation value referencing the row stays
// structural-only. Unlike
// literalValueKind it also verifies the payload reference, because a catalog
// name or remediation value must point at a value row the structural phase
// accepted.
func structuralLiteralKind(doc *ast.Document, id schema.ValueID) (schema.ValueKind, bool) {
	if id == 0 || uint64(id) > uint64(len(doc.ValueKinds)) || uint64(id) > uint64(len(doc.ValueRefs)) {
		return 0, false
	}
	kind := doc.ValueKinds[id-1]
	if !literalKind(kind) {
		return 0, false
	}
	if !valueRefValid(doc, kind, doc.ValueRefs[id-1]) {
		return 0, false
	}
	return kind, true
}

// checkValueRows validates each value row up to the safe parallel length: the
// kind must be a literal and the payload reference must index the matching
// payload column within bounds.
func (v *Validator) checkValueRows(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	rows := minInt(len(doc.ValueKinds), len(doc.ValueRefs))
	for i := 0; i < rows; i++ {
		row := uint32(i + 1)
		id := schema.ValueID(i + 1)
		kind := doc.ValueKinds[i]
		if !literalKind(kind) {
			dst = append(dst, Diagnostic{Code: CodeInvalidValue, Table: TableValue, Row: row, Value: id})
			continue
		}
		if !valueRefValid(doc, kind, doc.ValueRefs[i]) {
			dst = append(dst, Diagnostic{Code: CodeInvalidPayloadRef, Table: TableValue, Row: row, Value: id})
		}
	}
	return dst
}

// valueRefValid reports whether ref indexes a valid payload row for kind.
func valueRefValid(doc *ast.Document, kind schema.ValueKind, ref uint32) bool {
	switch kind {
	case schema.ValueKindSymbol:
		if uint64(ref) >= uint64(len(doc.SymbolStarts)) || uint64(ref) >= uint64(len(doc.SymbolLengths)) {
			return false
		}
		return validRange(doc.SymbolStarts[ref], doc.SymbolLengths[ref], len(doc.SymbolBytes))
	case schema.ValueKindInteger:
		return uint64(ref) < uint64(len(doc.IntegerValues))
	case schema.ValueKindBoolean:
		return uint64(ref) < uint64(len(doc.BooleanValues))
	case schema.ValueKindTimestamp:
		return uint64(ref) < uint64(len(doc.TimestampValues))
	}
	return false
}

// checkCompareRows validates each compare row up to the safe parallel length:
// the field must index the schema, a scalar ValueID is zero or in bounds, and
// any list range must be a valid CSR range whose elements are nonzero and in
// bounds. It inspects neither the compare operation nor value kinds; arity and
// type compatibility belong to the semantic phase.
func (v *Validator) checkCompareRows(dst []Diagnostic, doc *ast.Document, fields *schema.Schema) []Diagnostic {
	rows := safeMin(len(doc.CompareFields), len(doc.CompareOps), len(doc.CompareValues),
		len(doc.CompareListStarts), len(doc.CompareListCounts))
	valueCount := len(doc.ValueKinds)
	listTotal := len(doc.ListValueIDs)
	fieldMax := uint64(fields.Len())
	for i := 0; i < rows; i++ {
		row := uint32(i + 1)
		if field := uint64(doc.CompareFields[i]); field == 0 || field > fieldMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidField, Table: TableCompare, Row: row, Field: doc.CompareFields[i]})
		}
		if value := uint64(doc.CompareValues[i]); value != 0 && value > uint64(valueCount) {
			dst = append(dst, Diagnostic{Code: CodeInvalidValue, Table: TableCompare, Row: row, Value: doc.CompareValues[i]})
		}
		start := doc.CompareListStarts[i]
		count := uint32(doc.CompareListCounts[i])
		if !validRange(start, count, listTotal) {
			dst = append(dst, Diagnostic{Code: CodeInvalidCSRRange, Table: TableCompare, Row: row})
			continue
		}
		for j := uint32(0); j < count; j++ {
			if v := uint64(doc.ListValueIDs[int(start)+int(j)]); v == 0 || v > uint64(valueCount) {
				dst = append(dst, Diagnostic{Code: CodeInvalidValue, Table: TableCompare, Row: row, Value: doc.ListValueIDs[int(start)+int(j)]})
			}
		}
	}
	return dst
}

// checkGroupRows validates each All/Any payload row up to the safe parallel
// length: the CSR range must fit ChildNodeIDs, and only within a valid range
// are child IDs required to be nonzero and within the node table. An invalid
// range never inspects the child column, so a corrupt or truncated edge
// column cannot panic or leak reference diagnostics.
func (v *Validator) checkGroupRows(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	rows := minInt(len(doc.GroupChildStarts), len(doc.GroupChildCounts))
	nodeMax := uint64(len(doc.NodeKinds))
	for i := 0; i < rows; i++ {
		row := uint32(i + 1)
		start := doc.GroupChildStarts[i]
		count := uint32(doc.GroupChildCounts[i])
		if !validRange(start, count, len(doc.ChildNodeIDs)) {
			dst = append(dst, Diagnostic{Code: CodeInvalidCSRRange, Table: TableGroup, Row: row})
			continue
		}
		for j := uint32(0); j < count; j++ {
			if child := uint64(doc.ChildNodeIDs[int(start)+int(j)]); child == 0 || child > nodeMax {
				dst = append(dst, Diagnostic{Code: CodeInvalidNodeReference, Table: TableGroup, Row: row, Node: schema.NodeID(child)})
			}
		}
	}
	return dst
}

// checkNotRows validates each negation payload row: the target must be
// nonzero and within the node table. A bad target never marks the Not node
// itself unsafe; the graph phase skips the invalid edge.
func (v *Validator) checkNotRows(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	nodeMax := uint64(len(doc.NodeKinds))
	for i, target := range doc.NotChildren {
		row := uint32(i + 1)
		if t := uint64(target); t == 0 || t > nodeMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidNodeReference, Table: TableNot, Row: row, Node: target})
		}
	}
	return dst
}

// checkEvidenceRows validates each evidence payload row up to the safe
// parallel length: kind and state must each be nonzero and index their
// catalog. The two are independent same-row diagnostics, kind before state.
// Each carries its Member plus the actual offending EvidenceKind or
// EvidenceState so the records are unambiguous even when both are invalid.
// There is no evidence-ID column, so the owner remains the TableEvidenceNode
// row.
func (v *Validator) checkEvidenceRows(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	rows := minInt(len(doc.EvidenceKinds), len(doc.EvidenceStates))
	kindMax := uint64(len(doc.EvidenceKindNames))
	stateMax := uint64(len(doc.EvidenceStateNames))
	for i := 0; i < rows; i++ {
		row := uint32(i + 1)
		if kind := uint64(doc.EvidenceKinds[i]); kind == 0 || kind > kindMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidEvidence, Table: TableEvidenceNode, Member: MemberEvidenceKind, Row: row, EvidenceKind: doc.EvidenceKinds[i]})
		}
		if state := uint64(doc.EvidenceStates[i]); state == 0 || state > stateMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidEvidence, Table: TableEvidenceNode, Member: MemberEvidenceState, Row: row, EvidenceState: doc.EvidenceStates[i]})
		}
		var qualifiers [3]schema.ValueID
		if i < len(doc.EvidenceSubjects) {
			qualifiers[0] = doc.EvidenceSubjects[i]
		}
		if i < len(doc.EvidenceScopes) {
			qualifiers[1] = doc.EvidenceScopes[i]
		}
		if i < len(doc.EvidenceTimings) {
			qualifiers[2] = doc.EvidenceTimings[i]
		}
		for _, value := range qualifiers {
			if value != 0 {
				kind, ok := structuralLiteralKind(doc, value)
				if !ok || kind != schema.ValueKindSymbol {
					dst = append(dst, Diagnostic{Code: CodeInvalidValue, Table: TableEvidenceNode, Row: row, Value: value})
				}
			}
		}
	}
	return dst
}

// nodeRefValid reports whether ref indexes a safe row in the payload table
// named by kind. Each payload table's row count is the minimum over its
// parallel columns, so a corrupt column never makes a bound unsound.
func nodeRefValid(doc *ast.Document, kind ast.NodeKind, ref uint32) bool {
	switch kind {
	case ast.NodeKindCompare:
		return uint64(ref) < uint64(safeMin(
			len(doc.CompareFields), len(doc.CompareOps), len(doc.CompareValues),
			len(doc.CompareListStarts), len(doc.CompareListCounts)))
	case ast.NodeKindAll, ast.NodeKindAny:
		return uint64(ref) < uint64(minInt(len(doc.GroupChildStarts), len(doc.GroupChildCounts)))
	case ast.NodeKindNot:
		return uint64(ref) < uint64(len(doc.NotChildren))
	case ast.NodeKindEvidence:
		return uint64(ref) < uint64(minInt(len(doc.EvidenceKinds), len(doc.EvidenceStates)))
	}
	return false
}

// checkNodeRows validates each node row ascending by NodeID: source span,
// node kind, then payload reference. A row is marked unsafe when any peer
// column is missing, the kind is invalid, or the reference is invalid, so
// later graph traversal never dereferences a corrupt row.
func (v *Validator) checkNodeRows(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	for i := range doc.NodeKinds {
		row := uint32(i + 1)
		id := schema.NodeID(i + 1)
		unsafe := i >= len(doc.NodeRefs) || i >= len(doc.SourceStarts) || i >= len(doc.SourceEnds)

		var span ast.SourceSpan
		spanOK := false
		if i < len(doc.SourceStarts) && i < len(doc.SourceEnds) {
			span = ast.SourceSpan{Start: doc.SourceStarts[i], End: doc.SourceEnds[i]}
			spanOK = span.Start <= span.End && uint64(span.End) <= uint64(len(doc.InputBytes))
			if !spanOK {
				dst = append(dst, Diagnostic{Code: CodeInvalidSourceSpan, Table: TableNode, Row: row, Node: id})
			}
		}
		attach := span
		if !spanOK {
			attach = ast.SourceSpan{}
		}

		kind := doc.NodeKinds[i]
		if !kind.Valid() {
			unsafe = true
			dst = append(dst, Diagnostic{Code: CodeInvalidNodeKind, Table: TableNode, Row: row, Node: id, Span: attach})
		} else if i < len(doc.NodeRefs) && !nodeRefValid(doc, kind, doc.NodeRefs[i]) {
			unsafe = true
			dst = append(dst, Diagnostic{Code: CodeInvalidPayloadRef, Table: TableNode, Row: row, Node: id, Span: attach})
		} else if (kind == ast.NodeKindAll || kind == ast.NodeKindAny) && i < len(doc.NodeRefs) {
			// The reference is known valid here, so its group row exists in
			// both peer columns; an invalid CSR range on that row poisons the
			// node without appending a second diagnostic for it.
			r := doc.NodeRefs[i]
			if !validRange(doc.GroupChildStarts[r], uint32(doc.GroupChildCounts[r]), len(doc.ChildNodeIDs)) {
				unsafe = true
			}
		}

		if unsafe {
			v.nodeState[i] |= nodeStateUnsafe
		}
	}
	return dst
}

// nameInValueKinds reports whether name is a nonzero ValueID inside the value
// table. Whether the named value is a symbol belongs to the semantic phase.
func nameInValueKinds(doc *ast.Document, name schema.ValueID) bool {
	return name != 0 && uint64(name) <= uint64(len(doc.ValueKinds))
}

// validOwnerSpan validates a catalog row's half-open source range against the
// input length. An invalid range yields a zero span and false so non-span
// diagnostics attach no span.
func validOwnerSpan(start, end uint32, inputBytes int) (ast.SourceSpan, bool) {
	if !(start <= end && uint64(end) <= uint64(inputBytes)) {
		return ast.SourceSpan{}, false
	}
	return ast.SourceSpan{Start: start, End: end}, true
}

// checkEvidenceKindRows validates each evidence-kind catalog row up to the
// safe parallel length: the name ValueID must be nonzero and inside the value
// table, and the source span must fit the input. The name check precedes the
// span check; a valid owner span is attached to the name diagnostic only.
func (v *Validator) checkEvidenceKindRows(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	rows := safeMin(len(doc.EvidenceKindNames), len(doc.EvidenceKindSourceStarts), len(doc.EvidenceKindSourceEnds))
	for i := 0; i < rows; i++ {
		row := uint32(i + 1)
		id := schema.EvidenceKindID(i + 1)
		span, spanOK := validOwnerSpan(doc.EvidenceKindSourceStarts[i], doc.EvidenceKindSourceEnds[i], len(doc.InputBytes))
		attach := span
		if !spanOK {
			attach = ast.SourceSpan{}
		}
		if name := doc.EvidenceKindNames[i]; !nameInValueKinds(doc, name) {
			dst = append(dst, Diagnostic{Code: CodeInvalidValue, Table: TableEvidenceKind, Member: MemberName, Row: row, Span: attach, EvidenceKind: id, Value: name})
		}
		if !spanOK {
			dst = append(dst, Diagnostic{Code: CodeInvalidSourceSpan, Table: TableEvidenceKind, Member: MemberSpan, Row: row, EvidenceKind: id})
		}
	}
	return dst
}

// checkEvidenceStateRows validates each evidence-state catalog row up to the
// safe parallel length with the same name-then-span rule as evidence kinds.
func (v *Validator) checkEvidenceStateRows(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	rows := safeMin(len(doc.EvidenceStateNames), len(doc.EvidenceStateSourceStarts), len(doc.EvidenceStateSourceEnds))
	for i := 0; i < rows; i++ {
		row := uint32(i + 1)
		id := schema.EvidenceStateID(i + 1)
		span, spanOK := validOwnerSpan(doc.EvidenceStateSourceStarts[i], doc.EvidenceStateSourceEnds[i], len(doc.InputBytes))
		attach := span
		if !spanOK {
			attach = ast.SourceSpan{}
		}
		if name := doc.EvidenceStateNames[i]; !nameInValueKinds(doc, name) {
			dst = append(dst, Diagnostic{Code: CodeInvalidValue, Table: TableEvidenceState, Member: MemberName, Row: row, Span: attach, EvidenceState: id, Value: name})
		}
		if !spanOK {
			dst = append(dst, Diagnostic{Code: CodeInvalidSourceSpan, Table: TableEvidenceState, Member: MemberSpan, Row: row, EvidenceState: id})
		}
	}
	return dst
}

// checkOutcomeRows validates each outcome catalog row up to the safe parallel
// length: a nonzero in-table name ValueID and a valid source span, name check
// before span check, owner Outcome ID.
func (v *Validator) checkOutcomeRows(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	rows := safeMin(len(doc.OutcomeNames), len(doc.OutcomePrecedence), len(doc.OutcomeTerminal),
		len(doc.OutcomeSourceStarts), len(doc.OutcomeSourceEnds))
	for i := 0; i < rows; i++ {
		row := uint32(i + 1)
		id := schema.OutcomeID(i + 1)
		span, spanOK := validOwnerSpan(doc.OutcomeSourceStarts[i], doc.OutcomeSourceEnds[i], len(doc.InputBytes))
		attach := span
		if !spanOK {
			attach = ast.SourceSpan{}
		}
		if name := doc.OutcomeNames[i]; !nameInValueKinds(doc, name) {
			dst = append(dst, Diagnostic{Code: CodeInvalidValue, Table: TableOutcome, Member: MemberName, Row: row, Span: attach, Outcome: id, Value: name})
		}
		if !spanOK {
			dst = append(dst, Diagnostic{Code: CodeInvalidSourceSpan, Table: TableOutcome, Member: MemberSpan, Row: row, Outcome: id})
		}
	}
	return dst
}

// checkRemediationRows validates each remediation row up to the safe parallel
// length. It does not inspect the remediation kind or payload shape yet; that
// belongs to the semantic phase. A nonzero field, value, or evidence-kind ID
// must index its table; zero is structurally allowed. The source span is
// checked after the reference checks, and a valid owner span is attached to
// the reference diagnostics only.
func (v *Validator) checkRemediationRows(dst []Diagnostic, doc *ast.Document, fields *schema.Schema) []Diagnostic {
	rows := safeMin(len(doc.RemediationKinds), len(doc.RemediationFields), len(doc.RemediationValues),
		len(doc.RemediationEvidenceKinds), len(doc.RemediationSourceStarts), len(doc.RemediationSourceEnds))
	fieldMax := uint64(fields.Len())
	valueMax := uint64(len(doc.ValueKinds))
	evidenceMax := uint64(len(doc.EvidenceKindNames))
	for i := 0; i < rows; i++ {
		row := uint32(i + 1)
		id := schema.RemediationID(i + 1)
		span, spanOK := validOwnerSpan(doc.RemediationSourceStarts[i], doc.RemediationSourceEnds[i], len(doc.InputBytes))
		attach := span
		if !spanOK {
			attach = ast.SourceSpan{}
		}
		if field := uint64(doc.RemediationFields[i]); field != 0 && field > fieldMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidField, Table: TableRemediation, Member: MemberField, Row: row, Span: attach, Remediation: id, Field: doc.RemediationFields[i]})
		}
		if value := uint64(doc.RemediationValues[i]); value != 0 && value > valueMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidValue, Table: TableRemediation, Member: MemberValue, Row: row, Span: attach, Remediation: id, Value: doc.RemediationValues[i]})
		}
		if kind := uint64(doc.RemediationEvidenceKinds[i]); kind != 0 && kind > evidenceMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidEvidence, Table: TableRemediation, Member: MemberEvidenceKind, Row: row, Span: attach, Remediation: id, EvidenceKind: doc.RemediationEvidenceKinds[i]})
		}
		if !spanOK {
			dst = append(dst, Diagnostic{Code: CodeInvalidSourceSpan, Table: TableRemediation, Member: MemberSpan, Row: row, Remediation: id})
		}
	}
	return dst
}

// checkClauseRows validates each clause row up to the safe parallel length of
// all clause columns, then marks every row beyond that minimum up to
// len(ClauseAssertionRoots) unsafe so traversal never dereferences a corrupt
// row. Per safe row the checks run in fixed order: assertion root, evidence
// CSR and edge IDs, remediation CSR and edge IDs, the seven outcome slots,
// then the source span (reported last, attached to the other diagnostics).
// The assertion root and both CSR ranges mark the clause unsafe; a bad edge
// target ID never does because the graph can skip the individual edge. Zero
// outcome IDs are structurally allowed; zero and out-of-range resolution
// semantics belong to the semantic phase.
func (v *Validator) checkClauseRows(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	rows := safeMin(
		len(doc.ClauseAssertionRoots),
		len(doc.ClauseEvidenceStarts), len(doc.ClauseEvidenceCounts),
		len(doc.ClauseRemediationStarts), len(doc.ClauseRemediationCounts),
		len(doc.ClauseOnSatisfied), len(doc.ClauseOnFalse), len(doc.ClauseOnMissing),
		len(doc.ClauseOnStale), len(doc.ClauseOnUnclear), len(doc.ClauseOnUnverifiable),
		len(doc.ClauseOnConflict), len(doc.ClauseSourceStarts), len(doc.ClauseSourceEnds))
	nodeMax := uint64(len(doc.NodeKinds))
	remediationMax := uint64(len(doc.RemediationKinds))
	outcomeMax := uint64(len(doc.OutcomeNames))
	for i := 0; i < rows; i++ {
		row := uint32(i + 1)
		id := schema.ClauseID(i + 1)
		span, spanOK := validOwnerSpan(doc.ClauseSourceStarts[i], doc.ClauseSourceEnds[i], len(doc.InputBytes))
		attach := span
		if !spanOK {
			attach = ast.SourceSpan{}
		}
		unsafe := false

		if a := uint64(doc.ClauseAssertionRoots[i]); a == 0 || a > nodeMax {
			unsafe = true
			dst = append(dst, Diagnostic{Code: CodeInvalidNodeReference, Table: TableClause, Member: MemberAssertion, Row: row, Clause: id, Node: doc.ClauseAssertionRoots[i], Span: attach})
		}

		if start, count := doc.ClauseEvidenceStarts[i], uint32(doc.ClauseEvidenceCounts[i]); !validRange(start, count, len(doc.ClauseEvidenceNodeIDs)) {
			unsafe = true
			dst = append(dst, Diagnostic{Code: CodeInvalidCSRRange, Table: TableClause, Member: MemberEvidence, Row: row, Clause: id, Span: attach})
		} else {
			for j := uint32(0); j < count; j++ {
				if e := uint64(doc.ClauseEvidenceNodeIDs[int(start)+int(j)]); e == 0 || e > nodeMax {
					dst = append(dst, Diagnostic{Code: CodeInvalidNodeReference, Table: TableClause, Member: MemberEvidence, Row: row, Clause: id, Node: doc.ClauseEvidenceNodeIDs[int(start)+int(j)], Span: attach})
				}
			}
		}

		if start, count := doc.ClauseRemediationStarts[i], uint32(doc.ClauseRemediationCounts[i]); !validRange(start, count, len(doc.ClauseRemediationIDs)) {
			unsafe = true
			dst = append(dst, Diagnostic{Code: CodeInvalidCSRRange, Table: TableClause, Member: MemberRemediations, Row: row, Clause: id, Span: attach})
		} else {
			for j := uint32(0); j < count; j++ {
				if r := uint64(doc.ClauseRemediationIDs[int(start)+int(j)]); r == 0 || r > remediationMax {
					dst = append(dst, Diagnostic{Code: CodeInvalidRemediation, Table: TableClause, Member: MemberRemediation, Row: row, Clause: id, Remediation: doc.ClauseRemediationIDs[int(start)+int(j)], Span: attach})
				}
			}
		}

		if o := uint64(doc.ClauseOnSatisfied[i]); o != 0 && o > outcomeMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeSatisfied, Row: row, Clause: id, Outcome: doc.ClauseOnSatisfied[i], Span: attach})
		}
		if o := uint64(doc.ClauseOnFalse[i]); o != 0 && o > outcomeMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeFalse, Row: row, Clause: id, Outcome: doc.ClauseOnFalse[i], Span: attach})
		}
		if o := uint64(doc.ClauseOnMissing[i]); o != 0 && o > outcomeMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeMissing, Row: row, Clause: id, Outcome: doc.ClauseOnMissing[i], Span: attach})
		}
		if o := uint64(doc.ClauseOnStale[i]); o != 0 && o > outcomeMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeStale, Row: row, Clause: id, Outcome: doc.ClauseOnStale[i], Span: attach})
		}
		if o := uint64(doc.ClauseOnUnclear[i]); o != 0 && o > outcomeMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeUnclear, Row: row, Clause: id, Outcome: doc.ClauseOnUnclear[i], Span: attach})
		}
		if o := uint64(doc.ClauseOnUnverifiable[i]); o != 0 && o > outcomeMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeUnverifiable, Row: row, Clause: id, Outcome: doc.ClauseOnUnverifiable[i], Span: attach})
		}
		if o := uint64(doc.ClauseOnConflict[i]); o != 0 && o > outcomeMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeConflict, Row: row, Clause: id, Outcome: doc.ClauseOnConflict[i], Span: attach})
		}

		if !spanOK {
			dst = append(dst, Diagnostic{Code: CodeInvalidSourceSpan, Table: TableClause, Member: MemberSpan, Row: row, Clause: id})
		}
		if unsafe {
			v.clauseState[i] |= clauseStateUnsafe
		}
	}
	for i := rows; i < len(doc.ClauseAssertionRoots); i++ {
		v.clauseState[i] |= clauseStateUnsafe
	}
	return dst
}

// checkRequirementRows validates each requirement row up to the safe parallel
// length of all six requirement columns. A zero RequirementID is structurally
// allowed; the applicability root must be nonzero and inside the node table,
// the clause CSR range must fit RequirementClauseIDs, and each ClauseID inside
// a valid range must be nonzero and within the clause table. The source span
// is checked last and a valid owner span is attached to the reference
// diagnostics. There is no requirement state array: only clause rows carry
// unsafe state for traversal.
func (v *Validator) checkRequirementRows(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	rows := safeMin(
		len(doc.RequirementIDs), len(doc.RequirementApplicabilityRoots),
		len(doc.RequirementClauseStarts), len(doc.RequirementClauseCounts),
		len(doc.RequirementSourceStarts), len(doc.RequirementSourceEnds))
	nodeMax := uint64(len(doc.NodeKinds))
	clauseMax := uint64(len(doc.ClauseAssertionRoots))
	for i := 0; i < rows; i++ {
		row := uint32(i + 1)
		req := doc.RequirementIDs[i]
		span, spanOK := validOwnerSpan(doc.RequirementSourceStarts[i], doc.RequirementSourceEnds[i], len(doc.InputBytes))
		attach := span
		if !spanOK {
			attach = ast.SourceSpan{}
		}
		if a := uint64(doc.RequirementApplicabilityRoots[i]); a == 0 || a > nodeMax {
			dst = append(dst, Diagnostic{Code: CodeInvalidNodeReference, Table: TableRequirement, Member: MemberApplicability, Row: row, Requirement: req, Node: doc.RequirementApplicabilityRoots[i], Span: attach})
		}
		if start, count := doc.RequirementClauseStarts[i], uint32(doc.RequirementClauseCounts[i]); !validRange(start, count, len(doc.RequirementClauseIDs)) {
			dst = append(dst, Diagnostic{Code: CodeInvalidCSRRange, Table: TableRequirement, Member: MemberClauses, Row: row, Requirement: req, Span: attach})
		} else {
			for j := uint32(0); j < count; j++ {
				if c := uint64(doc.RequirementClauseIDs[int(start)+int(j)]); c == 0 || c > clauseMax {
					dst = append(dst, Diagnostic{Code: CodeInvalidPayloadRef, Table: TableRequirement, Member: MemberClause, Row: row, Requirement: req, Clause: doc.RequirementClauseIDs[int(start)+int(j)], Span: attach})
				}
			}
		}
		if !spanOK {
			dst = append(dst, Diagnostic{Code: CodeInvalidSourceSpan, Table: TableRequirement, Member: MemberSpan, Row: row, Requirement: req})
		}
	}
	return dst
}

// validateGraph runs the phase-3 graph checks: requirement and clause root
// reachability, tri-color cycle detection, and unreachable-node reporting. It
// walks requirements in row order, traversing each in-range applicability root
// as reachable (even when the requirement ID is semantically invalid) and
// marking every valid in-range ClauseID in a valid clause CSR as referenced.
// Then it scans clauses ascending and, for each referenced clause that is not
// structurally unsafe, traverses its assertion root followed by each valid
// evidence edge as reachable. Root traversals complete before the orphan scan:
// every remaining white safe node is traversed ascending with reachable=false
// so cycles inside orphan components are still reported. Finally it scans
// NodeID ascending and appends one CodeUnreachableNode per safe node lacking
// the reachable bit. Unsafe nodes never receive a graph diagnostic, invalid
// zero/high edges are skipped, cycle diagnostics precede unreachable
// diagnostics, reachable-root cycles precede orphan cycles, and the reusable
// traversal stack is empty on return. The pass allocates only when appending
// exceeds dst capacity or the reusable traversal stack must grow.
func (v *Validator) validateGraph(dst []Diagnostic, doc *ast.Document) []Diagnostic {
	nodeCount := len(doc.NodeKinds)
	reqRows := safeMin(
		len(doc.RequirementIDs), len(doc.RequirementApplicabilityRoots),
		len(doc.RequirementClauseStarts), len(doc.RequirementClauseCounts),
		len(doc.RequirementSourceStarts), len(doc.RequirementSourceEnds))
	clauseCount := len(doc.ClauseAssertionRoots)
	for i := 0; i < reqRows; i++ {
		dst = v.walkGraph(dst, doc, doc.RequirementApplicabilityRoots[i], true)
		start := doc.RequirementClauseStarts[i]
		count := uint32(doc.RequirementClauseCounts[i])
		if validRange(start, count, len(doc.RequirementClauseIDs)) {
			for j := uint32(0); j < count; j++ {
				if c := doc.RequirementClauseIDs[int(start)+int(j)]; c != 0 && uint64(c) <= uint64(clauseCount) {
					v.clauseState[int(c)-1] |= clauseStateReferenced
				}
			}
		}
	}
	for i := 0; i < clauseCount; i++ {
		if v.clauseState[i]&(clauseStateReferenced|clauseStateUnsafe) != clauseStateReferenced {
			continue
		}
		dst = v.walkGraph(dst, doc, doc.ClauseAssertionRoots[i], true)
		start := doc.ClauseEvidenceStarts[i]
		count := uint32(doc.ClauseEvidenceCounts[i])
		if validRange(start, count, len(doc.ClauseEvidenceNodeIDs)) {
			for j := uint32(0); j < count; j++ {
				dst = v.walkGraph(dst, doc, doc.ClauseEvidenceNodeIDs[int(start)+int(j)], true)
			}
		}
	}
	for i := 0; i < nodeCount; i++ {
		st := v.nodeState[i]
		if st&nodeStateUnsafe != 0 || st&(nodeStateGray|nodeStateBlack) != 0 {
			continue
		}
		dst = v.walkGraph(dst, doc, schema.NodeID(i+1), false)
	}
	for i := 0; i < nodeCount; i++ {
		if v.nodeState[i]&nodeStateUnsafe != 0 || v.nodeState[i]&nodeStateReachable != 0 {
			continue
		}
		dst = append(dst, Diagnostic{Code: CodeUnreachableNode, Table: TableNode, Row: uint32(i + 1), Node: schema.NodeID(i + 1), Span: validNodeSpan(doc, i)})
	}
	return dst
}

// walkGraph performs one iterative tri-color traversal of the safe node graph
// starting at root, marking visited nodes gray then black and appending exactly
// one CodeCycle per edge whose target is already gray. reachable controls
// whether newly gray nodes also receive the reachable bit, which is true for
// semantic-root traversals and false for orphan components. Compare and
// Evidence nodes are leaves, Not has one outgoing edge, and All/Any follow
// their structurally valid CSR in order. Zero or high targets and structurally
// unsafe targets are skipped because the structural phase already diagnosed
// them. On a gray target a CodeCycle is appended for the source edge and the
// target is not revisited; a black target is clean. The reusable stack holds
// one visitFrame per gray node, ending empty on return, and no pointer into
// the stack is held across an append.
func (v *Validator) walkGraph(dst []Diagnostic, doc *ast.Document, root schema.NodeID, reachable bool) []Diagnostic {
	nodeCount := len(doc.NodeKinds)
	if root == 0 || uint64(root) > uint64(nodeCount) {
		return dst
	}
	ri := int(root - 1)
	rs := v.nodeState[ri]
	if rs&nodeStateUnsafe != 0 || rs&(nodeStateGray|nodeStateBlack) != 0 {
		return dst
	}
	rs |= nodeStateGray
	if reachable {
		rs |= nodeStateReachable
	}
	v.nodeState[ri] = rs
	v.stack = append(v.stack, visitFrame{node: root})
	for len(v.stack) > 0 {
		frame := &v.stack[len(v.stack)-1]
		node := frame.node
		ni := int(node - 1)
		if count := graphEdgeCount(doc, node); frame.next < count {
			target := graphChild(doc, node, frame.next)
			frame.next++
			if target == 0 || uint64(target) > uint64(nodeCount) {
				continue
			}
			ti := int(target - 1)
			ts := v.nodeState[ti]
			if ts&nodeStateUnsafe != 0 {
				continue
			}
			if ts&nodeStateGray != 0 {
				dst = append(dst, Diagnostic{Code: CodeCycle, Table: TableNode, Member: MemberChild, Row: uint32(node), Node: target, Span: validNodeSpan(doc, ni)})
				continue
			}
			if ts&nodeStateBlack != 0 {
				continue
			}
			ns := ts | nodeStateGray
			if reachable {
				ns |= nodeStateReachable
			}
			v.nodeState[ti] = ns
			v.stack = append(v.stack, visitFrame{node: target})
			continue
		}
		v.nodeState[ni] |= nodeStateBlack
		v.nodeState[ni] &^= nodeStateGray
		v.stack = v.stack[:len(v.stack)-1]
	}
	return dst
}

// graphEdgeCount returns the number of outgoing graph edges of a structurally
// safe node: zero for Compare and Evidence leaves, one for Not, and the
// structurally valid child CSR count for All and Any groups.
func graphEdgeCount(doc *ast.Document, node schema.NodeID) uint32 {
	i := int(node - 1)
	switch doc.NodeKinds[i] {
	case ast.NodeKindNot:
		return 1
	case ast.NodeKindAll, ast.NodeKindAny:
		return uint32(doc.GroupChildCounts[doc.NodeRefs[i]])
	}
	return 0
}

// graphChild returns the target of a structurally safe node's j-th outgoing
// edge: NotChildren[ref] for Not and ChildNodeIDs[GroupChildStarts[ref]+j] for
// All/Any. The caller guarantees j is within the node's edge count.
func graphChild(doc *ast.Document, node schema.NodeID, j uint32) schema.NodeID {
	i := int(node - 1)
	switch doc.NodeKinds[i] {
	case ast.NodeKindNot:
		return doc.NotChildren[doc.NodeRefs[i]]
	case ast.NodeKindAll, ast.NodeKindAny:
		return doc.ChildNodeIDs[int(doc.GroupChildStarts[doc.NodeRefs[i]])+int(j)]
	}
	return 0
}

// validNodeSpan returns the exact valid source span of node row i, or a zero
// span when the row is missing or the range does not fit the input. An invalid
// node span never makes the node unsafe, so graph diagnostics attach a zero
// span instead.
func validNodeSpan(doc *ast.Document, i int) ast.SourceSpan {
	if i >= len(doc.SourceStarts) || i >= len(doc.SourceEnds) {
		return ast.SourceSpan{}
	}
	s := ast.SourceSpan{Start: doc.SourceStarts[i], End: doc.SourceEnds[i]}
	if s.Start <= s.End && uint64(s.End) <= uint64(len(doc.InputBytes)) {
		return s
	}
	return ast.SourceSpan{}
}
