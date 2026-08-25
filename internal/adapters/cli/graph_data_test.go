package cli

import (
	"strings"
	"testing"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/graphview"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func TestBuildASTGraphIncludesCompleteSemanticTopology(t *testing.T) {
	decoded, compiled := graphTestPolicy(t)
	graph, err := buildASTGraph(decoded.document, compiled)
	if err != nil {
		t.Fatalf("buildASTGraph() error = %v", err)
	}
	if err := graphview.Validate(&graph, graphview.DefaultLimits()); err != nil {
		t.Fatalf("Validate(AST) error = %v", err)
	}

	wantNodes := decoded.document.Len() + 1 + len(decoded.document.RequirementIDs) +
		len(decoded.document.ClauseAssertionRoots) + len(decoded.document.OutcomeNames) +
		len(decoded.document.RemediationKinds)
	if len(graph.Kinds) != wantNodes {
		t.Fatalf("AST graph nodes = %d, want %d", len(graph.Kinds), wantNodes)
	}
	if graph.SourceLength != uint32(len(decoded.document.InputBytes)) {
		t.Fatalf("AST source length = %d, want %d", graph.SourceLength, len(decoded.document.InputBytes))
	}
	for row := range decoded.document.Len() {
		if graph.SourceStarts[row] != decoded.document.SourceStarts[row] ||
			graph.SourceEnds[row] != decoded.document.SourceEnds[row] {
			t.Fatalf("AST node %d span = [%d,%d), want [%d,%d)", row+1,
				graph.SourceStarts[row], graph.SourceEnds[row],
				decoded.document.SourceStarts[row], decoded.document.SourceEnds[row])
		}
	}

	assertCompleteSemanticGraph(t, graph, "arg 1", graphview.EdgeArgument)
	compare := graphNode(t, graph, graphview.NodeCompare, `requester.trust equal "external"`)
	if compare == 0 {
		t.Fatal("AST graph does not expose the field and literal in a compare label")
	}
	evidence := graphNodeContaining(t, graph, graphview.NodeEvidence, "approval_record", "valid", "before_execution")
	clause := graphNode(t, graph, graphview.NodeClause, "clause 1")
	assertGraphEdge(t, graph, clause, evidence, graphview.EdgeEvidence, "requires evidence")
	assertNoGraphPayloadLeak(t, graph)
}

func TestBuildASTGraphLabelsNotChildAsOperand(t *testing.T) {
	deps := productTestDependencies()
	policySource := strings.Replace(deps.policy,
		`"applies": {"op": "equal", "field": "action.dataset", "value": "protected_dataset"},`,
		`"applies": {"op":"not","arg":{"op":"equal","field":"action.dataset","value":"protected_dataset"}},`, 1)
	if policySource == deps.policy {
		t.Fatal("test policy did not add negation")
	}
	var pipeline engine
	decoded, err := pipeline.decodePolicy([]byte(policySource))
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := pipeline.lowerPolicy(decoded)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := buildASTGraph(decoded.document, compiled)
	if err != nil {
		t.Fatal(err)
	}
	for row, kind := range decoded.document.NodeKinds {
		if kind != ast.NodeKindNot {
			continue
		}
		child, ok := decoded.document.NotChild(schema.NodeID(row + 1))
		if !ok {
			t.Fatalf("NotChild(%d) is missing", row+1)
		}
		assertGraphEdge(t, graph, uint32(row+1), uint32(child), graphview.EdgeOperand, "operand 1")
		return
	}
	t.Fatal("decoded policy contains no negation node")
}

func TestBuildProgramGraphIncludesCompleteSemanticTopology(t *testing.T) {
	_, compiled := graphTestPolicy(t)
	graph, err := buildProgramGraph(compiled)
	if err != nil {
		t.Fatalf("buildProgramGraph() error = %v", err)
	}
	if err := graphview.Validate(&graph, graphview.DefaultLimits()); err != nil {
		t.Fatalf("Validate(Program) error = %v", err)
	}

	wantNodes := compiled.InstructionCount() + 1 + len(compiled.RequirementIDs) +
		len(compiled.ClauseAssertionRoots) + len(compiled.Outcomes.Names) +
		len(compiled.Remediations.Kinds)
	if len(graph.Kinds) != wantNodes {
		t.Fatalf("Program graph nodes = %d, want %d", len(graph.Kinds), wantNodes)
	}
	for row := range compiled.InstructionCount() {
		if graph.Kinds[row] != graphview.NodeInstruction {
			t.Fatalf("Program graph node %d kind = %v, want instruction", row+1, graph.Kinds[row])
		}
		if graph.SourceStarts[row] != compiled.InstructionSourceStarts[row] ||
			graph.SourceEnds[row] != compiled.InstructionSourceEnds[row] {
			t.Fatalf("Program instruction %d span = [%d,%d), want [%d,%d)", row+1,
				graph.SourceStarts[row], graph.SourceEnds[row],
				compiled.InstructionSourceStarts[row], compiled.InstructionSourceEnds[row])
		}
	}

	assertCompleteSemanticGraph(t, graph, "operand 1", graphview.EdgeOperand)
	if graphNode(t, graph, graphview.NodeInstruction, `requester.trust equal "external"`) == 0 {
		t.Fatal("Program graph does not expose the field and literal in an instruction label")
	}
	assertNoGraphPayloadLeak(t, graph)
}

func TestBooleanGraphLabels(t *testing.T) {
	document := &ast.Document{
		NodeKinds:     []ast.NodeKind{ast.NodeKindBoolean},
		NodeRefs:      []uint32{1},
		ValueKinds:    []schema.ValueKind{schema.ValueKindBoolean},
		ValueRefs:     []uint32{0},
		BooleanValues: []uint8{1},
	}
	if kind := astGraphNodeKind(ast.NodeKindBoolean); kind != graphview.NodeCompare {
		t.Fatalf("Boolean graph kind = %v, want compare-style leaf", kind)
	}
	if label, ok := astGraphNodeLabel(document, nil, 1, ast.NodeKindBoolean); !ok || label != "true" {
		t.Fatalf("AST Boolean label = %q, %v", label, ok)
	}
	compiled := &program.Program{
		Fields:           []schema.FieldID{0},
		Values:           []schema.ValueID{1},
		ListStarts:       []uint32{0},
		ListCounts:       []uint16{0},
		EvidenceKinds:    []schema.EvidenceKindID{0},
		EvidenceStates:   []schema.EvidenceStateID{0},
		EvidenceSubjects: []schema.SymbolID{0},
		EvidenceScopes:   []schema.SymbolID{0},
		EvidenceTimings:  []schema.SymbolID{0},
		ValueKinds:       []schema.ValueKind{schema.ValueKindBoolean},
		ValueRefs:        []uint32{1},
		BooleanValues:    []uint64{1},
	}
	if label, ok := programGraphInstructionLabel(compiled, 0, program.OpcodeBoolean); !ok || label != "true" {
		t.Fatalf("Program Boolean label = %q, %v", label, ok)
	}
	if name := programOpcodeName(program.OpcodeBoolean); name != "boolean" {
		t.Fatalf("Boolean opcode name = %q", name)
	}
	if name := programOpcodeName(program.OpcodeDefined); name != "defined" {
		t.Fatalf("Defined opcode name = %q", name)
	}
}

func graphTestPolicy(t *testing.T) (decodedPolicy, *program.Program) {
	t.Helper()
	deps := productTestDependencies()
	var pipeline engine
	decoded, err := pipeline.decodePolicy([]byte(deps.policy))
	if err != nil {
		t.Fatalf("decodePolicy() error = %v", err)
	}
	compiled, err := pipeline.lowerPolicy(decoded)
	if err != nil {
		t.Fatalf("lowerPolicy() error = %v", err)
	}
	return decoded, compiled
}

func assertCompleteSemanticGraph(t *testing.T, graph graphview.Graph, structuralLabel string, structuralKind graphview.EdgeKind) {
	t.Helper()
	policy := graphNode(t, graph, graphview.NodePolicy, "policy verifoxx@1.0.0")
	requirement := graphNode(t, graph, graphview.NodeRequirement, "requirement R1")
	clause := graphNode(t, graph, graphview.NodeClause, "clause 1")
	approve := graphNode(t, graph, graphview.NodeOutcome, "outcome Approve")
	reject := graphNode(t, graph, graphview.NodeOutcome, "outcome Reject")
	escalate := graphNode(t, graph, graphview.NodeOutcome, "outcome Escalate")
	remediation := graphNode(t, graph, graphview.NodeRemediation, "add evidence usage_limit_adjustment")
	if len(graph.Roots) != 1 || graph.Roots[0] != policy {
		t.Fatalf("graph roots = %v, want policy node %d", graph.Roots, policy)
	}

	assertGraphEdge(t, graph, policy, requirement, graphview.EdgeContains, "contains")
	assertGraphEdgeKindAndLabel(t, graph, requirement, graphview.EdgeApplies, "applies")
	assertGraphEdge(t, graph, requirement, clause, graphview.EdgeClause, "clause")
	assertGraphEdgeKindAndLabel(t, graph, clause, graphview.EdgeAssertion, "assert")
	assertGraphEdge(t, graph, clause, approve, graphview.EdgeSatisfied, "satisfied")
	assertGraphEdge(t, graph, clause, reject, graphview.EdgeFalse, "false")
	assertGraphEdge(t, graph, clause, escalate, graphview.EdgeMissing, "missing")
	assertGraphEdge(t, graph, clause, escalate, graphview.EdgeStale, "stale")
	assertGraphEdge(t, graph, clause, escalate, graphview.EdgeUnclear, "unclear")
	assertGraphEdge(t, graph, clause, escalate, graphview.EdgeUnverifiable, "unverifiable")
	assertGraphEdge(t, graph, clause, escalate, graphview.EdgeConflict, "conflict")
	assertGraphEdgeKindAndLabel(t, graph, 0, graphview.EdgeRemediation, "remediation")
	assertGraphEdgeKindAndLabel(t, graph, 0, structuralKind, structuralLabel)
	if remediation == 0 {
		t.Fatal("graph does not contain the policy remediation")
	}
}

func graphNode(t *testing.T, graph graphview.Graph, kind graphview.NodeKind, label string) uint32 {
	t.Helper()
	for row := range graph.Kinds {
		if graph.Kinds[row] == kind && graph.Labels[row] == label {
			return uint32(row + 1)
		}
	}
	return 0
}

func graphNodeContaining(t *testing.T, graph graphview.Graph, kind graphview.NodeKind, parts ...string) uint32 {
	t.Helper()
	for row := range graph.Kinds {
		if graph.Kinds[row] != kind {
			continue
		}
		matched := true
		for _, part := range parts {
			matched = matched && strings.Contains(graph.Labels[row], part)
		}
		if matched {
			return uint32(row + 1)
		}
	}
	t.Fatalf("graph has no %v node containing %q", kind, parts)
	return 0
}

func assertGraphEdge(t *testing.T, graph graphview.Graph, from, to uint32, kind graphview.EdgeKind, label string) {
	t.Helper()
	if from == 0 || to == 0 {
		t.Fatalf("cannot inspect edge %d -> %d (%s)", from, to, label)
	}
	start := graph.EdgeStarts[from-1]
	end := start + uint32(graph.EdgeCounts[from-1])
	for edge := start; edge < end; edge++ {
		if graph.Edges[edge] == to && graph.EdgeKinds[edge] == kind && graph.EdgeLabels[edge] == label {
			return
		}
	}
	t.Fatalf("graph has no edge %d -> %d kind=%v label=%q", from, to, kind, label)
}

func assertGraphEdgeKindAndLabel(t *testing.T, graph graphview.Graph, from uint32, kind graphview.EdgeKind, label string) {
	t.Helper()
	first, last := uint32(0), uint32(len(graph.Edges))
	if from != 0 {
		first = graph.EdgeStarts[from-1]
		last = first + uint32(graph.EdgeCounts[from-1])
	}
	for edge := first; edge < last; edge++ {
		if graph.EdgeKinds[edge] == kind && graph.EdgeLabels[edge] == label {
			return
		}
	}
	t.Fatalf("graph has no edge kind=%v label=%q from=%d", kind, label, from)
}

func assertNoGraphPayloadLeak(t *testing.T, graph graphview.Graph) {
	t.Helper()
	for row := range graph.Labels {
		text := graph.Labels[row] + "\n" + graph.Details[row]
		for _, payload := range []string{"external_partner", "designated_reviewer", "unverified_remote_env", "one_valid_one_revoked"} {
			if strings.Contains(text, payload) {
				t.Fatalf("graph node %d leaked request/evidence payload %q in %q", row+1, payload, text)
			}
		}
	}
}
