package ast

import (
	"crypto/sha256"
	"errors"
	"math"

	"github.com/sebishogun/verifoxx/internal/schema"
)

var (
	ErrInvalidNodeKind     = errors.New("ast: invalid node kind")
	ErrInvalidCompareOp    = errors.New("ast: invalid compare operation")
	ErrInvalidField        = errors.New("ast: invalid field ID")
	ErrInvalidValue        = errors.New("ast: invalid value ID")
	ErrInvalidNode         = errors.New("ast: invalid node ID")
	ErrInvalidEvidence     = errors.New("ast: invalid evidence ID")
	ErrInvalidRequirement  = errors.New("ast: invalid requirement ID")
	ErrInvalidSourceSpan   = errors.New("ast: invalid source span")
	ErrSourceAfterRecords  = errors.New("ast: source cannot change after source-spanned records are added")
	ErrTooManyNodes        = errors.New("ast: too many nodes")
	ErrTooManyChildren     = errors.New("ast: too many group children")
	ErrTooManyRequirements = errors.New("ast: too many requirements")
	ErrSourceTooLarge      = errors.New("ast: source exceeds uint32 address space")
)

// Hints sizes each independently grown column before decoding begins.
type Hints struct {
	Nodes                  int
	Templates              int
	TemplateOps            int
	TemplateBytes          int
	Assumptions            int
	Explanations           int
	ExplanationUncertainty int
	CompareNodes           int
	CompareListValues      int
	GroupNodes             int
	ChildEdges             int
	NotNodes               int
	EvidenceNodes          int
	Values                 int
	SymbolValues           int
	SymbolBytes            int
	IntegerValues          int
	BooleanValues          int
	TimestampValues        int
	EvidenceKinds          int
	EvidenceStates         int
	Outcomes               int
	Remediations           int
	Clauses                int
	ClauseEvidenceEdges    int
	ClauseRemediationEdges int
	Requirements           int
	RequirementClauseEdges int
	SourceBytes            int
}

func nonNegative(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func multipliedHint(n, factor int) int {
	if n <= 0 || factor <= 0 || n > math.MaxInt/factor {
		return 0
	}
	return n * factor
}

// Builder owns one mutable Document. It is not safe for concurrent use.
type Builder struct {
	doc Document
}

// NewBuilder returns a builder with all columns pre-sized from hints.
func NewBuilder(hints Hints) *Builder {
	nodes := nonNegative(hints.Nodes)
	templates := nonNegative(hints.Templates)
	templateOps := nonNegative(hints.TemplateOps)
	templateBytes := nonNegative(hints.TemplateBytes)
	assumptions := nonNegative(hints.Assumptions)
	explanations := nonNegative(hints.Explanations)
	explanationUncertainty := nonNegative(hints.ExplanationUncertainty)
	compares := nonNegative(hints.CompareNodes)
	compareValues := nonNegative(hints.CompareListValues)
	groups := nonNegative(hints.GroupNodes)
	children := nonNegative(hints.ChildEdges)
	nots := nonNegative(hints.NotNodes)
	evidence := nonNegative(hints.EvidenceNodes)
	evidenceIssues := multipliedHint(evidence, EvidenceIssueReasonCount)
	values := nonNegative(hints.Values)
	symbolValues := nonNegative(hints.SymbolValues)
	symbolBytes := nonNegative(hints.SymbolBytes)
	integers := nonNegative(hints.IntegerValues)
	booleans := nonNegative(hints.BooleanValues)
	timestamps := nonNegative(hints.TimestampValues)
	evidenceKinds := nonNegative(hints.EvidenceKinds)
	evidenceStates := nonNegative(hints.EvidenceStates)
	outcomes := nonNegative(hints.Outcomes)
	remediations := nonNegative(hints.Remediations)
	clauses := nonNegative(hints.Clauses)
	clauseExplanations := multipliedHint(clauses, ResolutionBranchCount)
	clauseEvidence := nonNegative(hints.ClauseEvidenceEdges)
	clauseRemediations := nonNegative(hints.ClauseRemediationEdges)
	requirements := nonNegative(hints.Requirements)
	requirementClauses := nonNegative(hints.RequirementClauseEdges)
	source := nonNegative(hints.SourceBytes)
	return &Builder{doc: Document{
		NodeKinds:                     make([]NodeKind, 0, nodes),
		NodeRefs:                      make([]uint32, 0, nodes),
		TemplateBytes:                 make([]byte, 0, templateBytes),
		TemplateOpStarts:              make([]uint32, 0, templates),
		TemplateOpCounts:              make([]uint16, 0, templates),
		TemplateLiteralStarts:         make([]uint32, 0, templates),
		TemplateMaxBytes:              make([]uint32, 0, templates),
		TemplateContexts:              make([]TemplateContext, 0, templates),
		TemplateOps:                   make([]TemplateOp, 0, templateOps),
		TemplateArgs:                  make([]uint32, 0, templateOps),
		AssumptionTemplateIDs:         make([]schema.TemplateID, 0, assumptions),
		AssumptionsSet:                make([]uint8, 0, 1),
		ExplanationRationaleIDs:       make([]schema.TemplateID, 0, explanations),
		ExplanationUncertaintyStarts:  make([]uint32, 0, explanations),
		ExplanationUncertaintyCounts:  make([]uint16, 0, explanations),
		ExplanationUncertaintyIDs:     make([]schema.TemplateID, 0, explanationUncertainty),
		CompareFields:                 make([]schema.FieldID, 0, compares),
		CompareOps:                    make([]CompareOp, 0, compares),
		CompareValues:                 make([]schema.ValueID, 0, compares),
		CompareListStarts:             make([]uint32, 0, compares),
		CompareListCounts:             make([]uint16, 0, compares),
		ListValueIDs:                  make([]schema.ValueID, 0, compareValues),
		GroupChildStarts:              make([]uint32, 0, groups),
		GroupChildCounts:              make([]uint16, 0, groups),
		ChildNodeIDs:                  make([]schema.NodeID, 0, children),
		NotChildren:                   make([]schema.NodeID, 0, nots),
		EvidenceKinds:                 make([]schema.EvidenceKindID, 0, evidence),
		EvidenceStates:                make([]schema.EvidenceStateID, 0, evidence),
		EvidenceSubjects:              make([]schema.ValueID, 0, evidence),
		EvidenceScopes:                make([]schema.ValueID, 0, evidence),
		EvidenceTimings:               make([]schema.ValueID, 0, evidence),
		EvidenceIssueTemplateIDs:      make([]schema.TemplateID, 0, evidenceIssues),
		SourceStarts:                  make([]uint32, 0, nodes),
		SourceEnds:                    make([]uint32, 0, nodes),
		InputBytes:                    make([]byte, 0, source),
		ValueKinds:                    make([]schema.ValueKind, 0, values),
		ValueRefs:                     make([]uint32, 0, values),
		SymbolStarts:                  make([]uint32, 0, symbolValues),
		SymbolLengths:                 make([]uint32, 0, symbolValues),
		SymbolBytes:                   make([]byte, 0, symbolBytes),
		IntegerValues:                 make([]int64, 0, integers),
		BooleanValues:                 make([]uint8, 0, booleans),
		TimestampValues:               make([]int64, 0, timestamps),
		EvidenceKindNames:             make([]schema.ValueID, 0, evidenceKinds),
		EvidenceKindSourceStarts:      make([]uint32, 0, evidenceKinds),
		EvidenceKindSourceEnds:        make([]uint32, 0, evidenceKinds),
		EvidenceStateNames:            make([]schema.ValueID, 0, evidenceStates),
		EvidenceStateSourceStarts:     make([]uint32, 0, evidenceStates),
		EvidenceStateSourceEnds:       make([]uint32, 0, evidenceStates),
		OutcomeNames:                  make([]schema.ValueID, 0, outcomes),
		OutcomePrecedence:             make([]uint8, 0, outcomes),
		OutcomeTerminal:               make([]bool, 0, outcomes),
		OutcomeSourceStarts:           make([]uint32, 0, outcomes),
		OutcomeSourceEnds:             make([]uint32, 0, outcomes),
		RemediationKinds:              make([]RemediationKind, 0, remediations),
		RemediationFields:             make([]schema.FieldID, 0, remediations),
		RemediationValues:             make([]schema.ValueID, 0, remediations),
		RemediationEvidenceKinds:      make([]schema.EvidenceKindID, 0, remediations),
		RemediationSourceStarts:       make([]uint32, 0, remediations),
		RemediationSourceEnds:         make([]uint32, 0, remediations),
		ClauseAssertionRoots:          make([]schema.NodeID, 0, clauses),
		ClauseEvidenceStarts:          make([]uint32, 0, clauses),
		ClauseEvidenceCounts:          make([]uint16, 0, clauses),
		ClauseEvidenceNodeIDs:         make([]schema.NodeID, 0, clauseEvidence),
		ClauseRemediationStarts:       make([]uint32, 0, clauses),
		ClauseRemediationCounts:       make([]uint16, 0, clauses),
		ClauseRemediationIDs:          make([]schema.RemediationID, 0, clauseRemediations),
		ClauseOnSatisfied:             make([]schema.OutcomeID, 0, clauses),
		ClauseOnFalse:                 make([]schema.OutcomeID, 0, clauses),
		ClauseOnMissing:               make([]schema.OutcomeID, 0, clauses),
		ClauseOnStale:                 make([]schema.OutcomeID, 0, clauses),
		ClauseOnUnclear:               make([]schema.OutcomeID, 0, clauses),
		ClauseOnUnverifiable:          make([]schema.OutcomeID, 0, clauses),
		ClauseOnConflict:              make([]schema.OutcomeID, 0, clauses),
		ClauseExplanationIDs:          make([]schema.ExplanationID, 0, clauseExplanations),
		ClauseSourceStarts:            make([]uint32, 0, clauses),
		ClauseSourceEnds:              make([]uint32, 0, clauses),
		RequirementIDs:                make([]schema.RequirementID, 0, requirements),
		RequirementApplicabilityRoots: make([]schema.NodeID, 0, requirements),
		RequirementClauseStarts:       make([]uint32, 0, requirements),
		RequirementClauseCounts:       make([]uint16, 0, requirements),
		RequirementClauseIDs:          make([]schema.ClauseID, 0, requirementClauses),
		RequirementSourceStarts:       make([]uint32, 0, requirements),
		RequirementSourceEnds:         make([]uint32, 0, requirements),
	}}
}

// Document returns the builder-owned mutable document. Its slices remain
// valid only until the next builder mutation.
func (b *Builder) Document() *Document {
	return &b.doc
}

// Len returns the number of nodes currently stored.
func (b *Builder) Len() int {
	return b.doc.Len()
}

// SetSource copies source into builder-owned storage before any nodes are
// added. Reset permits binding a new source for the next document.
func (b *Builder) SetSource(source []byte) error {
	if len(b.doc.NodeKinds) != 0 || len(b.doc.EvidenceKindNames) != 0 || len(b.doc.EvidenceStateNames) != 0 ||
		len(b.doc.OutcomeNames) != 0 || len(b.doc.RemediationKinds) != 0 ||
		len(b.doc.ClauseAssertionRoots) != 0 || len(b.doc.RequirementIDs) != 0 {
		return ErrSourceAfterRecords
	}
	if uint64(len(source)) > uint64(math.MaxUint32) {
		return ErrSourceTooLarge
	}
	b.doc.InputBytes = append(b.doc.InputBytes[:0], source...)
	b.doc.Metadata.ContentHash = sha256.Sum256(b.doc.InputBytes)
	b.doc.Metadata.sourceSet = true
	return nil
}

func (b *Builder) validateNode(span SourceSpan) error {
	if uint64(len(b.doc.NodeKinds)) >= uint64(math.MaxUint32) {
		return ErrTooManyNodes
	}
	if !span.valid(len(b.doc.InputBytes)) {
		return ErrInvalidSourceSpan
	}
	return nil
}

func (b *Builder) addNode(kind NodeKind, ref uint32, span SourceSpan) schema.NodeID {
	b.doc.NodeKinds = append(b.doc.NodeKinds, kind)
	b.doc.NodeRefs = append(b.doc.NodeRefs, ref)
	b.doc.SourceStarts = append(b.doc.SourceStarts, span.Start)
	b.doc.SourceEnds = append(b.doc.SourceEnds, span.End)
	return schema.NodeID(len(b.doc.NodeKinds))
}

// AddCompare appends a typed compare payload and returns its stable NodeID.
// Exists and Defined accept ValueID zero; every other operation requires a
// nonzero literal value.
func (b *Builder) AddCompare(field schema.FieldID, op CompareOp, value schema.ValueID, span SourceSpan) (schema.NodeID, error) {
	if field == 0 {
		return 0, ErrInvalidField
	}
	if !op.Valid() {
		return 0, ErrInvalidCompareOp
	}
	if op == CompareOpIn {
		return 0, ErrInvalidValue
	}
	if (!op.RequiresValue() && value != 0) || (op.RequiresValue() && value == 0) {
		return 0, ErrInvalidValue
	}
	if err := b.validateNode(span); err != nil {
		return 0, err
	}
	ref := uint32(len(b.doc.CompareFields))
	b.doc.CompareFields = append(b.doc.CompareFields, field)
	b.doc.CompareOps = append(b.doc.CompareOps, op)
	b.doc.CompareValues = append(b.doc.CompareValues, value)
	b.doc.CompareListStarts = append(b.doc.CompareListStarts, uint32(len(b.doc.ListValueIDs)))
	b.doc.CompareListCounts = append(b.doc.CompareListCounts, 0)
	return b.addNode(NodeKindCompare, ref, span), nil
}

// AddExists appends an existence test, which has no literal ValueID.
func (b *Builder) AddExists(field schema.FieldID, span SourceSpan) (schema.NodeID, error) {
	return b.AddCompare(field, CompareOpExists, 0, span)
}

// AddDefined appends a classical field-definedness test with no literal ValueID.
func (b *Builder) AddDefined(field schema.FieldID, span SourceSpan) (schema.NodeID, error) {
	return b.AddCompare(field, CompareOpDefined, 0, span)
}

// AddBoolean appends one exact Boolean literal and constant node atomically.
func (b *Builder) AddBoolean(value bool, span SourceSpan) (schema.NodeID, error) {
	if err := b.validateNode(span); err != nil {
		return 0, err
	}
	if err := b.validateValue(); err != nil {
		return 0, err
	}
	ref := uint32(len(b.doc.BooleanValues))
	encoded := uint8(0)
	if value {
		encoded = 1
	}
	b.doc.BooleanValues = append(b.doc.BooleanValues, encoded)
	valueID := b.addValue(schema.ValueKindBoolean, ref)
	return b.addNode(NodeKindBoolean, uint32(valueID), span), nil
}

// AddIn appends an In comparison and copies its ValueIDs into one CSR edge
// column. Empty lists are retained for Task 7 arity diagnostics.
func (b *Builder) AddIn(field schema.FieldID, values []schema.ValueID, span SourceSpan) (schema.NodeID, error) {
	if field == 0 {
		return 0, ErrInvalidField
	}
	if len(values) > math.MaxUint16 || uint64(len(b.doc.ListValueIDs))+uint64(len(values)) > uint64(math.MaxUint32) {
		return 0, ErrTooManyChildren
	}
	for _, value := range values {
		if value == 0 {
			return 0, ErrInvalidValue
		}
	}
	if err := b.validateNode(span); err != nil {
		return 0, err
	}
	start := uint32(len(b.doc.ListValueIDs))
	ref := uint32(len(b.doc.CompareFields))
	b.doc.ListValueIDs = append(b.doc.ListValueIDs, values...)
	b.doc.CompareFields = append(b.doc.CompareFields, field)
	b.doc.CompareOps = append(b.doc.CompareOps, CompareOpIn)
	b.doc.CompareValues = append(b.doc.CompareValues, 0)
	b.doc.CompareListStarts = append(b.doc.CompareListStarts, start)
	b.doc.CompareListCounts = append(b.doc.CompareListCounts, uint16(len(values)))
	return b.addNode(NodeKindCompare, ref, span), nil
}

// AddGroup appends an All or Any node and copies its child IDs into the
// shared CSR edge column. Empty groups are retained for Task 7 validation.
func (b *Builder) AddGroup(kind NodeKind, children []schema.NodeID, span SourceSpan) (schema.NodeID, error) {
	if !kind.group() {
		return 0, ErrInvalidNodeKind
	}
	if len(children) > math.MaxUint16 || uint64(len(b.doc.ChildNodeIDs))+uint64(len(children)) > math.MaxUint32 {
		return 0, ErrTooManyChildren
	}
	for _, child := range children {
		if child == 0 {
			return 0, ErrInvalidNode
		}
	}
	if err := b.validateNode(span); err != nil {
		return 0, err
	}
	start := uint32(len(b.doc.ChildNodeIDs))
	ref := uint32(len(b.doc.GroupChildStarts))
	b.doc.ChildNodeIDs = append(b.doc.ChildNodeIDs, children...)
	b.doc.GroupChildStarts = append(b.doc.GroupChildStarts, start)
	b.doc.GroupChildCounts = append(b.doc.GroupChildCounts, uint16(len(children)))
	return b.addNode(kind, ref, span), nil
}

// AddNot appends a negation node referencing child.
func (b *Builder) AddNot(child schema.NodeID, span SourceSpan) (schema.NodeID, error) {
	if child == 0 {
		return 0, ErrInvalidNode
	}
	if err := b.validateNode(span); err != nil {
		return 0, err
	}
	ref := uint32(len(b.doc.NotChildren))
	b.doc.NotChildren = append(b.doc.NotChildren, child)
	return b.addNode(NodeKindNot, ref, span), nil
}

// AddEvidence appends an evidence requirement node.
func (b *Builder) AddEvidence(kind schema.EvidenceKindID, state schema.EvidenceStateID, span SourceSpan) (schema.NodeID, error) {
	return b.AddEvidenceMatch(kind, state, 0, 0, 0, span)
}

// AddEvidenceMatch appends an evidence requirement with optional symbol values.
func (b *Builder) AddEvidenceMatch(kind schema.EvidenceKindID, state schema.EvidenceStateID, subject, scope, timing schema.ValueID, span SourceSpan) (schema.NodeID, error) {
	if kind == 0 || state == 0 {
		return 0, ErrInvalidEvidence
	}
	if err := b.validateNode(span); err != nil {
		return 0, err
	}
	ref := uint32(len(b.doc.EvidenceKinds))
	b.doc.EvidenceKinds = append(b.doc.EvidenceKinds, kind)
	b.doc.EvidenceStates = append(b.doc.EvidenceStates, state)
	b.doc.EvidenceSubjects = append(b.doc.EvidenceSubjects, subject)
	b.doc.EvidenceScopes = append(b.doc.EvidenceScopes, scope)
	b.doc.EvidenceTimings = append(b.doc.EvidenceTimings, timing)
	var issues [EvidenceIssueReasonCount]schema.TemplateID
	b.doc.EvidenceIssueTemplateIDs = append(b.doc.EvidenceIssueTemplateIDs, issues[:]...)
	return b.addNode(NodeKindEvidence, ref, span), nil
}

// Reset clears logical content while retaining every column's capacity.
func (b *Builder) Reset() {
	d := &b.doc
	d.NodeKinds = d.NodeKinds[:0]
	d.NodeRefs = d.NodeRefs[:0]
	d.TemplateBytes = d.TemplateBytes[:0]
	d.TemplateOpStarts = d.TemplateOpStarts[:0]
	d.TemplateOpCounts = d.TemplateOpCounts[:0]
	d.TemplateLiteralStarts = d.TemplateLiteralStarts[:0]
	d.TemplateMaxBytes = d.TemplateMaxBytes[:0]
	d.TemplateContexts = d.TemplateContexts[:0]
	d.TemplateOps = d.TemplateOps[:0]
	d.TemplateArgs = d.TemplateArgs[:0]
	d.AssumptionTemplateIDs = d.AssumptionTemplateIDs[:0]
	d.AssumptionsSet = d.AssumptionsSet[:0]
	d.ExplanationRationaleIDs = d.ExplanationRationaleIDs[:0]
	d.ExplanationUncertaintyStarts = d.ExplanationUncertaintyStarts[:0]
	d.ExplanationUncertaintyCounts = d.ExplanationUncertaintyCounts[:0]
	d.ExplanationUncertaintyIDs = d.ExplanationUncertaintyIDs[:0]
	d.CompareFields = d.CompareFields[:0]
	d.CompareOps = d.CompareOps[:0]
	d.CompareValues = d.CompareValues[:0]
	d.CompareListStarts = d.CompareListStarts[:0]
	d.CompareListCounts = d.CompareListCounts[:0]
	d.ListValueIDs = d.ListValueIDs[:0]
	d.GroupChildStarts = d.GroupChildStarts[:0]
	d.GroupChildCounts = d.GroupChildCounts[:0]
	d.ChildNodeIDs = d.ChildNodeIDs[:0]
	d.NotChildren = d.NotChildren[:0]
	d.EvidenceKinds = d.EvidenceKinds[:0]
	d.EvidenceStates = d.EvidenceStates[:0]
	d.EvidenceSubjects = d.EvidenceSubjects[:0]
	d.EvidenceScopes = d.EvidenceScopes[:0]
	d.EvidenceTimings = d.EvidenceTimings[:0]
	d.EvidenceIssueTemplateIDs = d.EvidenceIssueTemplateIDs[:0]
	d.SourceStarts = d.SourceStarts[:0]
	d.SourceEnds = d.SourceEnds[:0]
	d.InputBytes = d.InputBytes[:0]
	d.Metadata = PolicyMetadata{}
	d.ValueKinds = d.ValueKinds[:0]
	d.ValueRefs = d.ValueRefs[:0]
	d.SymbolStarts = d.SymbolStarts[:0]
	d.SymbolLengths = d.SymbolLengths[:0]
	d.SymbolBytes = d.SymbolBytes[:0]
	d.IntegerValues = d.IntegerValues[:0]
	d.BooleanValues = d.BooleanValues[:0]
	d.TimestampValues = d.TimestampValues[:0]
	d.EvidenceKindNames = d.EvidenceKindNames[:0]
	d.EvidenceKindSourceStarts = d.EvidenceKindSourceStarts[:0]
	d.EvidenceKindSourceEnds = d.EvidenceKindSourceEnds[:0]
	d.EvidenceStateNames = d.EvidenceStateNames[:0]
	d.EvidenceStateSourceStarts = d.EvidenceStateSourceStarts[:0]
	d.EvidenceStateSourceEnds = d.EvidenceStateSourceEnds[:0]
	d.OutcomeNames = d.OutcomeNames[:0]
	d.OutcomePrecedence = d.OutcomePrecedence[:0]
	d.OutcomeTerminal = d.OutcomeTerminal[:0]
	d.OutcomeSourceStarts = d.OutcomeSourceStarts[:0]
	d.OutcomeSourceEnds = d.OutcomeSourceEnds[:0]
	d.RemediationKinds = d.RemediationKinds[:0]
	d.RemediationFields = d.RemediationFields[:0]
	d.RemediationValues = d.RemediationValues[:0]
	d.RemediationEvidenceKinds = d.RemediationEvidenceKinds[:0]
	d.RemediationSourceStarts = d.RemediationSourceStarts[:0]
	d.RemediationSourceEnds = d.RemediationSourceEnds[:0]
	d.ClauseAssertionRoots = d.ClauseAssertionRoots[:0]
	d.ClauseEvidenceStarts = d.ClauseEvidenceStarts[:0]
	d.ClauseEvidenceCounts = d.ClauseEvidenceCounts[:0]
	d.ClauseEvidenceNodeIDs = d.ClauseEvidenceNodeIDs[:0]
	d.ClauseRemediationStarts = d.ClauseRemediationStarts[:0]
	d.ClauseRemediationCounts = d.ClauseRemediationCounts[:0]
	d.ClauseRemediationIDs = d.ClauseRemediationIDs[:0]
	d.ClauseOnSatisfied = d.ClauseOnSatisfied[:0]
	d.ClauseOnFalse = d.ClauseOnFalse[:0]
	d.ClauseOnMissing = d.ClauseOnMissing[:0]
	d.ClauseOnStale = d.ClauseOnStale[:0]
	d.ClauseOnUnclear = d.ClauseOnUnclear[:0]
	d.ClauseOnUnverifiable = d.ClauseOnUnverifiable[:0]
	d.ClauseOnConflict = d.ClauseOnConflict[:0]
	d.ClauseExplanationIDs = d.ClauseExplanationIDs[:0]
	d.ClauseSourceStarts = d.ClauseSourceStarts[:0]
	d.ClauseSourceEnds = d.ClauseSourceEnds[:0]
	d.RequirementIDs = d.RequirementIDs[:0]
	d.RequirementApplicabilityRoots = d.RequirementApplicabilityRoots[:0]
	d.RequirementClauseStarts = d.RequirementClauseStarts[:0]
	d.RequirementClauseCounts = d.RequirementClauseCounts[:0]
	d.RequirementClauseIDs = d.RequirementClauseIDs[:0]
	d.RequirementSourceStarts = d.RequirementSourceStarts[:0]
	d.RequirementSourceEnds = d.RequirementSourceEnds[:0]
}
