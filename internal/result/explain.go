package result

import (
	"errors"
	"strconv"

	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

var (
	ErrInvalidExplanationCatalog = errors.New("result: invalid explanation catalog")
	ErrInvalidExplanationResult  = errors.New("result: invalid explanation result")
	ErrExplanationTooLarge       = errors.New("result: explanation output too large")
)

// ExplanationCatalog is a borrowed view over immutable Program columns needed
// to validate and materialize policy-authored result text.
type ExplanationCatalog struct {
	Templates     TemplateTable
	Explanations  ExplanationTable
	Outcomes      OutcomeTable
	Remediations  RemediationTable
	SymbolBytes   []byte
	SymbolStarts  []uint32
	SymbolLengths []uint32

	ValueKinds      []schema.ValueKind
	ValueRefs       []uint32
	IntegerValues   []int64
	BooleanValues   []uint64
	TimestampValues []int64

	FieldNames         []schema.SymbolID
	FieldKinds         []schema.ValueKind
	EvidenceKindNames  []schema.SymbolID
	EvidenceStateNames []schema.SymbolID
	RequirementIDs     []schema.RequirementID

	EvidenceIssueNodeIDs      []schema.NodeID
	EvidenceIssueTemplateIDs  []schema.TemplateID
	EvidenceSourceNodes       []schema.NodeID
	EvidenceInstructionIDs    []schema.InstructionID
	InstructionEvidenceKinds  []schema.EvidenceKindID
	InstructionEvidenceStates []schema.EvidenceStateID
	PolicyName                schema.SymbolID
	PolicyVersion             schema.SymbolID
}

// TextRange indexes one UTF-8 byte range in Materialized.Bytes.
type TextRange struct {
	Start uint32
	End   uint32
}

// RenderedRemediation preserves the structured remediation kind and typed
// value metadata while text fields index Materialized.Bytes.
type RenderedRemediation struct {
	FieldName        TextRange
	Value            TextRange
	EvidenceKindName TextRange
	Kind             RemediationKind
	ValueKind        schema.ValueKind
}

// Materialized owns reusable output storage for one explanation. Outcome
// borrows the bound immutable catalog; Requirements and Evidence borrow the
// selected result.Batch row and are valid while that batch remains unchanged.
type Materialized struct {
	Bytes          []byte
	Outcome        []byte
	EvidenceIssues []TextRange
	Assumptions    []TextRange
	Uncertainty    []TextRange
	Remediations   []RenderedRemediation
	Requirements   []schema.RequirementID
	Evidence       []schema.EvidenceID
	Rationale      TextRange
	// DriverRequirementRow is the zero-based row in the bound RequirementIDs.
	DriverRequirementRow uint32
}

// Explainer validates and retains one borrowed immutable catalog header.
type Explainer struct {
	catalog ExplanationCatalog
	bound   bool
}

var explanationReasonNames = [...]string{
	"",
	"missing",
	"stale",
	"unclear",
	"unverifiable",
	"wrong_scope",
	"wrong_subject",
	"wrong_timing",
	"invalid",
	"conflict",
}

// Bind validates catalog into temporary state and replaces the usable catalog
// only after every borrowed column and cross-reference succeeds.
func (e *Explainer) Bind(catalog ExplanationCatalog) error {
	if e == nil || !validExplanationCatalog(&catalog) {
		return ErrInvalidExplanationCatalog
	}
	e.catalog = catalog
	e.bound = true
	return nil
}

func validExplanationCatalog(catalog *ExplanationCatalog) bool {
	if catalog == nil || catalog.Templates.Validate() != nil ||
		catalog.Explanations.Validate(&catalog.Templates) != nil ||
		!validCatalogSymbols(catalog) || !catalog.Outcomes.valid() ||
		!catalog.Remediations.valid() {
		return false
	}
	if value, ok := catalogSymbol(catalog, catalog.PolicyName); !ok || len(value) == 0 {
		return false
	}
	if value, ok := catalogSymbol(catalog, catalog.PolicyVersion); !ok || len(value) == 0 {
		return false
	}
	for _, id := range catalog.Outcomes.Names {
		if value, ok := catalogSymbol(catalog, id); !ok || len(value) == 0 {
			return false
		}
	}
	if len(catalog.FieldNames) != len(catalog.FieldKinds) {
		return false
	}
	for row, id := range catalog.FieldNames {
		if value, ok := catalogSymbol(catalog, id); !ok || len(value) == 0 || !catalog.FieldKinds[row].Valid() {
			return false
		}
	}
	for _, ids := range [][]schema.SymbolID{catalog.EvidenceKindNames, catalog.EvidenceStateNames} {
		for _, id := range ids {
			if value, ok := catalogSymbol(catalog, id); !ok || len(value) == 0 {
				return false
			}
		}
	}
	for row, id := range catalog.RequirementIDs {
		if id == 0 {
			return false
		}
		for previous := 0; previous < row; previous++ {
			if catalog.RequirementIDs[previous] == id {
				return false
			}
		}
	}
	if !validCatalogValues(catalog) || !validCatalogRemediations(catalog) ||
		!validCatalogEvidenceIssues(catalog) || !validCatalogTemplateBounds(catalog) ||
		!validCatalogTemplateContexts(catalog) {
		return false
	}
	return true
}

func validCatalogSymbols(catalog *ExplanationCatalog) bool {
	if len(catalog.SymbolStarts) == 0 || len(catalog.SymbolStarts) != len(catalog.SymbolLengths) {
		return false
	}
	var cursor uint64
	for row := range catalog.SymbolStarts {
		start := uint64(catalog.SymbolStarts[row])
		end := start + uint64(catalog.SymbolLengths[row])
		if start != cursor || end > uint64(len(catalog.SymbolBytes)) {
			return false
		}
		cursor = end
	}
	return cursor == uint64(len(catalog.SymbolBytes))
}

func catalogSymbol(catalog *ExplanationCatalog, id schema.SymbolID) ([]byte, bool) {
	if catalog == nil || id == 0 {
		return nil, false
	}
	row := uint64(id - 1)
	if row >= uint64(len(catalog.SymbolStarts)) || row >= uint64(len(catalog.SymbolLengths)) {
		return nil, false
	}
	start := uint64(catalog.SymbolStarts[row])
	end := start + uint64(catalog.SymbolLengths[row])
	if end > uint64(len(catalog.SymbolBytes)) {
		return nil, false
	}
	return catalog.SymbolBytes[int(start):int(end)], true
}

func validCatalogValues(catalog *ExplanationCatalog) bool {
	if len(catalog.ValueKinds) != len(catalog.ValueRefs) {
		return false
	}
	for row, kind := range catalog.ValueKinds {
		ref := catalog.ValueRefs[row]
		switch kind {
		case schema.ValueKindSymbol:
			if _, ok := catalogSymbol(catalog, schema.SymbolID(ref)); !ok {
				return false
			}
		case schema.ValueKindInteger:
			if ref == 0 || uint64(ref) > uint64(len(catalog.IntegerValues)) {
				return false
			}
		case schema.ValueKindBoolean:
			if ref == 0 || uint64(ref) > uint64(len(catalog.BooleanValues)) || catalog.BooleanValues[ref-1] > 1 {
				return false
			}
		case schema.ValueKindTimestamp:
			if ref == 0 || uint64(ref) > uint64(len(catalog.TimestampValues)) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func validCatalogRemediations(catalog *ExplanationCatalog) bool {
	if uint64(len(catalog.Remediations.Kinds)) > uint64(^uint32(0)) {
		return false
	}
	for row := range catalog.Remediations.Kinds {
		id := schema.RemediationID(row + 1)
		remediation, ok := catalog.Remediations.Lookup(id)
		if !ok {
			return false
		}
		switch remediation.Kind {
		case RemediationSetField:
			fieldRow := uint64(remediation.Field - 1)
			valueRow := uint64(remediation.Value - 1)
			if remediation.Field == 0 || remediation.Value == 0 ||
				fieldRow >= uint64(len(catalog.FieldKinds)) || valueRow >= uint64(len(catalog.ValueKinds)) ||
				catalog.FieldKinds[fieldRow] != catalog.ValueKinds[valueRow] {
				return false
			}
		case RemediationAddEvidence:
			if remediation.EvidenceKind == 0 || uint64(remediation.EvidenceKind) > uint64(len(catalog.EvidenceKindNames)) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func validCatalogEvidenceIssues(catalog *ExplanationCatalog) bool {
	if len(catalog.EvidenceSourceNodes) != len(catalog.EvidenceInstructionIDs) ||
		len(catalog.InstructionEvidenceKinds) != len(catalog.InstructionEvidenceStates) ||
		uint64(len(catalog.EvidenceIssueNodeIDs))*EvidenceIssueTemplateCount != uint64(len(catalog.EvidenceIssueTemplateIDs)) {
		return false
	}
	for edge, node := range catalog.EvidenceSourceNodes {
		instruction := catalog.EvidenceInstructionIDs[edge]
		if node == 0 || instruction == 0 || uint64(instruction) > uint64(len(catalog.InstructionEvidenceKinds)) {
			return false
		}
		row := instruction - 1
		kind := catalog.InstructionEvidenceKinds[row]
		state := catalog.InstructionEvidenceStates[row]
		if kind == 0 || uint64(kind) > uint64(len(catalog.EvidenceKindNames)) ||
			state == 0 || uint64(state) > uint64(len(catalog.EvidenceStateNames)) {
			return false
		}
	}
	var previous schema.NodeID
	for issueRow, node := range catalog.EvidenceIssueNodeIDs {
		if node == 0 || node <= previous {
			return false
		}
		previous = node
		kind, state, ok := catalogEvidenceRequirement(catalog, node)
		if !ok || kind == 0 || state == 0 {
			return false
		}
		start := issueRow * EvidenceIssueTemplateCount
		for reason := range EvidenceIssueTemplateCount {
			if _, ok := catalog.Templates.Lookup(catalog.EvidenceIssueTemplateIDs[start+reason]); !ok {
				return false
			}
		}
	}
	return true
}

func catalogEvidenceRequirement(catalog *ExplanationCatalog, node schema.NodeID) (schema.EvidenceKindID, schema.EvidenceStateID, bool) {
	var kind schema.EvidenceKindID
	var state schema.EvidenceStateID
	found := false
	for edge, source := range catalog.EvidenceSourceNodes {
		if source != node {
			continue
		}
		instruction := catalog.EvidenceInstructionIDs[edge]
		if instruction == 0 || uint64(instruction) > uint64(len(catalog.InstructionEvidenceKinds)) ||
			uint64(instruction) > uint64(len(catalog.InstructionEvidenceStates)) {
			return 0, 0, false
		}
		row := instruction - 1
		candidateKind := catalog.InstructionEvidenceKinds[row]
		candidateState := catalog.InstructionEvidenceStates[row]
		if found && (candidateKind != kind || candidateState != state) {
			return 0, 0, false
		}
		kind, state, found = candidateKind, candidateState, true
	}
	return kind, state, found
}

func validCatalogTemplateBounds(catalog *ExplanationCatalog) bool {
	if uint64(len(catalog.Templates.OpStarts)) > uint64(^uint32(0)) {
		return false
	}
	for row := range catalog.Templates.OpStarts {
		id := schema.TemplateID(row + 1)
		template, ok := catalog.Templates.Lookup(id)
		if !ok {
			return false
		}
		maximum, ok := catalogTemplateMaximum(catalog, template)
		if !ok || maximum > uint64(template.MaxBytes) {
			return false
		}
	}
	return true
}

func catalogTemplateMaximum(catalog *ExplanationCatalog, template Template) (uint64, bool) {
	var maximum uint64
	for index, op := range template.Ops {
		var n uint64
		switch op {
		case TemplateOpLiteral:
			n = uint64(template.Args[index])
		case TemplateOpPolicyName:
			value, ok := catalogSymbol(catalog, catalog.PolicyName)
			if !ok {
				return 0, false
			}
			n = uint64(len(value))
		case TemplateOpPolicyVersion:
			value, ok := catalogSymbol(catalog, catalog.PolicyVersion)
			if !ok {
				return 0, false
			}
			n = uint64(len(value))
		case TemplateOpRequestID, TemplateOpRequirementID, TemplateOpClauseID, TemplateOpNodeID, TemplateOpEvidenceID:
			n = 11
		case TemplateOpOutcome:
			n = maxCatalogSymbolLength(catalog, catalog.Outcomes.Names)
		case TemplateOpReason:
			n = uint64(len("wrong_subject"))
		case TemplateOpEvidenceKind:
			n = maxCatalogSymbolLength(catalog, catalog.EvidenceKindNames)
		case TemplateOpEvidenceState, TemplateOpRequiredEvidenceState:
			n = maxCatalogSymbolLength(catalog, catalog.EvidenceStateNames)
		default:
			return 0, false
		}
		maximum += n
		if maximum > MaxRenderedTemplateBytes {
			return 0, false
		}
	}
	return maximum, true
}

func maxCatalogSymbolLength(catalog *ExplanationCatalog, ids []schema.SymbolID) uint64 {
	if len(ids) == 0 {
		return MaxRenderedTemplateBytes + 1
	}
	var maximum uint64
	for _, id := range ids {
		value, ok := catalogSymbol(catalog, id)
		if !ok {
			return MaxRenderedTemplateBytes + 1
		}
		if uint64(len(value)) > maximum {
			maximum = uint64(len(value))
		}
	}
	return maximum
}

func validCatalogTemplateContexts(catalog *ExplanationCatalog) bool {
	for _, id := range catalog.Explanations.AssumptionTemplateIDs {
		if !catalogTemplateOpsAllowed(catalog, id, TemplateOpRequestID, false) {
			return false
		}
	}
	for row := range catalog.Explanations.RationaleTemplateIDs {
		explanation, ok := catalog.Explanations.Lookup(schema.ExplanationID(row + 1))
		if !ok || !catalogTemplateOpsAllowed(catalog, explanation.Rationale, TemplateOpReason, false) {
			return false
		}
		for _, id := range explanation.Uncertainty {
			if !catalogTemplateOpsAllowed(catalog, id, TemplateOpReason, false) {
				return false
			}
		}
	}
	for row := range catalog.EvidenceIssueNodeIDs {
		start := row * EvidenceIssueTemplateCount
		for reason := range EvidenceIssueTemplateCount {
			allowPresent := reason != int(truth.ReasonMissing-1)
			if !catalogTemplateOpsAllowed(catalog, catalog.EvidenceIssueTemplateIDs[start+reason], TemplateOpEvidenceID, allowPresent) {
				return false
			}
		}
	}
	return true
}

func catalogTemplateOpsAllowed(catalog *ExplanationCatalog, id schema.TemplateID, maximum TemplateOp, allowPresentEvidence bool) bool {
	template, ok := catalog.Templates.Lookup(id)
	if !ok {
		return false
	}
	for _, op := range template.Ops {
		if op > maximum || (!allowPresentEvidence && (op == TemplateOpEvidenceState || op == TemplateOpEvidenceID)) {
			return false
		}
	}
	return true
}

type templateBindings struct {
	policyName            []byte
	policyVersion         []byte
	outcome               []byte
	evidenceKind          []byte
	evidenceState         []byte
	requiredEvidenceState []byte
	request               schema.RequestID
	requirement           schema.RequirementID
	clause                schema.ClauseID
	node                  schema.NodeID
	reason                schema.ReasonID
	evidence              schema.EvidenceID
}

type explanationIssuePlan struct {
	template Template
	bindings templateBindings
}

type explanationPlan struct {
	uncertainty    []schema.TemplateID
	requirements   []schema.RequirementID
	evidence       []schema.EvidenceID
	remediations   []schema.RemediationID
	rationale      Template
	issues         [EvidenceIssueTemplateCount]explanationIssuePlan
	base           templateBindings
	totalBytes     uint32
	requirementRow uint32
	issueCount     uint8
}

// Materialize validates one complete result row before changing dst, then
// appends all text and structured ranges into caller-owned reusable storage.
func (e *Explainer) Materialize(dst *Materialized, batch *Batch, row uint32, requestID schema.RequestID) error {
	if e == nil || !e.bound {
		return ErrInvalidExplanationCatalog
	}
	if dst == nil || batch == nil || requestID == 0 {
		return ErrInvalidExplanationResult
	}
	plan, err := e.plan(batch, row, requestID)
	if err != nil {
		return err
	}
	materializePlan(dst, &e.catalog, &plan)
	return nil
}

func (e *Explainer) plan(batch *Batch, row uint32, requestID schema.RequestID) (explanationPlan, error) {
	var plan explanationPlan
	catalog := &e.catalog
	if !validMaterializedBatchShape(batch, row) {
		return plan, ErrInvalidExplanationResult
	}
	requirementStart, requirementEnd, _ := materializedRange(batch.RequirementOffsets, row, len(batch.RequirementIDs))
	driverStart, driverEnd, _ := materializedRange(batch.DriverOffsets, row, len(batch.DriverRequirements))
	evidenceStart, evidenceEnd, _ := materializedRange(batch.EvidenceOffsets, row, len(batch.EvidenceIDs))
	reasonStart, reasonEnd, _ := materializedRange(batch.ReasonOffsets, row, len(batch.ReasonIDs))
	remediationStart, remediationEnd, _ := materializedRange(batch.RemediationOffsets, row, len(batch.RemediationIDs))
	if driverEnd-driverStart != 1 {
		return plan, ErrInvalidExplanationResult
	}

	outcome, ok := catalog.Outcomes.Lookup(batch.OutcomeIDs[row])
	if !ok {
		return plan, ErrInvalidExplanationResult
	}
	outcomeName, ok := catalogSymbol(catalog, outcome.Name)
	if !ok {
		return plan, ErrInvalidExplanationCatalog
	}
	policyName, _ := catalogSymbol(catalog, catalog.PolicyName)
	policyVersion, _ := catalogSymbol(catalog, catalog.PolicyVersion)
	driver := int(driverStart)
	requirement := batch.DriverRequirements[driver]
	clause := batch.DriverClauses[driver]
	node := batch.DriverNodes[driver]
	reason := batch.DriverReasons[driver]
	if clause == 0 || node == 0 ||
		(reason != 0 && (reason < truth.ReasonMissing || reason > truth.ReasonConflict)) {
		return plan, ErrInvalidExplanationResult
	}
	explanation, ok := catalog.Explanations.Lookup(batch.DriverExplanations[driver])
	if !ok {
		return plan, ErrInvalidExplanationResult
	}
	plan.rationale = catalog.Templates.validatedTemplate(explanation.Rationale)
	plan.uncertainty = explanation.Uncertainty
	plan.requirements = batch.RequirementIDs[requirementStart:requirementEnd]
	plan.evidence = batch.EvidenceIDs[evidenceStart:evidenceEnd]
	plan.remediations = batch.RemediationIDs[remediationStart:remediationEnd]
	plan.base = templateBindings{
		policyName: policyName, policyVersion: policyVersion, outcome: outcomeName,
		request: requestID, requirement: requirement, clause: clause, node: node, reason: reason,
	}
	plan.requirementRow, ok = appliedRequirementRow(catalog, plan.requirements, requirement)
	if !ok || !validEvidenceView(plan.evidence) {
		return explanationPlan{}, ErrInvalidExplanationResult
	}

	rationaleBytes, ok := renderedTemplateLength(plan.rationale, plan.base)
	if !ok {
		return explanationPlan{}, ErrInvalidExplanationResult
	}
	total := rationaleBytes
	tooLarge := false
	for _, id := range catalog.Explanations.AssumptionTemplateIDs {
		template := catalog.Templates.validatedTemplate(id)
		n, ok := renderedTemplateLength(template, plan.base)
		if !ok {
			return explanationPlan{}, ErrInvalidExplanationResult
		}
		if !addExplanationLength(&total, n) {
			tooLarge = true
		}
	}
	for _, id := range plan.uncertainty {
		template := catalog.Templates.validatedTemplate(id)
		n, ok := renderedTemplateLength(template, plan.base)
		if !ok {
			return explanationPlan{}, ErrInvalidExplanationResult
		}
		if !addExplanationLength(&total, n) {
			tooLarge = true
		}
	}

	var previousReason schema.ReasonID
	foundDriverReason := reason == 0
	for edge := reasonStart; edge < reasonEnd; edge++ {
		i := int(edge)
		reasonID := batch.ReasonIDs[i]
		reasonNode := batch.ReasonNodes[i]
		evidenceID := batch.ReasonEvidenceIDs[i]
		evidenceState := batch.ReasonEvidenceStates[i]
		if reasonID < truth.ReasonMissing || reasonID > truth.ReasonConflict || reasonID <= previousReason || reasonNode == 0 ||
			(evidenceID == 0) != (evidenceState == 0) {
			return explanationPlan{}, ErrInvalidExplanationResult
		}
		previousReason = reasonID
		foundDriverReason = foundDriverReason || reasonID == reason
		issueRow, isEvidence := catalogIssueRow(catalog, reasonNode)
		if !isEvidence {
			if evidenceID != 0 {
				return explanationPlan{}, ErrInvalidExplanationResult
			}
			continue
		}
		if reasonID == truth.ReasonMissing {
			if evidenceID != 0 {
				return explanationPlan{}, ErrInvalidExplanationResult
			}
		} else if evidenceID == 0 || !catalogEvidenceStateValid(catalog, evidenceState) || !evidenceViewContains(plan.evidence, evidenceID) {
			return explanationPlan{}, ErrInvalidExplanationResult
		}
		kind, requiredState, ok := catalogEvidenceRequirement(catalog, reasonNode)
		if !ok {
			return explanationPlan{}, ErrInvalidExplanationCatalog
		}
		kindName, _ := catalogSymbol(catalog, catalog.EvidenceKindNames[kind-1])
		requiredStateName, _ := catalogSymbol(catalog, catalog.EvidenceStateNames[requiredState-1])
		var evidenceStateName []byte
		if evidenceState != 0 {
			evidenceStateName, _ = catalogSymbol(catalog, catalog.EvidenceStateNames[evidenceState-1])
		}
		templateID := catalog.EvidenceIssueTemplateIDs[issueRow*EvidenceIssueTemplateCount+int(reasonID-1)]
		template := catalog.Templates.validatedTemplate(templateID)
		bindings := plan.base
		bindings.node = reasonNode
		bindings.reason = reasonID
		bindings.evidence = evidenceID
		bindings.evidenceKind = kindName
		bindings.evidenceState = evidenceStateName
		bindings.requiredEvidenceState = requiredStateName
		n, ok := renderedTemplateLength(template, bindings)
		if !ok {
			return explanationPlan{}, ErrInvalidExplanationResult
		}
		plan.issues[plan.issueCount] = explanationIssuePlan{template: template, bindings: bindings}
		plan.issueCount++
		if !addExplanationLength(&total, n) {
			tooLarge = true
		}
	}
	if !foundDriverReason || (reason == 0 && reasonStart != reasonEnd) {
		return explanationPlan{}, ErrInvalidExplanationResult
	}

	for _, id := range plan.remediations {
		remediation, ok := catalog.Remediations.Lookup(id)
		if !ok {
			return explanationPlan{}, ErrInvalidExplanationResult
		}
		n, ok := renderedRemediationLength(catalog, remediation)
		if !ok {
			return explanationPlan{}, ErrInvalidExplanationCatalog
		}
		if !addExplanationLength(&total, n) {
			tooLarge = true
		}
	}
	if tooLarge {
		return explanationPlan{}, ErrExplanationTooLarge
	}
	plan.totalBytes = total
	return plan, nil
}

func addExplanationLength(total *uint32, n uint32) bool {
	if *total > MaxRenderedExplanationBytes || n > MaxRenderedExplanationBytes-*total {
		return false
	}
	*total += n
	return true
}

func validMaterializedBatchShape(batch *Batch, row uint32) bool {
	if batch == nil || row >= batch.Rows || uint64(batch.Rows)+1 > uint64(^uint(0)>>1) ||
		uint64(len(batch.OutcomeIDs)) != uint64(batch.Rows) {
		return false
	}
	if len(batch.DriverRequirements) != len(batch.DriverClauses) || len(batch.DriverRequirements) != len(batch.DriverNodes) ||
		len(batch.DriverRequirements) != len(batch.DriverReasons) || len(batch.DriverRequirements) != len(batch.DriverExplanations) ||
		len(batch.ReasonIDs) != len(batch.ReasonNodes) || len(batch.ReasonIDs) != len(batch.ReasonEvidenceIDs) ||
		len(batch.ReasonIDs) != len(batch.ReasonEvidenceStates) {
		return false
	}
	checks := []struct {
		offsets []uint32
		edges   int
	}{
		{batch.RequirementOffsets, len(batch.RequirementIDs)},
		{batch.DriverOffsets, len(batch.DriverRequirements)},
		{batch.EvidenceOffsets, len(batch.EvidenceIDs)},
		{batch.ReasonOffsets, len(batch.ReasonIDs)},
		{batch.RemediationOffsets, len(batch.RemediationIDs)},
	}
	for _, check := range checks {
		if uint64(len(check.offsets)) != uint64(batch.Rows)+1 {
			return false
		}
		if _, _, ok := materializedRange(check.offsets, row, check.edges); !ok {
			return false
		}
	}
	return true
}

func materializedRange(offsets []uint32, row uint32, edges int) (uint32, uint32, bool) {
	if uint64(row)+1 >= uint64(len(offsets)) || len(offsets) == 0 || offsets[0] != 0 ||
		uint64(offsets[len(offsets)-1]) != uint64(edges) {
		return 0, 0, false
	}
	start := offsets[row]
	end := offsets[row+1]
	if start > end || uint64(end) > uint64(edges) {
		return 0, 0, false
	}
	return start, end, true
}

func appliedRequirementRow(catalog *ExplanationCatalog, ids []schema.RequirementID, driver schema.RequirementID) (uint32, bool) {
	catalogRow := 0
	var driverRow uint32
	foundDriver := false
	for _, id := range ids {
		for catalogRow < len(catalog.RequirementIDs) && catalog.RequirementIDs[catalogRow] != id {
			catalogRow++
		}
		if catalogRow == len(catalog.RequirementIDs) {
			return 0, false
		}
		if id == driver {
			driverRow = uint32(catalogRow)
			foundDriver = true
		}
		catalogRow++
	}
	return driverRow, foundDriver
}

func validEvidenceView(ids []schema.EvidenceID) bool {
	for _, id := range ids {
		if id == 0 {
			return false
		}
	}
	return true
}

func evidenceViewContains(ids []schema.EvidenceID, want schema.EvidenceID) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func catalogIssueRow(catalog *ExplanationCatalog, node schema.NodeID) (int, bool) {
	low, high := 0, len(catalog.EvidenceIssueNodeIDs)
	for low < high {
		middle := low + (high-low)/2
		if catalog.EvidenceIssueNodeIDs[middle] < node {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return low, low < len(catalog.EvidenceIssueNodeIDs) && catalog.EvidenceIssueNodeIDs[low] == node
}

func catalogEvidenceStateValid(catalog *ExplanationCatalog, id schema.EvidenceStateID) bool {
	return id != 0 && uint64(id) <= uint64(len(catalog.EvidenceStateNames))
}

func renderedTemplateLength(template Template, bindings templateBindings) (uint32, bool) {
	var total uint64
	for index, op := range template.Ops {
		var n int
		switch op {
		case TemplateOpLiteral:
			n = int(template.Args[index])
		case TemplateOpPolicyName:
			n = len(bindings.policyName)
		case TemplateOpPolicyVersion:
			n = len(bindings.policyVersion)
		case TemplateOpRequestID:
			if bindings.request == 0 {
				return 0, false
			}
			n = namespacedIDLength(uint32(bindings.request))
		case TemplateOpOutcome:
			n = len(bindings.outcome)
		case TemplateOpRequirementID:
			if bindings.requirement == 0 {
				return 0, false
			}
			n = namespacedIDLength(uint32(bindings.requirement))
		case TemplateOpClauseID:
			if bindings.clause == 0 {
				return 0, false
			}
			n = namespacedIDLength(uint32(bindings.clause))
		case TemplateOpNodeID:
			if bindings.node == 0 {
				return 0, false
			}
			n = namespacedIDLength(uint32(bindings.node))
		case TemplateOpReason:
			name, ok := ReasonName(bindings.reason)
			if !ok {
				return 0, false
			}
			n = len(name)
		case TemplateOpEvidenceKind:
			n = len(bindings.evidenceKind)
		case TemplateOpEvidenceState:
			n = len(bindings.evidenceState)
		case TemplateOpRequiredEvidenceState:
			n = len(bindings.requiredEvidenceState)
		case TemplateOpEvidenceID:
			if bindings.evidence == 0 {
				return 0, false
			}
			n = namespacedIDLength(uint32(bindings.evidence))
		default:
			return 0, false
		}
		if n == 0 && op != TemplateOpLiteral {
			return 0, false
		}
		total += uint64(n)
		if total > uint64(template.MaxBytes) || total > MaxRenderedTemplateBytes {
			return 0, false
		}
	}
	return uint32(total), true
}

// ReasonName returns the stable machine-readable name for one engine reason.
func ReasonName(id schema.ReasonID) (string, bool) {
	if id < truth.ReasonMissing || id > truth.ReasonConflict {
		return "", false
	}
	return explanationReasonNames[id], true
}

func namespacedIDLength(value uint32) int {
	digits := 1
	for value >= 10 {
		value /= 10
		digits++
	}
	return digits + 1
}

func renderedRemediationLength(catalog *ExplanationCatalog, remediation Remediation) (uint32, bool) {
	switch remediation.Kind {
	case RemediationSetField:
		field, ok := catalogSymbol(catalog, catalog.FieldNames[remediation.Field-1])
		if !ok {
			return 0, false
		}
		valueLength, ok := catalogValueLength(catalog, remediation.Value)
		if !ok {
			return 0, false
		}
		return boundedExplanationLength(uint64(len(field)) + uint64(valueLength)), true
	case RemediationAddEvidence:
		name, ok := catalogSymbol(catalog, catalog.EvidenceKindNames[remediation.EvidenceKind-1])
		return boundedExplanationLength(uint64(len(name))), ok
	default:
		return 0, false
	}
}

func catalogValueLength(catalog *ExplanationCatalog, id schema.ValueID) (uint32, bool) {
	if id == 0 || uint64(id) > uint64(len(catalog.ValueKinds)) || uint64(id) > uint64(len(catalog.ValueRefs)) {
		return 0, false
	}
	row := id - 1
	ref := catalog.ValueRefs[row]
	switch catalog.ValueKinds[row] {
	case schema.ValueKindSymbol:
		value, ok := catalogSymbol(catalog, schema.SymbolID(ref))
		return boundedExplanationLength(uint64(len(value))), ok
	case schema.ValueKindInteger:
		var scratch [20]byte
		return uint32(len(strconv.AppendInt(scratch[:0], catalog.IntegerValues[ref-1], 10))), true
	case schema.ValueKindBoolean:
		if catalog.BooleanValues[ref-1] == 0 {
			return 5, true
		}
		return 4, true
	case schema.ValueKindTimestamp:
		var scratch [20]byte
		return uint32(len(strconv.AppendInt(scratch[:0], catalog.TimestampValues[ref-1], 10))), true
	default:
		return 0, false
	}
}

func boundedExplanationLength(n uint64) uint32 {
	if n > MaxRenderedExplanationBytes {
		return MaxRenderedExplanationBytes + 1
	}
	return uint32(n)
}

func materializePlan(dst *Materialized, catalog *ExplanationCatalog, plan *explanationPlan) {
	dst.Bytes = reserveExplanation(dst.Bytes, int(plan.totalBytes))
	dst.EvidenceIssues = reserveExplanation(dst.EvidenceIssues, int(plan.issueCount))
	dst.Assumptions = reserveExplanation(dst.Assumptions, len(catalog.Explanations.AssumptionTemplateIDs))
	dst.Uncertainty = reserveExplanation(dst.Uncertainty, len(plan.uncertainty))
	dst.Remediations = reserveExplanation(dst.Remediations, len(plan.remediations))
	dst.Outcome = plan.base.outcome
	dst.Requirements = plan.requirements
	dst.Evidence = plan.evidence
	dst.DriverRequirementRow = plan.requirementRow

	dst.Rationale = appendRenderedTemplate(&dst.Bytes, plan.rationale, plan.base)
	for issue := range int(plan.issueCount) {
		item := &plan.issues[issue]
		dst.EvidenceIssues = append(dst.EvidenceIssues, appendRenderedTemplate(&dst.Bytes, item.template, item.bindings))
	}
	for _, id := range catalog.Explanations.AssumptionTemplateIDs {
		template := catalog.Templates.validatedTemplate(id)
		dst.Assumptions = append(dst.Assumptions, appendRenderedTemplate(&dst.Bytes, template, plan.base))
	}
	for _, id := range plan.uncertainty {
		template := catalog.Templates.validatedTemplate(id)
		dst.Uncertainty = append(dst.Uncertainty, appendRenderedTemplate(&dst.Bytes, template, plan.base))
	}
	for _, id := range plan.remediations {
		remediation, _ := catalog.Remediations.Lookup(id)
		dst.Remediations = append(dst.Remediations, appendRenderedRemediation(&dst.Bytes, catalog, remediation))
	}
}

func reserveExplanation[T any](dst []T, capacity int) []T {
	if cap(dst) < capacity {
		return make([]T, 0, capacity)
	}
	return dst[:0]
}

func appendRenderedTemplate(dst *[]byte, template Template, bindings templateBindings) TextRange {
	start := uint32(len(*dst))
	literal := 0
	for index, op := range template.Ops {
		switch op {
		case TemplateOpLiteral:
			end := literal + int(template.Args[index])
			*dst = append(*dst, template.LiteralBytes[literal:end]...)
			literal = end
		case TemplateOpPolicyName:
			*dst = append(*dst, bindings.policyName...)
		case TemplateOpPolicyVersion:
			*dst = append(*dst, bindings.policyVersion...)
		case TemplateOpRequestID:
			*dst = appendNamespacedID(*dst, 'R', uint32(bindings.request))
		case TemplateOpOutcome:
			*dst = append(*dst, bindings.outcome...)
		case TemplateOpRequirementID:
			*dst = appendNamespacedID(*dst, 'R', uint32(bindings.requirement))
		case TemplateOpClauseID:
			*dst = appendNamespacedID(*dst, 'C', uint32(bindings.clause))
		case TemplateOpNodeID:
			*dst = appendNamespacedID(*dst, 'N', uint32(bindings.node))
		case TemplateOpReason:
			name, _ := ReasonName(bindings.reason)
			*dst = append(*dst, name...)
		case TemplateOpEvidenceKind:
			*dst = append(*dst, bindings.evidenceKind...)
		case TemplateOpEvidenceState:
			*dst = append(*dst, bindings.evidenceState...)
		case TemplateOpRequiredEvidenceState:
			*dst = append(*dst, bindings.requiredEvidenceState...)
		case TemplateOpEvidenceID:
			*dst = appendNamespacedID(*dst, 'E', uint32(bindings.evidence))
		}
	}
	return TextRange{Start: start, End: uint32(len(*dst))}
}

func appendNamespacedID(dst []byte, prefix byte, value uint32) []byte {
	dst = append(dst, prefix)
	return strconv.AppendUint(dst, uint64(value), 10)
}

func appendRenderedRemediation(dst *[]byte, catalog *ExplanationCatalog, remediation Remediation) RenderedRemediation {
	rendered := RenderedRemediation{Kind: remediation.Kind}
	switch remediation.Kind {
	case RemediationSetField:
		field, _ := catalogSymbol(catalog, catalog.FieldNames[remediation.Field-1])
		rendered.FieldName = appendText(dst, field)
		rendered.ValueKind = catalog.ValueKinds[remediation.Value-1]
		rendered.Value = appendCatalogValue(dst, catalog, remediation.Value)
	case RemediationAddEvidence:
		name, _ := catalogSymbol(catalog, catalog.EvidenceKindNames[remediation.EvidenceKind-1])
		rendered.EvidenceKindName = appendText(dst, name)
	}
	return rendered
}

func appendText(dst *[]byte, value []byte) TextRange {
	start := uint32(len(*dst))
	*dst = append(*dst, value...)
	return TextRange{Start: start, End: uint32(len(*dst))}
}

func appendCatalogValue(dst *[]byte, catalog *ExplanationCatalog, id schema.ValueID) TextRange {
	start := uint32(len(*dst))
	row := id - 1
	ref := catalog.ValueRefs[row]
	switch catalog.ValueKinds[row] {
	case schema.ValueKindSymbol:
		value, _ := catalogSymbol(catalog, schema.SymbolID(ref))
		*dst = append(*dst, value...)
	case schema.ValueKindInteger:
		*dst = strconv.AppendInt(*dst, catalog.IntegerValues[ref-1], 10)
	case schema.ValueKindBoolean:
		*dst = strconv.AppendBool(*dst, catalog.BooleanValues[ref-1] != 0)
	case schema.ValueKindTimestamp:
		*dst = strconv.AppendInt(*dst, catalog.TimestampValues[ref-1], 10)
	}
	return TextRange{Start: start, End: uint32(len(*dst))}
}
