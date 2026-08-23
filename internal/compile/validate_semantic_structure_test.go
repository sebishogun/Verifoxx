package compile

import (
	"math"
	"testing"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// clausePeerNames is the fixed column-name table indexed by clausePeers.
var clausePeerNames = [...]string{
	"ClauseEvidenceStarts", "ClauseEvidenceCounts", "ClauseRemediationStarts",
	"ClauseRemediationCounts", "ClauseOnSatisfied", "ClauseOnFalse",
	"ClauseOnMissing", "ClauseOnStale", "ClauseOnUnclear",
	"ClauseOnUnverifiable", "ClauseOnConflict", "ClauseExplanationIDs",
	"ClauseSourceStarts", "ClauseSourceEnds",
}

// requirementPeerNames is the fixed column-name table for the requirement
// peers.
var requirementPeerNames = [...]string{
	"RequirementApplicabilityRoots", "RequirementClauseStarts",
	"RequirementClauseCounts", "RequirementSourceStarts",
	"RequirementSourceEnds",
}

// acceptedDocNames names the canonical builders used by the zero-diagnostics
// test.
var acceptedDocNames = [...]string{"fixture", "buildMinimal", "buildInDoc", "buildCatalogDoc"}

func clausePeerName(i int) string {
	if i >= 0 && i < len(clausePeerNames) {
		return clausePeerNames[i]
	}
	return "column"
}

func requirementPeerName(i int) string {
	if i >= 0 && i < len(requirementPeerNames) {
		return requirementPeerNames[i]
	}
	return "column"
}

func acceptedDocName(i int) string {
	if i >= 0 && i < len(acceptedDocNames) {
		return acceptedDocNames[i]
	}
	return "doc"
}

// clausePeers lists every non-baseline Clause column so a test can append one
// element to each and lock the single-diagnostic-per-table rule.
var clausePeers = []func(*ast.Document){
	func(d *ast.Document) { d.ClauseEvidenceStarts = append(d.ClauseEvidenceStarts, 0) },
	func(d *ast.Document) { d.ClauseEvidenceCounts = append(d.ClauseEvidenceCounts, 0) },
	func(d *ast.Document) { d.ClauseRemediationStarts = append(d.ClauseRemediationStarts, 0) },
	func(d *ast.Document) { d.ClauseRemediationCounts = append(d.ClauseRemediationCounts, 0) },
	func(d *ast.Document) { d.ClauseOnSatisfied = append(d.ClauseOnSatisfied, 0) },
	func(d *ast.Document) { d.ClauseOnFalse = append(d.ClauseOnFalse, 0) },
	func(d *ast.Document) { d.ClauseOnMissing = append(d.ClauseOnMissing, 0) },
	func(d *ast.Document) { d.ClauseOnStale = append(d.ClauseOnStale, 0) },
	func(d *ast.Document) { d.ClauseOnUnclear = append(d.ClauseOnUnclear, 0) },
	func(d *ast.Document) { d.ClauseOnUnverifiable = append(d.ClauseOnUnverifiable, 0) },
	func(d *ast.Document) { d.ClauseOnConflict = append(d.ClauseOnConflict, 0) },
	func(d *ast.Document) { d.ClauseExplanationIDs = append(d.ClauseExplanationIDs, 0) },
	func(d *ast.Document) { d.ClauseSourceStarts = append(d.ClauseSourceStarts, 0) },
	func(d *ast.Document) { d.ClauseSourceEnds = append(d.ClauseSourceEnds, 0) },
}

func TestValidateStructuralClauseColumnLengths(t *testing.T) {
	for i, mutate := range clausePeers {
		t.Run(clausePeerName(i), func(t *testing.T) {
			doc, fields := buildMinimal(t)
			mutate(doc)
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeColumnLength, Table: TableClause},
			})
		})
	}
	t.Run("all fourteen peers", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		for _, mutate := range clausePeers {
			mutate(doc)
		}
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeColumnLength, Table: TableClause},
		})
	})
}

func TestValidateStructuralRequirementColumnLengths(t *testing.T) {
	peers := []func(*ast.Document){
		func(d *ast.Document) { d.RequirementApplicabilityRoots = append(d.RequirementApplicabilityRoots, 0) },
		func(d *ast.Document) { d.RequirementClauseStarts = append(d.RequirementClauseStarts, 0) },
		func(d *ast.Document) { d.RequirementClauseCounts = append(d.RequirementClauseCounts, 0) },
		func(d *ast.Document) { d.RequirementSourceStarts = append(d.RequirementSourceStarts, 0) },
		func(d *ast.Document) { d.RequirementSourceEnds = append(d.RequirementSourceEnds, 0) },
	}
	for i, mutate := range peers {
		t.Run(requirementPeerName(i), func(t *testing.T) {
			doc, fields := buildMinimal(t)
			mutate(doc)
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeColumnLength, Table: TableRequirement},
			})
		})
	}
	t.Run("all five peers", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		for _, mutate := range peers {
			mutate(doc)
		}
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeColumnLength, Table: TableRequirement},
		})
	})
}

func TestValidateStructuralAllTableOrder(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.NodeRefs = append(doc.NodeRefs, 0)
	doc.CompareOps = append(doc.CompareOps, 0)
	doc.GroupChildCounts = append(doc.GroupChildCounts, 0)
	doc.EvidenceKinds = append(doc.EvidenceKinds, 0)
	doc.ValueRefs = append(doc.ValueRefs, 0)
	doc.EvidenceKindNames = append(doc.EvidenceKindNames, 0)
	doc.EvidenceStateNames = append(doc.EvidenceStateNames, 0)
	doc.OutcomeNames = append(doc.OutcomeNames, 0)
	doc.RemediationKinds = append(doc.RemediationKinds, 0)
	doc.ClauseEvidenceStarts = append(doc.ClauseEvidenceStarts, 0)
	doc.RequirementApplicabilityRoots = append(doc.RequirementApplicabilityRoots, 0)
	doc.TemplateArgs = append(doc.TemplateArgs, 0)
	doc.ExplanationUncertaintyCounts = append(doc.ExplanationUncertaintyCounts, 0)
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeColumnLength, Table: TableNode},
		{Code: CodeColumnLength, Table: TableCompare},
		{Code: CodeColumnLength, Table: TableGroup},
		{Code: CodeColumnLength, Table: TableEvidenceNode},
		{Code: CodeColumnLength, Table: TableValue},
		{Code: CodeColumnLength, Table: TableEvidenceKind},
		{Code: CodeColumnLength, Table: TableEvidenceState},
		{Code: CodeColumnLength, Table: TableOutcome},
		{Code: CodeColumnLength, Table: TableRemediation},
		{Code: CodeColumnLength, Table: TableClause},
		{Code: CodeColumnLength, Table: TableRequirement},
		{Code: CodeColumnLength, Table: TableTemplate},
		{Code: CodeColumnLength, Table: TableExplanation},
	})
}

func TestValidateStructuralClauseAssertion(t *testing.T) {
	doc, _ := buildMinimal(t)
	nodeMax := schema.NodeID(len(doc.NodeKinds))
	tests := []struct {
		name string
		root schema.NodeID
	}{
		{"zero", 0},
		{"high", nodeMax + 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			doc.ClauseAssertionRoots[0] = tc.root
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidNodeReference, Table: TableClause, Member: MemberAssertion, Row: 1, Clause: 1, Node: tc.root, Span: ast.SourceSpan{Start: 0, End: 1}},
			})
		})
	}
}

func TestValidateStructuralClauseEvidenceCSR(t *testing.T) {
	tests := []struct {
		name  string
		start uint32
		count uint16
	}{
		{"start beyond total", 2, 0},
		{"count beyond total", 0, math.MaxUint16},
		{"start near MaxUint32", math.MaxUint32, 0},
		{"start near MaxUint32 with count", math.MaxUint32, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			doc.ClauseEvidenceStarts[0] = tc.start
			doc.ClauseEvidenceCounts[0] = tc.count
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidCSRRange, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Span: ast.SourceSpan{Start: 0, End: 1}},
			})
		})
	}
}

func TestValidateStructuralClauseEvidenceNodeIDs(t *testing.T) {
	doc, _ := buildMinimal(t)
	nodeMax := schema.NodeID(len(doc.NodeKinds))
	tests := []struct {
		name string
		id   schema.NodeID
	}{
		{"zero", 0},
		{"high", nodeMax + 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			doc.ClauseEvidenceNodeIDs[0] = tc.id
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidNodeReference, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1, Node: tc.id, Span: ast.SourceSpan{Start: 0, End: 1}},
			})
		})
	}
}

func TestValidateStructuralClauseRemediationCSR(t *testing.T) {
	tests := []struct {
		name  string
		start uint32
		count uint16
	}{
		{"start beyond total", 1, 0},
		{"count beyond total", 0, 1},
		{"start near MaxUint32", math.MaxUint32, 0},
		{"start near MaxUint32 with count", math.MaxUint32, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			doc.ClauseRemediationStarts[0] = tc.start
			doc.ClauseRemediationCounts[0] = tc.count
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidCSRRange, Table: TableClause, Member: MemberRemediations, Row: 1, Clause: 1, Span: ast.SourceSpan{Start: 0, End: 1}},
			})
		})
	}
}

func TestValidateStructuralClauseRemediationIDs(t *testing.T) {
	doc, _ := fixture(t)
	start := doc.ClauseRemediationStarts[0]
	remMax := schema.RemediationID(len(doc.RemediationKinds))
	tests := []struct {
		name string
		id   schema.RemediationID
	}{
		{"zero", 0},
		{"high", remMax + 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := fixture(t)
			span := ast.SourceSpan{Start: doc.ClauseSourceStarts[0], End: doc.ClauseSourceEnds[0]}
			doc.ClauseRemediationIDs[start] = tc.id
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidRemediation, Table: TableClause, Member: MemberRemediation, Row: 1, Clause: 1, Remediation: tc.id, Span: span},
			})
		})
	}
}

func TestValidateStructuralClauseOutcomes(t *testing.T) {
	high := func(doc *ast.Document) schema.OutcomeID {
		return schema.OutcomeID(len(doc.OutcomeNames) + 1)
	}
	tests := []struct {
		name   string
		member MemberKind
		set    func(*ast.Document, schema.OutcomeID)
	}{
		{"satisfied", MemberOutcomeSatisfied, func(d *ast.Document, o schema.OutcomeID) { d.ClauseOnSatisfied[0] = o }},
		{"false", MemberOutcomeFalse, func(d *ast.Document, o schema.OutcomeID) { d.ClauseOnFalse[0] = o }},
		{"missing", MemberOutcomeMissing, func(d *ast.Document, o schema.OutcomeID) { d.ClauseOnMissing[0] = o }},
		{"stale", MemberOutcomeStale, func(d *ast.Document, o schema.OutcomeID) { d.ClauseOnStale[0] = o }},
		{"unclear", MemberOutcomeUnclear, func(d *ast.Document, o schema.OutcomeID) { d.ClauseOnUnclear[0] = o }},
		{"unverifiable", MemberOutcomeUnverifiable, func(d *ast.Document, o schema.OutcomeID) { d.ClauseOnUnverifiable[0] = o }},
		{"conflict", MemberOutcomeConflict, func(d *ast.Document, o schema.OutcomeID) { d.ClauseOnConflict[0] = o }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			out := high(doc)
			tc.set(doc, out)
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidOutcome, Table: TableClause, Member: tc.member, Row: 1, Clause: 1, Outcome: out, Span: ast.SourceSpan{Start: 0, End: 1}},
			})
		})
	}

	t.Run("all seven high in order", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		out := high(doc)
		doc.ClauseOnSatisfied[0] = out
		doc.ClauseOnFalse[0] = out
		doc.ClauseOnMissing[0] = out
		doc.ClauseOnStale[0] = out
		doc.ClauseOnUnclear[0] = out
		doc.ClauseOnUnverifiable[0] = out
		doc.ClauseOnConflict[0] = out
		var v Validator
		span := ast.SourceSpan{Start: 0, End: 1}
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeSatisfied, Row: 1, Clause: 1, Outcome: out, Span: span},
			{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeFalse, Row: 1, Clause: 1, Outcome: out, Span: span},
			{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeMissing, Row: 1, Clause: 1, Outcome: out, Span: span},
			{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeStale, Row: 1, Clause: 1, Outcome: out, Span: span},
			{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeUnclear, Row: 1, Clause: 1, Outcome: out, Span: span},
			{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeUnverifiable, Row: 1, Clause: 1, Outcome: out, Span: span},
			{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeConflict, Row: 1, Clause: 1, Outcome: out, Span: span},
		})
	})

	t.Run("all zeros accepted structurally", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseOnSatisfied[0] = 0
		doc.ClauseOnFalse[0] = 0
		doc.ClauseOnMissing[0] = 0
		doc.ClauseOnStale[0] = 0
		doc.ClauseOnUnclear[0] = 0
		doc.ClauseOnUnverifiable[0] = 0
		doc.ClauseOnConflict[0] = 0
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), nil)
	})
}

func TestValidateStructuralClauseSpans(t *testing.T) {
	t.Run("reversed", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseSourceStarts[0] = 5
		doc.ClauseSourceEnds[0] = 2
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidSourceSpan, Table: TableClause, Member: MemberSpan, Row: 1, Clause: 1},
		})
	})
	t.Run("end beyond input", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseSourceEnds[0] = uint32(len(doc.InputBytes)) + 10
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidSourceSpan, Table: TableClause, Member: MemberSpan, Row: 1, Clause: 1},
		})
	})
}

func TestValidateStructuralClauseRowOrder(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.ClauseAssertionRoots[0] = 0
	doc.ClauseEvidenceStarts[0] = math.MaxUint32
	doc.ClauseRemediationStarts[0] = math.MaxUint32
	out := schema.OutcomeID(len(doc.OutcomeNames) + 1)
	doc.ClauseOnSatisfied[0] = out
	doc.ClauseOnFalse[0] = out
	doc.ClauseOnMissing[0] = out
	doc.ClauseOnStale[0] = out
	doc.ClauseOnUnclear[0] = out
	doc.ClauseOnUnverifiable[0] = out
	doc.ClauseOnConflict[0] = out
	doc.ClauseSourceStarts[0] = 5
	doc.ClauseSourceEnds[0] = 2
	var v Validator
	got := v.validateStructure(nil, doc, fields)
	want(t, got, []Diagnostic{
		{Code: CodeInvalidNodeReference, Table: TableClause, Member: MemberAssertion, Row: 1, Clause: 1},
		{Code: CodeInvalidCSRRange, Table: TableClause, Member: MemberEvidence, Row: 1, Clause: 1},
		{Code: CodeInvalidCSRRange, Table: TableClause, Member: MemberRemediations, Row: 1, Clause: 1},
		{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeSatisfied, Row: 1, Clause: 1, Outcome: out},
		{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeFalse, Row: 1, Clause: 1, Outcome: out},
		{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeMissing, Row: 1, Clause: 1, Outcome: out},
		{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeStale, Row: 1, Clause: 1, Outcome: out},
		{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeUnclear, Row: 1, Clause: 1, Outcome: out},
		{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeUnverifiable, Row: 1, Clause: 1, Outcome: out},
		{Code: CodeInvalidOutcome, Table: TableClause, Member: MemberOutcomeConflict, Row: 1, Clause: 1, Outcome: out},
		{Code: CodeInvalidSourceSpan, Table: TableClause, Member: MemberSpan, Row: 1, Clause: 1},
	})
	if v.clauseState[0]&clauseStateUnsafe == 0 {
		t.Fatal("clause 1 not marked unsafe for invalid assertion and CSR ranges")
	}
}

func TestValidateStructuralClauseUnsafe(t *testing.T) {
	t.Run("bad assertion", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseAssertionRoots[0] = 0
		var v Validator
		v.validateStructure(nil, doc, fields)
		if v.clauseState[0]&clauseStateUnsafe == 0 {
			t.Fatal("invalid assertion did not mark clause unsafe")
		}
	})
	t.Run("bad evidence CSR", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseEvidenceStarts[0] = math.MaxUint32
		var v Validator
		v.validateStructure(nil, doc, fields)
		if v.clauseState[0]&clauseStateUnsafe == 0 {
			t.Fatal("invalid evidence CSR did not mark clause unsafe")
		}
	})
	t.Run("bad remediation CSR", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseRemediationStarts[0] = math.MaxUint32
		var v Validator
		v.validateStructure(nil, doc, fields)
		if v.clauseState[0]&clauseStateUnsafe == 0 {
			t.Fatal("invalid remediation CSR did not mark clause unsafe")
		}
	})
	t.Run("bad evidence edge target not unsafe", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseEvidenceNodeIDs[0] = schema.NodeID(len(doc.NodeKinds) + 1)
		var v Validator
		v.validateStructure(nil, doc, fields)
		if v.clauseState[0]&clauseStateUnsafe != 0 {
			t.Fatal("bad evidence edge target must not mark clause unsafe")
		}
	})
	t.Run("bad remediation edge target not unsafe", func(t *testing.T) {
		doc, fields := fixture(t)
		start := doc.ClauseRemediationStarts[0]
		doc.ClauseRemediationIDs[start] = schema.RemediationID(len(doc.RemediationKinds) + 1)
		var v Validator
		v.validateStructure(nil, doc, fields)
		if v.clauseState[0]&clauseStateUnsafe != 0 {
			t.Fatal("bad remediation edge target must not mark clause unsafe")
		}
	})
	t.Run("rows beyond safe min", func(t *testing.T) {
		doc, fields := buildDoc(t, 6, 2)
		doc.ClauseOnSatisfied = doc.ClauseOnSatisfied[:1]
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeColumnLength, Table: TableClause},
		})
		if v.clauseState[0]&clauseStateUnsafe != 0 {
			t.Fatal("safe clause 1 marked unsafe")
		}
		if v.clauseState[1]&clauseStateUnsafe == 0 {
			t.Fatal("clause 2 beyond safe min not marked unsafe")
		}
	})
}

func TestValidateStructuralRequirementApplicability(t *testing.T) {
	doc, _ := buildMinimal(t)
	nodeMax := schema.NodeID(len(doc.NodeKinds))
	tests := []struct {
		name string
		root schema.NodeID
	}{
		{"zero", 0},
		{"high", nodeMax + 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			doc.RequirementApplicabilityRoots[0] = tc.root
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidNodeReference, Table: TableRequirement, Member: MemberApplicability, Row: 1, Requirement: 1, Node: tc.root, Span: ast.SourceSpan{Start: 0, End: 1}},
			})
		})
	}
}

func TestValidateStructuralRequirementClauseCSR(t *testing.T) {
	tests := []struct {
		name  string
		start uint32
		count uint16
	}{
		{"start beyond total", 2, 0},
		{"count beyond total", 0, math.MaxUint16},
		{"start near MaxUint32", math.MaxUint32, 0},
		{"start near MaxUint32 with count", math.MaxUint32, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			doc.RequirementClauseStarts[0] = tc.start
			doc.RequirementClauseCounts[0] = tc.count
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidCSRRange, Table: TableRequirement, Member: MemberClauses, Row: 1, Requirement: 1, Span: ast.SourceSpan{Start: 0, End: 1}},
			})
		})
	}
}

func TestValidateStructuralRequirementClauseIDs(t *testing.T) {
	doc, _ := buildMinimal(t)
	clauseMax := schema.ClauseID(len(doc.ClauseAssertionRoots))
	tests := []struct {
		name string
		id   schema.ClauseID
	}{
		{"zero", 0},
		{"high", clauseMax + 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			doc, fields := buildMinimal(t)
			doc.RequirementClauseIDs[0] = tc.id
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
				{Code: CodeInvalidPayloadRef, Table: TableRequirement, Member: MemberClause, Row: 1, Requirement: 1, Clause: tc.id, Span: ast.SourceSpan{Start: 0, End: 1}},
			})
		})
	}
}

func TestValidateStructuralRequirementZeroID(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.RequirementIDs[0] = 0
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), nil)
}

func TestValidateStructuralRequirementSpans(t *testing.T) {
	t.Run("reversed", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.RequirementSourceStarts[0] = 5
		doc.RequirementSourceEnds[0] = 2
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidSourceSpan, Table: TableRequirement, Member: MemberSpan, Row: 1, Requirement: 1},
		})
	})
	t.Run("end beyond input", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.RequirementSourceEnds[0] = uint32(len(doc.InputBytes)) + 10
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeInvalidSourceSpan, Table: TableRequirement, Member: MemberSpan, Row: 1, Requirement: 1},
		})
	})
}

func TestValidateStructuralRequirementRowOrder(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.RequirementApplicabilityRoots[0] = 0
	doc.RequirementClauseStarts[0] = math.MaxUint32
	doc.RequirementSourceStarts[0] = 5
	doc.RequirementSourceEnds[0] = 2
	var v Validator
	want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
		{Code: CodeInvalidNodeReference, Table: TableRequirement, Member: MemberApplicability, Row: 1, Requirement: 1},
		{Code: CodeInvalidCSRRange, Table: TableRequirement, Member: MemberClauses, Row: 1, Requirement: 1},
		{Code: CodeInvalidSourceSpan, Table: TableRequirement, Member: MemberSpan, Row: 1, Requirement: 1},
	})
}

func TestValidateStructuralClauseRequirementTruncationNoPanic(t *testing.T) {
	trunc := func(t *testing.T, mutate func(*ast.Document)) {
		t.Helper()
		doc, fields := buildMinimal(t)
		mutate(doc)
		var v Validator
		v.validateStructure(nil, doc, fields)
	}
	t.Run("clause every peer", func(t *testing.T) {
		trunc(t, func(d *ast.Document) {
			d.ClauseEvidenceStarts = d.ClauseEvidenceStarts[:0]
			d.ClauseEvidenceCounts = d.ClauseEvidenceCounts[:0]
			d.ClauseRemediationStarts = d.ClauseRemediationStarts[:0]
			d.ClauseRemediationCounts = d.ClauseRemediationCounts[:0]
			d.ClauseOnSatisfied = d.ClauseOnSatisfied[:0]
			d.ClauseOnFalse = d.ClauseOnFalse[:0]
			d.ClauseOnMissing = d.ClauseOnMissing[:0]
			d.ClauseOnStale = d.ClauseOnStale[:0]
			d.ClauseOnUnclear = d.ClauseOnUnclear[:0]
			d.ClauseOnUnverifiable = d.ClauseOnUnverifiable[:0]
			d.ClauseOnConflict = d.ClauseOnConflict[:0]
			d.ClauseSourceStarts = d.ClauseSourceStarts[:0]
			d.ClauseSourceEnds = d.ClauseSourceEnds[:0]
		})
	})
	t.Run("requirement every peer", func(t *testing.T) {
		trunc(t, func(d *ast.Document) {
			d.RequirementApplicabilityRoots = d.RequirementApplicabilityRoots[:0]
			d.RequirementClauseStarts = d.RequirementClauseStarts[:0]
			d.RequirementClauseCounts = d.RequirementClauseCounts[:0]
			d.RequirementSourceStarts = d.RequirementSourceStarts[:0]
			d.RequirementSourceEnds = d.RequirementSourceEnds[:0]
		})
	})
	t.Run("both every peer", func(t *testing.T) {
		doc, fields := buildMinimal(t)
		doc.ClauseEvidenceStarts = doc.ClauseEvidenceStarts[:0]
		doc.ClauseEvidenceCounts = doc.ClauseEvidenceCounts[:0]
		doc.ClauseRemediationStarts = doc.ClauseRemediationStarts[:0]
		doc.ClauseRemediationCounts = doc.ClauseRemediationCounts[:0]
		doc.ClauseOnSatisfied = doc.ClauseOnSatisfied[:0]
		doc.ClauseOnFalse = doc.ClauseOnFalse[:0]
		doc.ClauseOnMissing = doc.ClauseOnMissing[:0]
		doc.ClauseOnStale = doc.ClauseOnStale[:0]
		doc.ClauseOnUnclear = doc.ClauseOnUnclear[:0]
		doc.ClauseOnUnverifiable = doc.ClauseOnUnverifiable[:0]
		doc.ClauseOnConflict = doc.ClauseOnConflict[:0]
		doc.ClauseSourceStarts = doc.ClauseSourceStarts[:0]
		doc.ClauseSourceEnds = doc.ClauseSourceEnds[:0]
		doc.RequirementApplicabilityRoots = doc.RequirementApplicabilityRoots[:0]
		doc.RequirementClauseStarts = doc.RequirementClauseStarts[:0]
		doc.RequirementClauseCounts = doc.RequirementClauseCounts[:0]
		doc.RequirementSourceStarts = doc.RequirementSourceStarts[:0]
		doc.RequirementSourceEnds = doc.RequirementSourceEnds[:0]
		var v Validator
		want(t, v.validateStructure(nil, doc, fields), []Diagnostic{
			{Code: CodeColumnLength, Table: TableClause},
			{Code: CodeColumnLength, Table: TableRequirement},
		})
	})
}

func TestValidateStructuralCanonicalTablesZero(t *testing.T) {
	docs := []func(t *testing.T) (*ast.Document, *schema.Schema){
		func(t *testing.T) (*ast.Document, *schema.Schema) { return fixture(t) },
		buildMinimal,
		buildInDoc,
		buildCatalogDoc,
	}
	for i, build := range docs {
		t.Run(acceptedDocName(i), func(t *testing.T) {
			doc, fields := build(t)
			var v Validator
			want(t, v.validateStructure(nil, doc, fields), nil)
		})
	}
}
