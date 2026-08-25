package cli

import (
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/graphview"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

type semanticGraphIDs struct {
	policy      uint32
	requirement uint32
	clause      uint32
	outcome     uint32
	remediation uint32
}

func makeSemanticGraphIDs(base, requirements, clauses, outcomes int) semanticGraphIDs {
	policy := uint32(base + 1)
	requirement := policy + 1
	clause := requirement + uint32(requirements)
	outcome := clause + uint32(clauses)
	return semanticGraphIDs{
		policy: policy, requirement: requirement, clause: clause,
		outcome: outcome, remediation: outcome + uint32(outcomes),
	}
}

func buildASTGraph(document *ast.Document, compiled *program.Program) (graphview.Graph, error) {
	if document == nil || compiled == nil || document.Len() == 0 || uint64(len(document.InputBytes)) > math.MaxUint32 {
		return graphview.Graph{}, errInvalidTUIData
	}
	base := document.Len()
	requirements := len(document.RequirementIDs)
	clauses := len(document.ClauseAssertionRoots)
	outcomes := len(document.OutcomeNames)
	remediations := len(document.RemediationKinds)
	ids := makeSemanticGraphIDs(base, requirements, clauses, outcomes)
	nodes := base + 1 + requirements + clauses + outcomes + remediations
	edges, ok := astGraphEdgeCount(document, requirements, clauses, outcomes, remediations)
	if !ok {
		return graphview.Graph{}, graphview.ErrGraphLimit
	}
	graph, ok := newSemanticGraph(nodes, edges, uint32(len(document.InputBytes)))
	if !ok {
		return graphview.Graph{}, graphview.ErrGraphLimit
	}

	for row, kind := range document.NodeKinds {
		id := schema.NodeID(row + 1)
		span, found := document.Span(id)
		label, foundLabel := astGraphNodeLabel(document, compiled, id, kind)
		if !found || !foundLabel {
			return graphview.Graph{}, errInvalidTUIData
		}
		setGraphNode(&graph, row, astGraphNodeKind(kind), label,
			"AST node #"+strconv.Itoa(row+1), span.Start, span.End)
	}
	if err := fillASTSemanticNodes(&graph, document, compiled, ids); err != nil {
		return graphview.Graph{}, err
	}
	if err := fillASTEdges(&graph, document, ids); err != nil {
		return graphview.Graph{}, err
	}
	graph.Roots = append(graph.Roots, ids.policy)
	if err := graphview.Validate(&graph, graphview.DefaultLimits()); err != nil {
		return graphview.Graph{}, err
	}
	return graph, nil
}

func buildProgramGraph(compiled *program.Program) (graphview.Graph, error) {
	if compiled == nil || compiled.InstructionCount() == 0 || uint64(len(compiled.InputBytes)) > math.MaxUint32 {
		return graphview.Graph{}, errInvalidTUIData
	}
	base := compiled.InstructionCount()
	requirements := len(compiled.RequirementIDs)
	clauses := len(compiled.ClauseAssertionRoots)
	outcomes := len(compiled.Outcomes.Names)
	remediations := len(compiled.Remediations.Kinds)
	ids := makeSemanticGraphIDs(base, requirements, clauses, outcomes)
	nodes := base + 1 + requirements + clauses + outcomes + remediations
	edges, ok := programGraphEdgeCount(compiled, requirements, clauses, outcomes, remediations)
	if !ok {
		return graphview.Graph{}, graphview.ErrGraphLimit
	}
	graph, ok := newSemanticGraph(nodes, edges, uint32(len(compiled.InputBytes)))
	if !ok {
		return graphview.Graph{}, graphview.ErrGraphLimit
	}

	if len(compiled.InstructionSourceStarts) != base || len(compiled.InstructionSourceEnds) != base {
		return graphview.Graph{}, errInvalidTUIData
	}
	for row, opcode := range compiled.Opcodes {
		label, found := programGraphInstructionLabel(compiled, row, opcode)
		if !found {
			return graphview.Graph{}, errInvalidTUIData
		}
		detail := "instruction #" + strconv.Itoa(row+1)
		if row < len(compiled.InstructionNodes) {
			detail += "; source node #" + strconv.FormatUint(uint64(compiled.InstructionNodes[row]), 10)
		}
		setGraphNode(&graph, row, graphview.NodeInstruction, label, detail,
			compiled.InstructionSourceStarts[row], compiled.InstructionSourceEnds[row])
	}
	if err := fillProgramSemanticNodes(&graph, compiled, ids); err != nil {
		return graphview.Graph{}, err
	}
	if err := fillProgramEdges(&graph, compiled, ids); err != nil {
		return graphview.Graph{}, err
	}
	graph.Roots = append(graph.Roots, ids.policy)
	if err := graphview.Validate(&graph, graphview.DefaultLimits()); err != nil {
		return graphview.Graph{}, err
	}
	return graph, nil
}

func newSemanticGraph(nodes, edges int, sourceLength uint32) (graphview.Graph, bool) {
	limits := graphview.DefaultLimits()
	if nodes <= 0 || nodes > limits.MaxNodes || edges < 0 || edges > limits.MaxEdges {
		return graphview.Graph{}, false
	}
	return graphview.Graph{
		Labels: make([]string, nodes), Details: make([]string, nodes), Kinds: make([]graphview.NodeKind, nodes),
		SourceStarts: make([]uint32, nodes), SourceEnds: make([]uint32, nodes),
		EdgeStarts: make([]uint32, nodes), EdgeCounts: make([]uint16, nodes),
		Edges: make([]uint32, 0, edges), EdgeKinds: make([]graphview.EdgeKind, 0, edges),
		EdgeLabels: make([]string, 0, edges), Roots: make([]uint32, 0, 1), SourceLength: sourceLength,
	}, true
}

func astGraphEdgeCount(document *ast.Document, requirements, clauses, outcomes, remediations int) (int, bool) {
	return semanticGraphEdgeCount(
		uint64(len(document.ChildNodeIDs))+uint64(len(document.NotChildren)),
		requirements, clauses, outcomes, remediations,
		len(document.RequirementClauseIDs), len(document.ClauseEvidenceNodeIDs), len(document.ClauseRemediationIDs),
	)
}

func programGraphEdgeCount(compiled *program.Program, requirements, clauses, outcomes, remediations int) (int, bool) {
	return semanticGraphEdgeCount(
		uint64(len(compiled.Operands)), requirements, clauses, outcomes, remediations,
		len(compiled.RequirementClauseIDs), len(compiled.ClauseEvidenceIDs), len(compiled.ClauseRemediationIDs),
	)
}

func semanticGraphEdgeCount(structural uint64, requirements, clauses, outcomes, remediations, requirementClauses, evidence, clauseRemediations int) (int, bool) {
	total := structural + uint64(requirements+outcomes+remediations) +
		uint64(requirements+requirementClauses) + uint64(clauses*8+evidence+clauseRemediations)
	if total > uint64(graphview.DefaultLimits().MaxEdges) {
		return 0, false
	}
	return int(total), true
}

func setGraphNode(graph *graphview.Graph, row int, kind graphview.NodeKind, label, detail string, start, end uint32) {
	graph.Kinds[row] = kind
	graph.Labels[row] = boundGraphText(label, graphview.DefaultLimits().MaxLabelBytes)
	graph.Details[row] = boundGraphText(detail, graphview.DefaultLimits().MaxDetailBytes)
	graph.SourceStarts[row] = start
	graph.SourceEnds[row] = end
}

func fillASTSemanticNodes(graph *graphview.Graph, document *ast.Document, compiled *program.Program, ids semanticGraphIDs) error {
	metadata, ok := document.PolicyMetadata()
	if !ok {
		return errInvalidTUIData
	}
	name, ok := astGraphValue(document, metadata.Name)
	if !ok {
		return errInvalidTUIData
	}
	version, ok := astGraphValue(document, metadata.Version)
	if !ok {
		return errInvalidTUIData
	}
	setGraphNode(graph, int(ids.policy-1), graphview.NodePolicy,
		"policy "+unquoteGraphSymbol(name)+"@"+unquoteGraphSymbol(version),
		"policy source", 0, uint32(len(document.InputBytes)))

	for row, requirement := range document.RequirementIDs {
		span, found := document.RequirementSpan(requirement)
		if !found {
			return errInvalidTUIData
		}
		setGraphNode(graph, int(ids.requirement)+row-1, graphview.NodeRequirement,
			"requirement R"+strconv.FormatUint(uint64(requirement), 10),
			"policy requirement", span.Start, span.End)
	}
	for row := range document.ClauseAssertionRoots {
		span, found := document.ClauseSpan(schema.ClauseID(row + 1))
		if !found {
			return errInvalidTUIData
		}
		setGraphNode(graph, int(ids.clause)+row-1, graphview.NodeClause,
			"clause "+strconv.Itoa(row+1), "policy clause", span.Start, span.End)
	}
	for row := range document.OutcomeNames {
		id := schema.OutcomeID(row + 1)
		value, precedence, terminal, found := document.Outcome(id)
		span, spanOK := document.OutcomeSpan(id)
		label, labelOK := astGraphValue(document, value)
		if !found || !spanOK || !labelOK {
			return errInvalidTUIData
		}
		setGraphNode(graph, int(ids.outcome)+row-1, graphview.NodeOutcome,
			"outcome "+unquoteGraphSymbol(label),
			"precedence="+strconv.Itoa(int(precedence))+" terminal="+strconv.FormatBool(terminal), span.Start, span.End)
	}
	for row := range document.RemediationKinds {
		id := schema.RemediationID(row + 1)
		kind, field, value, evidence, found := document.Remediation(id)
		span, spanOK := document.RemediationSpan(id)
		label, labelOK := astGraphRemediationLabel(document, compiled, kind, field, value, evidence)
		if !found || !spanOK || !labelOK {
			return errInvalidTUIData
		}
		setGraphNode(graph, int(ids.remediation)+row-1, graphview.NodeRemediation,
			label, "bounded corrective action", span.Start, span.End)
	}
	return nil
}

func fillProgramSemanticNodes(graph *graphview.Graph, compiled *program.Program, ids semanticGraphIDs) error {
	name, ok := programGraphSymbol(compiled, compiled.PolicyName)
	if !ok {
		return errInvalidTUIData
	}
	version, ok := programGraphSymbol(compiled, compiled.PolicyVersion)
	if !ok {
		return errInvalidTUIData
	}
	setGraphNode(graph, int(ids.policy-1), graphview.NodePolicy,
		"policy "+name+"@"+version, "compiled policy", 0, uint32(len(compiled.InputBytes)))

	if len(compiled.RequirementSourceStarts) != len(compiled.RequirementIDs) ||
		len(compiled.RequirementSourceEnds) != len(compiled.RequirementIDs) {
		return errInvalidTUIData
	}
	for row, requirement := range compiled.RequirementIDs {
		setGraphNode(graph, int(ids.requirement)+row-1, graphview.NodeRequirement,
			"requirement R"+strconv.FormatUint(uint64(requirement), 10), "compiled requirement",
			compiled.RequirementSourceStarts[row], compiled.RequirementSourceEnds[row])
	}
	if len(compiled.ClauseSourceStarts) != len(compiled.ClauseAssertionRoots) ||
		len(compiled.ClauseSourceEnds) != len(compiled.ClauseAssertionRoots) {
		return errInvalidTUIData
	}
	for row := range compiled.ClauseAssertionRoots {
		setGraphNode(graph, int(ids.clause)+row-1, graphview.NodeClause,
			"clause "+strconv.Itoa(row+1), "compiled clause",
			compiled.ClauseSourceStarts[row], compiled.ClauseSourceEnds[row])
	}
	if len(compiled.OutcomeSourceStarts) != len(compiled.Outcomes.Names) ||
		len(compiled.OutcomeSourceEnds) != len(compiled.Outcomes.Names) {
		return errInvalidTUIData
	}
	for row, symbol := range compiled.Outcomes.Names {
		label, found := programGraphSymbol(compiled, symbol)
		if !found || row >= len(compiled.Outcomes.Precedence) || row >= len(compiled.Outcomes.Terminal) {
			return errInvalidTUIData
		}
		setGraphNode(graph, int(ids.outcome)+row-1, graphview.NodeOutcome, "outcome "+label,
			"precedence="+strconv.Itoa(int(compiled.Outcomes.Precedence[row]))+
				" terminal="+strconv.FormatBool(compiled.Outcomes.Terminal[row]),
			compiled.OutcomeSourceStarts[row], compiled.OutcomeSourceEnds[row])
	}
	if len(compiled.RemediationSourceStarts) != len(compiled.Remediations.Kinds) ||
		len(compiled.RemediationSourceEnds) != len(compiled.Remediations.Kinds) {
		return errInvalidTUIData
	}
	for row, kind := range compiled.Remediations.Kinds {
		label, found := programGraphRemediationLabel(compiled, row, kind)
		if !found {
			return errInvalidTUIData
		}
		setGraphNode(graph, int(ids.remediation)+row-1, graphview.NodeRemediation, label,
			"bounded corrective action", compiled.RemediationSourceStarts[row], compiled.RemediationSourceEnds[row])
	}
	return nil
}

func fillASTEdges(graph *graphview.Graph, document *ast.Document, ids semanticGraphIDs) error {
	base := document.Len()
	for row := range graph.Kinds {
		graph.EdgeStarts[row] = uint32(len(graph.Edges))
		source := uint32(row + 1)
		switch {
		case row < base:
			id := schema.NodeID(source)
			switch document.NodeKinds[row] {
			case ast.NodeKindAll, ast.NodeKindAny:
				children, ok := document.GroupChildren(id)
				if !ok {
					return errInvalidTUIData
				}
				for index, child := range children {
					if !appendGraphEdge(graph, source, uint32(child), graphview.EdgeArgument, "arg "+strconv.Itoa(index+1)) {
						return graphview.ErrGraphLimit
					}
				}
			case ast.NodeKindNot:
				child, ok := document.NotChild(id)
				if !ok || !appendGraphEdge(graph, source, uint32(child), graphview.EdgeOperand, "operand 1") {
					return errInvalidTUIData
				}
			}
		case source == ids.policy:
			if !appendPolicyEdges(graph, source, ids, len(document.RequirementIDs), len(document.OutcomeNames), len(document.RemediationKinds)) {
				return graphview.ErrGraphLimit
			}
		case source >= ids.requirement && source < ids.clause:
			index := int(source - ids.requirement)
			if err := appendASTRequirementEdges(graph, document, ids, source, index); err != nil {
				return err
			}
		case source >= ids.clause && source < ids.outcome:
			index := int(source - ids.clause)
			if err := appendASTClauseEdges(graph, document, ids, source, index); err != nil {
				return err
			}
		}
	}
	return nil
}

func fillProgramEdges(graph *graphview.Graph, compiled *program.Program, ids semanticGraphIDs) error {
	base := compiled.InstructionCount()
	if len(compiled.OperandStarts) != base || len(compiled.OperandCounts) != base {
		return errInvalidTUIData
	}
	for row := range graph.Kinds {
		graph.EdgeStarts[row] = uint32(len(graph.Edges))
		source := uint32(row + 1)
		switch {
		case row < base:
			start := uint64(compiled.OperandStarts[row])
			count := uint64(compiled.OperandCounts[row])
			if start+count > uint64(len(compiled.Operands)) {
				return errInvalidTUIData
			}
			for index, child := range compiled.Operands[int(start):int(start+count)] {
				if !appendGraphEdge(graph, source, uint32(child), graphview.EdgeOperand, "operand "+strconv.Itoa(index+1)) {
					return graphview.ErrGraphLimit
				}
			}
		case source == ids.policy:
			if !appendPolicyEdges(graph, source, ids, len(compiled.RequirementIDs), len(compiled.Outcomes.Names), len(compiled.Remediations.Kinds)) {
				return graphview.ErrGraphLimit
			}
		case source >= ids.requirement && source < ids.clause:
			index := int(source - ids.requirement)
			if err := appendProgramRequirementEdges(graph, compiled, ids, source, index); err != nil {
				return err
			}
		case source >= ids.clause && source < ids.outcome:
			index := int(source - ids.clause)
			if err := appendProgramClauseEdges(graph, compiled, ids, source, index); err != nil {
				return err
			}
		}
	}
	return nil
}

func appendPolicyEdges(graph *graphview.Graph, source uint32, ids semanticGraphIDs, requirements, outcomes, remediations int) bool {
	for row := range requirements {
		if !appendGraphEdge(graph, source, ids.requirement+uint32(row), graphview.EdgeContains, "contains") {
			return false
		}
	}
	for row := range outcomes {
		if !appendGraphEdge(graph, source, ids.outcome+uint32(row), graphview.EdgeContains, "contains") {
			return false
		}
	}
	for row := range remediations {
		if !appendGraphEdge(graph, source, ids.remediation+uint32(row), graphview.EdgeContains, "contains") {
			return false
		}
	}
	return true
}

func appendASTRequirementEdges(graph *graphview.Graph, document *ast.Document, ids semanticGraphIDs, source uint32, row int) error {
	if row >= len(document.RequirementApplicabilityRoots) || row >= len(document.RequirementIDs) {
		return errInvalidTUIData
	}
	if !appendGraphEdge(graph, source, uint32(document.RequirementApplicabilityRoots[row]), graphview.EdgeApplies, "applies") {
		return graphview.ErrGraphLimit
	}
	clauses, ok := document.RequirementClauses(document.RequirementIDs[row])
	if !ok {
		return errInvalidTUIData
	}
	for _, clause := range clauses {
		if clause == 0 || !appendGraphEdge(graph, source, ids.clause+uint32(clause-1), graphview.EdgeClause, "clause") {
			return errInvalidTUIData
		}
	}
	return nil
}

func appendProgramRequirementEdges(graph *graphview.Graph, compiled *program.Program, ids semanticGraphIDs, source uint32, row int) error {
	if row >= len(compiled.RequirementRoots) || row >= len(compiled.RequirementClauseStarts) || row >= len(compiled.RequirementClauseCounts) {
		return errInvalidTUIData
	}
	if !appendGraphEdge(graph, source, uint32(compiled.RequirementRoots[row]), graphview.EdgeApplies, "applies") {
		return graphview.ErrGraphLimit
	}
	start := uint64(compiled.RequirementClauseStarts[row])
	count := uint64(compiled.RequirementClauseCounts[row])
	if start+count > uint64(len(compiled.RequirementClauseIDs)) {
		return errInvalidTUIData
	}
	for _, clause := range compiled.RequirementClauseIDs[int(start):int(start+count)] {
		if clause == 0 || !appendGraphEdge(graph, source, ids.clause+uint32(clause-1), graphview.EdgeClause, "clause") {
			return errInvalidTUIData
		}
	}
	return nil
}

func appendASTClauseEdges(graph *graphview.Graph, document *ast.Document, ids semanticGraphIDs, source uint32, row int) error {
	clause := schema.ClauseID(row + 1)
	assertion, resolution, ok := document.Clause(clause)
	if !ok || !appendGraphEdge(graph, source, uint32(assertion), graphview.EdgeAssertion, "assert") {
		return errInvalidTUIData
	}
	evidence, ok := document.ClauseEvidence(clause)
	if !ok {
		return errInvalidTUIData
	}
	for _, node := range evidence {
		if !appendGraphEdge(graph, source, uint32(node), graphview.EdgeEvidence, "requires evidence") {
			return graphview.ErrGraphLimit
		}
	}
	if !appendResolutionEdges(graph, source, ids.outcome, [7]schema.OutcomeID{
		resolution.OnSatisfied, resolution.OnFalse, resolution.OnMissing, resolution.OnStale,
		resolution.OnUnclear, resolution.OnUnverifiable, resolution.OnConflict,
	}) {
		return errInvalidTUIData
	}
	remediations, ok := document.ClauseRemediations(clause)
	if !ok {
		return errInvalidTUIData
	}
	return appendRemediationEdges(graph, source, ids.remediation, remediations)
}

func appendProgramClauseEdges(graph *graphview.Graph, compiled *program.Program, ids semanticGraphIDs, source uint32, row int) error {
	if row >= len(compiled.ClauseAssertionRoots) || row >= len(compiled.ClauseEvidenceStarts) ||
		row >= len(compiled.ClauseEvidenceCounts) || row >= len(compiled.ClauseOnSatisfied) ||
		row >= len(compiled.ClauseOnFalse) || row >= len(compiled.ClauseRemediationStarts) ||
		row >= len(compiled.ClauseRemediationCounts) {
		return errInvalidTUIData
	}
	if !appendGraphEdge(graph, source, uint32(compiled.ClauseAssertionRoots[row]), graphview.EdgeAssertion, "assert") {
		return graphview.ErrGraphLimit
	}
	evidenceStart := uint64(compiled.ClauseEvidenceStarts[row])
	evidenceCount := uint64(compiled.ClauseEvidenceCounts[row])
	if evidenceStart+evidenceCount > uint64(len(compiled.ClauseEvidenceIDs)) {
		return errInvalidTUIData
	}
	for _, instruction := range compiled.ClauseEvidenceIDs[int(evidenceStart):int(evidenceStart+evidenceCount)] {
		if !appendGraphEdge(graph, source, uint32(instruction), graphview.EdgeEvidence, "requires evidence") {
			return graphview.ErrGraphLimit
		}
	}
	resolutionBase := row * truth.ReasonCount
	if resolutionBase+truth.ReasonCount > len(compiled.Resolutions.OutcomeIDs) {
		return errInvalidTUIData
	}
	outcomes := [7]schema.OutcomeID{
		compiled.ClauseOnSatisfied[row], compiled.ClauseOnFalse[row],
		compiled.Resolutions.OutcomeIDs[resolutionBase+int(truth.ReasonMissing-1)],
		compiled.Resolutions.OutcomeIDs[resolutionBase+int(truth.ReasonStale-1)],
		compiled.Resolutions.OutcomeIDs[resolutionBase+int(truth.ReasonUnclear-1)],
		compiled.Resolutions.OutcomeIDs[resolutionBase+int(truth.ReasonUnverifiable-1)],
		compiled.Resolutions.OutcomeIDs[resolutionBase+int(truth.ReasonConflict-1)],
	}
	if !appendResolutionEdges(graph, source, ids.outcome, outcomes) {
		return errInvalidTUIData
	}
	remediationStart := uint64(compiled.ClauseRemediationStarts[row])
	remediationCount := uint64(compiled.ClauseRemediationCounts[row])
	if remediationStart+remediationCount > uint64(len(compiled.ClauseRemediationIDs)) {
		return errInvalidTUIData
	}
	return appendRemediationEdges(graph, source, ids.remediation,
		compiled.ClauseRemediationIDs[int(remediationStart):int(remediationStart+remediationCount)])
}

func appendResolutionEdges(graph *graphview.Graph, source, outcomeBase uint32, outcomes [7]schema.OutcomeID) bool {
	kinds := [...]graphview.EdgeKind{
		graphview.EdgeSatisfied, graphview.EdgeFalse, graphview.EdgeMissing, graphview.EdgeStale,
		graphview.EdgeUnclear, graphview.EdgeUnverifiable, graphview.EdgeConflict,
	}
	labels := [...]string{"satisfied", "false", "missing", "stale", "unclear", "unverifiable", "conflict"}
	for row, outcome := range outcomes {
		if outcome == 0 || !appendGraphEdge(graph, source, outcomeBase+uint32(outcome-1), kinds[row], labels[row]) {
			return false
		}
	}
	return true
}

func appendRemediationEdges(graph *graphview.Graph, source, remediationBase uint32, remediations []schema.RemediationID) error {
	for _, remediation := range remediations {
		if remediation == 0 || !appendGraphEdge(graph, source, remediationBase+uint32(remediation-1), graphview.EdgeRemediation, "remediation") {
			return errInvalidTUIData
		}
	}
	return nil
}

func appendGraphEdge(graph *graphview.Graph, source, destination uint32, kind graphview.EdgeKind, label string) bool {
	if source == 0 || destination == 0 || uint64(source) > uint64(len(graph.Kinds)) ||
		uint64(destination) > uint64(len(graph.Kinds)) || graph.EdgeCounts[source-1] == math.MaxUint16 ||
		len(graph.Edges) == graphview.DefaultLimits().MaxEdges {
		return false
	}
	graph.Edges = append(graph.Edges, destination)
	graph.EdgeKinds = append(graph.EdgeKinds, kind)
	graph.EdgeLabels = append(graph.EdgeLabels, label)
	graph.EdgeCounts[source-1]++
	return true
}

func astGraphNodeKind(kind ast.NodeKind) graphview.NodeKind {
	switch kind {
	case ast.NodeKindBoolean:
		return graphview.NodeCompare
	case ast.NodeKindCompare:
		return graphview.NodeCompare
	case ast.NodeKindAll:
		return graphview.NodeAll
	case ast.NodeKindAny:
		return graphview.NodeAny
	case ast.NodeKindNot:
		return graphview.NodeNot
	case ast.NodeKindEvidence:
		return graphview.NodeEvidence
	default:
		return graphview.NodeInvalid
	}
}

func astGraphNodeLabel(document *ast.Document, compiled *program.Program, id schema.NodeID, kind ast.NodeKind) (string, bool) {
	switch kind {
	case ast.NodeKindBoolean:
		value, ok := document.Boolean(id)
		if !ok {
			return "", false
		}
		return astGraphValue(document, value)
	case ast.NodeKindCompare:
		field, op, value, ok := document.Compare(id)
		name, nameOK := programGraphField(compiled, field)
		if !ok || !nameOK {
			return "", false
		}
		label := name + " " + compareOpName(op)
		if op == ast.CompareOpIn {
			values, found := document.InValues(id)
			if !found {
				return "", false
			}
			list, found := astGraphValueList(document, values)
			return label + " " + list, found
		}
		if op.RequiresValue() {
			literal, found := astGraphValue(document, value)
			return label + " " + literal, found
		}
		return label, true
	case ast.NodeKindAll:
		return "all", true
	case ast.NodeKindAny:
		return "any", true
	case ast.NodeKindNot:
		return "not", true
	case ast.NodeKindEvidence:
		kindID, stateID, subject, scope, timing, ok := document.EvidenceMatch(id)
		kindName, kindOK := astGraphEvidenceKind(document, kindID)
		stateName, stateOK := astGraphEvidenceState(document, stateID)
		if !ok || !kindOK || !stateOK {
			return "", false
		}
		label := "evidence " + kindName + " = " + stateName
		return appendASTEvidenceQualifiers(label, document, subject, scope, timing)
	default:
		return "", false
	}
}

func programGraphInstructionLabel(compiled *program.Program, row int, opcode program.Opcode) (string, bool) {
	if row >= len(compiled.Fields) || row >= len(compiled.Values) || row >= len(compiled.ListStarts) ||
		row >= len(compiled.ListCounts) || row >= len(compiled.EvidenceKinds) || row >= len(compiled.EvidenceStates) ||
		row >= len(compiled.EvidenceSubjects) || row >= len(compiled.EvidenceScopes) || row >= len(compiled.EvidenceTimings) {
		return "", false
	}
	switch opcode {
	case program.OpcodeBoolean:
		return programGraphValue(compiled, compiled.Values[row])
	case program.OpcodeEqual, program.OpcodeNotEqual, program.OpcodeExists, program.OpcodeDefined,
		program.OpcodeLess, program.OpcodeLessEqual, program.OpcodeGreater, program.OpcodeGreaterEqual:
		field, ok := programGraphField(compiled, compiled.Fields[row])
		if !ok {
			return "", false
		}
		label := field + " " + programOpcodeName(opcode)
		if opcode == program.OpcodeExists || opcode == program.OpcodeDefined {
			return label, true
		}
		value, ok := programGraphValue(compiled, compiled.Values[row])
		return label + " " + value, ok
	case program.OpcodeIn:
		field, ok := programGraphField(compiled, compiled.Fields[row])
		if !ok {
			return "", false
		}
		start := uint64(compiled.ListStarts[row])
		count := uint64(compiled.ListCounts[row])
		if start+count > uint64(len(compiled.ListValues)) {
			return "", false
		}
		list, ok := programGraphValueList(compiled, compiled.ListValues[int(start):int(start+count)])
		return field + " in " + list, ok
	case program.OpcodeEvidence:
		kind, ok := programGraphEvidenceKind(compiled, compiled.EvidenceKinds[row])
		state, stateOK := programGraphEvidenceState(compiled, compiled.EvidenceStates[row])
		if !ok || !stateOK {
			return "", false
		}
		label := "evidence " + kind + " = " + state
		return appendProgramEvidenceQualifiers(label, compiled,
			compiled.EvidenceSubjects[row], compiled.EvidenceScopes[row], compiled.EvidenceTimings[row])
	case program.OpcodeAll:
		return "all", true
	case program.OpcodeAny:
		return "any", true
	case program.OpcodeNot:
		return "not", true
	default:
		return "", false
	}
}

func astGraphRemediationLabel(document *ast.Document, compiled *program.Program, kind ast.RemediationKind, field schema.FieldID, value schema.ValueID, evidence schema.EvidenceKindID) (string, bool) {
	switch kind {
	case ast.RemediationKindSetField:
		name, ok := programGraphField(compiled, field)
		literal, valueOK := astGraphValue(document, value)
		return "set " + name + " = " + literal, ok && valueOK
	case ast.RemediationKindAddEvidence:
		name, ok := astGraphEvidenceKind(document, evidence)
		return "add evidence " + name, ok
	default:
		return "", false
	}
}

func programGraphRemediationLabel(compiled *program.Program, row int, kind result.RemediationKind) (string, bool) {
	if row >= len(compiled.Remediations.Fields) || row >= len(compiled.Remediations.Values) ||
		row >= len(compiled.Remediations.EvidenceKinds) {
		return "", false
	}
	switch kind {
	case result.RemediationSetField:
		name, ok := programGraphField(compiled, compiled.Remediations.Fields[row])
		literal, valueOK := programGraphValue(compiled, compiled.Remediations.Values[row])
		return "set " + name + " = " + literal, ok && valueOK
	case result.RemediationAddEvidence:
		name, ok := programGraphEvidenceKind(compiled, compiled.Remediations.EvidenceKinds[row])
		return "add evidence " + name, ok
	default:
		return "", false
	}
}

func astGraphValue(document *ast.Document, id schema.ValueID) (string, bool) {
	kind, ok := document.ValueKind(id)
	if !ok {
		return "", false
	}
	switch kind {
	case schema.ValueKindSymbol:
		value, found := document.SymbolValue(id)
		return quoteGraphBytes(value), found
	case schema.ValueKindInteger:
		value, found := document.IntegerValue(id)
		return strconv.FormatInt(value, 10), found
	case schema.ValueKindBoolean:
		value, found := document.BooleanValue(id)
		return strconv.FormatBool(value), found
	case schema.ValueKindTimestamp:
		value, found := document.TimestampValue(id)
		return strconv.FormatInt(value, 10), found
	default:
		return "", false
	}
}

func programGraphValue(compiled *program.Program, id schema.ValueID) (string, bool) {
	if id == 0 || uint64(id) > uint64(len(compiled.ValueKinds)) || uint64(id) > uint64(len(compiled.ValueRefs)) {
		return "", false
	}
	row := id - 1
	ref := compiled.ValueRefs[row]
	switch compiled.ValueKinds[row] {
	case schema.ValueKindSymbol:
		value, ok := compiled.Symbol(schema.SymbolID(ref))
		return quoteGraphBytes(value), ok
	case schema.ValueKindInteger:
		if ref == 0 || uint64(ref) > uint64(len(compiled.IntegerValues)) {
			return "", false
		}
		return strconv.FormatInt(compiled.IntegerValues[ref-1], 10), true
	case schema.ValueKindBoolean:
		if ref == 0 || uint64(ref) > uint64(len(compiled.BooleanValues)) || compiled.BooleanValues[ref-1] > 1 {
			return "", false
		}
		return strconv.FormatBool(compiled.BooleanValues[ref-1] != 0), true
	case schema.ValueKindTimestamp:
		if ref == 0 || uint64(ref) > uint64(len(compiled.TimestampValues)) {
			return "", false
		}
		return strconv.FormatInt(compiled.TimestampValues[ref-1], 10), true
	default:
		return "", false
	}
}

func astGraphValueList(document *ast.Document, values []schema.ValueID) (string, bool) {
	var builder strings.Builder
	builder.WriteByte('[')
	for row, id := range values {
		value, ok := astGraphValue(document, id)
		if !ok {
			return "", false
		}
		if row != 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(value)
	}
	builder.WriteByte(']')
	return builder.String(), true
}

func programGraphValueList(compiled *program.Program, values []schema.ValueID) (string, bool) {
	var builder strings.Builder
	builder.WriteByte('[')
	for row, id := range values {
		value, ok := programGraphValue(compiled, id)
		if !ok {
			return "", false
		}
		if row != 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(value)
	}
	builder.WriteByte(']')
	return builder.String(), true
}

func programGraphField(compiled *program.Program, id schema.FieldID) (string, bool) {
	if id == 0 || uint64(id) > uint64(len(compiled.FieldNames)) {
		return "", false
	}
	return programGraphSymbol(compiled, compiled.FieldNames[id-1])
}

func astGraphEvidenceKind(document *ast.Document, id schema.EvidenceKindID) (string, bool) {
	value, ok := document.EvidenceKindName(id)
	if !ok {
		return "", false
	}
	label, ok := astGraphValue(document, value)
	return unquoteGraphSymbol(label), ok
}

func astGraphEvidenceState(document *ast.Document, id schema.EvidenceStateID) (string, bool) {
	value, ok := document.EvidenceStateName(id)
	if !ok {
		return "", false
	}
	label, ok := astGraphValue(document, value)
	return unquoteGraphSymbol(label), ok
}

func programGraphEvidenceKind(compiled *program.Program, id schema.EvidenceKindID) (string, bool) {
	if id == 0 || uint64(id) > uint64(len(compiled.EvidenceKindNames)) {
		return "", false
	}
	return programGraphSymbol(compiled, compiled.EvidenceKindNames[id-1])
}

func programGraphEvidenceState(compiled *program.Program, id schema.EvidenceStateID) (string, bool) {
	if id == 0 || uint64(id) > uint64(len(compiled.EvidenceStateNames)) {
		return "", false
	}
	return programGraphSymbol(compiled, compiled.EvidenceStateNames[id-1])
}

func programGraphSymbol(compiled *program.Program, id schema.SymbolID) (string, bool) {
	value, ok := compiled.Symbol(id)
	if !ok || !utf8.Valid(value) {
		return "", false
	}
	return safeGraphName(value), true
}

func appendASTEvidenceQualifiers(label string, document *ast.Document, subject, scope, timing schema.ValueID) (string, bool) {
	qualifiers := [...]struct {
		name string
		id   schema.ValueID
	}{{"subject", subject}, {"scope", scope}, {"timing", timing}}
	for _, qualifier := range qualifiers {
		if qualifier.id == 0 {
			continue
		}
		value, ok := astGraphValue(document, qualifier.id)
		if !ok {
			return "", false
		}
		label += " " + qualifier.name + "=" + value
	}
	return label, true
}

func appendProgramEvidenceQualifiers(label string, compiled *program.Program, subject, scope, timing schema.SymbolID) (string, bool) {
	qualifiers := [...]struct {
		name string
		id   schema.SymbolID
	}{{"subject", subject}, {"scope", scope}, {"timing", timing}}
	for _, qualifier := range qualifiers {
		if qualifier.id == 0 {
			continue
		}
		value, ok := compiled.Symbol(qualifier.id)
		if !ok {
			return "", false
		}
		label += " " + qualifier.name + "=" + quoteGraphBytes(value)
	}
	return label, true
}

func quoteGraphBytes(value []byte) string {
	return strconv.Quote(string(value))
}

func safeGraphName(value []byte) string {
	text := string(value)
	for _, character := range text {
		if unicode.IsControl(character) {
			return strconv.Quote(text)
		}
	}
	return text
}

func unquoteGraphSymbol(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if decoded, err := strconv.Unquote(value); err == nil {
			return safeGraphName([]byte(decoded))
		}
	}
	return value
}

func boundGraphText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	end := limit - 3
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + "..."
}
