package eval

import (
	"slices"
	"testing"

	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

const (
	provenanceRequirementNode schema.NodeID = 101
	provenanceAssertionNode   schema.NodeID = 202
	provenanceEvidenceNode    schema.NodeID = 303
)

func installProvenanceSourceNodes(t testing.TB, p *program.Program) {
	t.Helper()
	for row := range p.RequirementSourceNodeIDs {
		p.RequirementSourceNodeIDs[row] = provenanceRequirementNode
	}
	for row := range p.ClauseAssertionSourceNodeIDs {
		p.ClauseAssertionSourceNodeIDs[row] = provenanceAssertionNode
	}
	for row := range p.ClauseEvidenceSourceNodeIDs {
		p.ClauseEvidenceSourceNodeIDs[row] = provenanceEvidenceNode
	}
	p.EvidenceIssueNodeIDs[0] = provenanceEvidenceNode
	if err := p.ValidateResultTables(); err != nil {
		t.Fatalf("ValidateResultTables: %v", err)
	}
}

func oneRowProvenanceBatch(
	t testing.TB,
	p *program.Program,
	assertion schema.SymbolID,
	records []EvidenceRecord,
	refs []uint32,
) Batch {
	t.Helper()
	var builder Builder
	if err := builder.Begin(p, 1, uint32(len(records)), uint32(len(refs))); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := builder.SetRequestID(0, 1); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetSymbol(0, 1, executionSymbolActive); err != nil {
		t.Fatal(err)
	}
	if assertion != 0 {
		if err := builder.SetSymbol(0, 2, assertion); err != nil {
			t.Fatal(err)
		}
	}
	for row, record := range records {
		if err := builder.SetEvidence(uint32(row), record); err != nil {
			t.Fatalf("SetEvidence(%d): %v", row, err)
		}
	}
	if err := builder.SetEvidenceCSR([]uint32{0, uint32(len(refs))}, refs); err != nil {
		t.Fatalf("SetEvidenceCSR: %v", err)
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return batch
}

func setProvenanceEvidenceQualifiers(p *program.Program, subject, scope, timing schema.SymbolID) {
	p.EvidenceSubjects = make([]schema.SymbolID, len(p.Opcodes))
	p.EvidenceScopes = make([]schema.SymbolID, len(p.Opcodes))
	p.EvidenceTimings = make([]schema.SymbolID, len(p.Opcodes))
	evidenceRow := int(p.ClauseEvidenceIDs[0] - 1)
	p.EvidenceSubjects[evidenceRow] = subject
	p.EvidenceScopes[evidenceRow] = scope
	p.EvidenceTimings[evidenceRow] = timing
}

func TestExecutorExplanationProvenancePaths(t *testing.T) {
	p := executionTestProgram(t, 1)
	installProvenanceSourceNodes(t, p)
	batch := executionPathBatch(t, p)
	var executor Executor
	var got result.Batch
	if err := executor.Execute(&got, p, batch); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !slices.Equal(got.DriverExplanations, []schema.ExplanationID{1, 2, 3, 4, 7, 3}) {
		t.Fatalf("driver explanations = %v", got.DriverExplanations)
	}
	if !slices.Equal(got.DriverNodes, []schema.NodeID{
		provenanceAssertionNode,
		provenanceAssertionNode,
		provenanceAssertionNode,
		provenanceEvidenceNode,
		provenanceEvidenceNode,
		provenanceRequirementNode,
	}) {
		t.Fatalf("driver source nodes = %v", got.DriverNodes)
	}
	if !slices.Equal(got.ReasonIDs, []schema.ReasonID{
		truth.ReasonMissing,
		truth.ReasonStale,
		truth.ReasonConflict,
		truth.ReasonMissing,
	}) || !slices.Equal(got.ReasonNodes, []schema.NodeID{
		provenanceAssertionNode,
		provenanceEvidenceNode,
		provenanceEvidenceNode,
		provenanceRequirementNode,
	}) || !slices.Equal(got.ReasonEvidenceIDs, []schema.EvidenceID{0, 2, 3, 0}) ||
		!slices.Equal(got.ReasonEvidenceStates, []schema.EvidenceStateID{0, 2, 3, 0}) {
		t.Fatalf("reason provenance = reasons %v nodes %v evidence %v states %v",
			got.ReasonIDs, got.ReasonNodes, got.ReasonEvidenceIDs, got.ReasonEvidenceStates)
	}
	if !slices.Equal(got.EvidenceOffsets, []uint32{0, 1, 2, 3, 4, 5, 6, 7}) ||
		!slices.Equal(got.EvidenceIDs, []schema.EvidenceID{1, 1, 1, 1, 2, 3, 1}) {
		t.Fatalf("request evidence = %v/%v", got.EvidenceOffsets, got.EvidenceIDs)
	}
}

func TestExecutorExplanationProvenanceEvidenceReasons(t *testing.T) {
	tests := []struct {
		name                           string
		reason                         schema.ReasonID
		explanation                    schema.ExplanationID
		state                          schema.EvidenceStateID
		expectedSubject, expectedScope schema.SymbolID
		expectedTiming                 schema.SymbolID
		actualSubject, actualScope     schema.SymbolID
		actualTiming                   schema.SymbolID
	}{
		{name: "missing", reason: truth.ReasonMissing, explanation: 3},
		{name: "stale", reason: truth.ReasonStale, explanation: 4, state: 2},
		{name: "unclear", reason: truth.ReasonUnclear, explanation: 5, state: 4},
		{name: "unverifiable", reason: truth.ReasonUnverifiable, explanation: 6, state: 5},
		{name: "invalid", reason: truth.ReasonInvalid, explanation: 6, state: 6},
		{
			name: "wrong scope", reason: truth.ReasonWrongScope, explanation: 6, state: 1,
			expectedScope: executionSymbolScopeA, actualScope: executionSymbolScopeB,
		},
		{
			name: "wrong subject", reason: truth.ReasonWrongSubject, explanation: 6, state: 1,
			expectedSubject: executionSymbolSubjectA, actualSubject: executionSymbolSubjectB,
		},
		{
			name: "wrong timing", reason: truth.ReasonWrongTiming, explanation: 6, state: 1,
			expectedTiming: executionSymbolTimingA, actualTiming: executionSymbolTimingB,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := executionTestProgram(t, 1)
			installProvenanceSourceNodes(t, p)
			setProvenanceEvidenceQualifiers(p, tt.expectedSubject, tt.expectedScope, tt.expectedTiming)
			var records []EvidenceRecord
			var refs []uint32
			if tt.state != 0 {
				records = []EvidenceRecord{{
					ID: 41, Kind: 1, State: tt.state,
					Subject: tt.actualSubject, Scope: tt.actualScope, Timing: tt.actualTiming,
				}}
				refs = []uint32{0}
			}
			batch := oneRowProvenanceBatch(t, p, executionSymbolYes, records, refs)
			var executor Executor
			var got result.Batch
			if err := executor.Execute(&got, p, batch); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if !slices.Equal(got.DriverExplanations, []schema.ExplanationID{tt.explanation}) ||
				!slices.Equal(got.ReasonIDs, []schema.ReasonID{tt.reason}) ||
				!slices.Equal(got.ReasonNodes, []schema.NodeID{provenanceEvidenceNode}) {
				t.Fatalf("decision provenance = explanations %v reasons %v nodes %v",
					got.DriverExplanations, got.ReasonIDs, got.ReasonNodes)
			}
			wantID := schema.EvidenceID(41)
			if tt.reason == truth.ReasonMissing {
				wantID = 0
			}
			if !slices.Equal(got.ReasonEvidenceIDs, []schema.EvidenceID{wantID}) ||
				!slices.Equal(got.ReasonEvidenceStates, []schema.EvidenceStateID{tt.state}) {
				t.Fatalf("causal evidence = %v/%v, want %d/%d",
					got.ReasonEvidenceIDs, got.ReasonEvidenceStates, wantID, tt.state)
			}
		})
	}
}

func TestExecutorExplanationProvenanceMultipleReasonsAscending(t *testing.T) {
	p := executionTestProgram(t, 1)
	installProvenanceSourceNodes(t, p)
	setProvenanceEvidenceQualifiers(p, executionSymbolSubjectA, executionSymbolScopeA, executionSymbolTimingA)
	record := EvidenceRecord{
		ID: 51, Kind: 1, State: 2,
		Subject: executionSymbolSubjectB, Scope: executionSymbolScopeB, Timing: executionSymbolTimingB,
	}
	batch := oneRowProvenanceBatch(t, p, executionSymbolYes, []EvidenceRecord{record}, []uint32{0})
	var executor Executor
	var got result.Batch
	if err := executor.Execute(&got, p, batch); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	wantReasons := []schema.ReasonID{
		truth.ReasonStale,
		truth.ReasonWrongScope,
		truth.ReasonWrongSubject,
		truth.ReasonWrongTiming,
	}
	if !slices.Equal(got.ReasonIDs, wantReasons) ||
		!slices.Equal(got.ReasonNodes, []schema.NodeID{303, 303, 303, 303}) ||
		!slices.Equal(got.ReasonEvidenceIDs, []schema.EvidenceID{51, 51, 51, 51}) ||
		!slices.Equal(got.ReasonEvidenceStates, []schema.EvidenceStateID{2, 2, 2, 2}) ||
		!slices.Equal(got.DriverReasons, []schema.ReasonID{truth.ReasonStale}) ||
		!slices.Equal(got.DriverExplanations, []schema.ExplanationID{4}) {
		t.Fatalf("multiple reason provenance = reasons %v nodes %v evidence %v states %v driver %v/%v",
			got.ReasonIDs, got.ReasonNodes, got.ReasonEvidenceIDs, got.ReasonEvidenceStates,
			got.DriverReasons, got.DriverExplanations)
	}
}

func TestExecutorExplanationProvenanceAggregateConflictAndDuplicateEdges(t *testing.T) {
	tests := []struct {
		name        string
		refs        []uint32
		wantIDs     []schema.EvidenceID
		causalID    schema.EvidenceID
		causalState schema.EvidenceStateID
	}{
		{
			name: "positive first", refs: []uint32{1, 0, 1},
			wantIDs: []schema.EvidenceID{20, 10, 20}, causalID: 20, causalState: 1,
		},
		{
			name: "negative first", refs: []uint32{0, 1, 0},
			wantIDs: []schema.EvidenceID{10, 20, 10}, causalID: 10, causalState: 7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := executionTestProgram(t, 1)
			installProvenanceSourceNodes(t, p)
			records := []EvidenceRecord{
				evidenceRecord(10, 1, 7),
				evidenceRecord(20, 1, 1),
			}
			batch := oneRowProvenanceBatch(t, p, executionSymbolYes, records, tt.refs)
			var executor Executor
			var got result.Batch
			if err := executor.Execute(&got, p, batch); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if !slices.Equal(got.ReasonIDs, []schema.ReasonID{truth.ReasonConflict}) ||
				!slices.Equal(got.ReasonNodes, []schema.NodeID{provenanceEvidenceNode}) ||
				!slices.Equal(got.ReasonEvidenceIDs, []schema.EvidenceID{tt.causalID}) ||
				!slices.Equal(got.ReasonEvidenceStates, []schema.EvidenceStateID{tt.causalState}) {
				t.Fatalf("aggregate conflict provenance = %v/%v/%v/%v",
					got.ReasonIDs, got.ReasonNodes, got.ReasonEvidenceIDs, got.ReasonEvidenceStates)
			}
			if !slices.Equal(got.EvidenceOffsets, []uint32{0, 3}) || !slices.Equal(got.EvidenceIDs, tt.wantIDs) {
				t.Fatalf("duplicate edge evidence = %v/%v", got.EvidenceOffsets, got.EvidenceIDs)
			}
		})
	}
}

func TestExecutorExplanationProvenanceSecondEvidenceEdge(t *testing.T) {
	p := executionTestProgram(t, 1)
	installProvenanceSourceNodes(t, p)
	p.EvidenceKindNames = append(p.EvidenceKindNames, executionSymbolSubjectA)
	second := appendExecutorInstruction(p, program.OpcodeEvidence, 0, 0, nil, 2, 1)
	p.RootFlags[second-1] = program.RootEvidence
	p.TruthSlots = append(p.TruthSlots, 4)
	p.ReasonSlots = append(p.ReasonSlots, 4)
	p.TruthSlotCount = 4
	p.ReasonSlotCount = 4
	p.ClauseEvidenceIDs = []schema.InstructionID{3, second}
	p.ClauseEvidenceCounts[0] = 2
	p.ClauseEvidenceSourceNodeIDs = []schema.NodeID{provenanceEvidenceNode, 404}
	p.EvidenceIssueNodeIDs = []schema.NodeID{provenanceEvidenceNode, 404}
	for range result.EvidenceIssueTemplateCount {
		p.EvidenceIssueTemplateIDs = append(p.EvidenceIssueTemplateIDs, 1)
	}
	if err := p.ValidateResultTables(); err != nil {
		t.Fatalf("ValidateResultTables: %v", err)
	}

	records := []EvidenceRecord{
		evidenceRecord(1, 1, 1),
		evidenceRecord(2, 2, 2),
	}
	batch := oneRowProvenanceBatch(t, p, executionSymbolYes, records, []uint32{0, 1})
	var executor Executor
	var got result.Batch
	if err := executor.Execute(&got, p, batch); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !slices.Equal(got.DriverNodes, []schema.NodeID{404}) ||
		!slices.Equal(got.ReasonNodes, []schema.NodeID{404}) ||
		!slices.Equal(got.ReasonEvidenceIDs, []schema.EvidenceID{2}) ||
		!slices.Equal(got.ReasonEvidenceStates, []schema.EvidenceStateID{2}) {
		t.Fatalf("second-edge provenance = driver %v reason %v evidence %v state %v",
			got.DriverNodes, got.ReasonNodes, got.ReasonEvidenceIDs, got.ReasonEvidenceStates)
	}
}

func TestExecutorExplanationProvenanceReuseAllocations(t *testing.T) {
	p := executionTestProgram(t, 1)
	installProvenanceSourceNodes(t, p)
	batch := executionPathBatch(t, p)
	var executor Executor
	var dst result.Batch
	if err := executor.Execute(&dst, p, batch); err != nil {
		t.Fatal(err)
	}
	poisonResult(&dst)
	if err := executor.Execute(&dst, p, batch); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dst.ReasonEvidenceIDs, []schema.EvidenceID{0, 2, 3, 0}) ||
		!slices.Equal(dst.DriverExplanations, []schema.ExplanationID{1, 2, 3, 4, 7, 3}) {
		t.Fatalf("reused provenance = evidence %v explanations %v", dst.ReasonEvidenceIDs, dst.DriverExplanations)
	}
	var runErr error
	if allocs := testing.AllocsPerRun(100, func() {
		runErr = executor.Execute(&dst, p, batch)
	}); allocs != 0 {
		t.Fatalf("warm Execute allocations = %g, want 0", allocs)
	}
	if runErr != nil {
		t.Fatalf("warm Execute: %v", runErr)
	}
}
