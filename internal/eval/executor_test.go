package eval

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"

	policyindex "github.com/sebishogun/verifoxx/internal/index"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

func appendExecutorInstruction(
	p *program.Program,
	opcode program.Opcode,
	field schema.FieldID,
	value schema.ValueID,
	operands []schema.InstructionID,
	evidenceKind schema.EvidenceKindID,
	evidenceState schema.EvidenceStateID,
) schema.InstructionID {
	operandStart := uint32(len(p.Operands))
	p.Operands = append(p.Operands, operands...)
	p.Opcodes = append(p.Opcodes, opcode)
	p.Fields = append(p.Fields, field)
	p.Values = append(p.Values, value)
	p.ListStarts = append(p.ListStarts, 0)
	p.ListCounts = append(p.ListCounts, 0)
	p.OperandStarts = append(p.OperandStarts, operandStart)
	p.OperandCounts = append(p.OperandCounts, uint16(len(operands)))
	p.EvidenceKinds = append(p.EvidenceKinds, evidenceKind)
	p.EvidenceStates = append(p.EvidenceStates, evidenceState)
	p.RootFlags = append(p.RootFlags, 0)
	id := schema.InstructionID(len(p.Opcodes))
	p.InstructionNodes = append(p.InstructionNodes, schema.NodeID(id))
	p.InstructionSourceStarts = append(p.InstructionSourceStarts, uint32(id-1))
	p.InstructionSourceEnds = append(p.InstructionSourceEnds, uint32(id))
	return id
}

func executorPredicateBatch(t testing.TB, p *program.Program, rows uint32) Batch {
	t.Helper()
	var builder Builder
	if err := builder.Begin(p, rows, 0, 0); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for row := uint32(0); row < rows; row++ {
		if err := builder.SetRequestID(row, schema.RequestID(row+1)); err != nil {
			t.Fatalf("SetRequestID(%d): %v", row, err)
		}
		switch row & 3 {
		case 0:
			if err := builder.SetSymbol(row, 1, 1); err != nil {
				t.Fatal(err)
			}
			if err := builder.SetInteger(row, 2, 7); err != nil {
				t.Fatal(err)
			}
			if err := builder.SetBoolean(row, 3, true); err != nil {
				t.Fatal(err)
			}
		case 1:
			if err := builder.SetSymbol(row, 1, 2); err != nil {
				t.Fatal(err)
			}
			if err := builder.SetInteger(row, 2, 7); err != nil {
				t.Fatal(err)
			}
			if err := builder.SetBoolean(row, 3, false); err != nil {
				t.Fatal(err)
			}
		case 2:
			if err := builder.SetInteger(row, 2, 8); err != nil {
				t.Fatal(err)
			}
		case 3:
			if err := builder.SetSymbol(row, 1, 1); err != nil {
				t.Fatal(err)
			}
			if err := builder.SetBoolean(row, 3, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return batch
}

func executorGroupProgram(t testing.TB, opcode program.Opcode, truthDst, reasonDst schema.SlotID) (*program.Program, []schema.InstructionID) {
	t.Helper()
	p := predicateTestProgram(t)
	operands := []schema.InstructionID{
		appendExecutorInstruction(p, program.OpcodeEqual, 1, 1, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeEqual, 2, 2, nil, 0, 0),
		appendExecutorInstruction(p, program.OpcodeEqual, 3, 3, nil, 0, 0),
	}
	root := appendExecutorInstruction(p, opcode, 0, 0, operands, 0, 0)
	p.RootFlags[root-1] = program.RootAssertion
	p.TruthSlots = []schema.SlotID{1, 2, 3, truthDst}
	p.ReasonSlots = []schema.SlotID{1, 2, 3, reasonDst}
	p.TruthSlotCount = uint32(max(3, truthDst))
	p.ReasonSlotCount = uint32(max(3, reasonDst))
	return p, operands
}

func expectedGroup(t testing.TB, p *program.Program, batch Batch, opcode program.Opcode, operands []schema.InstructionID) (truth.Planes, ReasonPlanes) {
	t.Helper()
	leafTruth := make([]truth.Planes, len(operands))
	leafReasons := make([]ReasonPlanes, len(operands))
	for i, id := range operands {
		leafTruth[i], leafReasons[i] = makeLeafOutputs(batch.Rows)
		evalPredicate(leafTruth[i], leafReasons[i], batch, p, id)
	}
	dst, reasons := makeLeafOutputs(batch.Rows)
	copy(dst.Positive, leafTruth[0].Positive)
	copy(dst.Negative, leafTruth[0].Negative)
	copy(reasons.Words, leafReasons[0].Words)
	for i := 1; i < len(operands); i++ {
		if opcode == program.OpcodeAll {
			truth.And(dst, dst, leafTruth[i], batch.Rows)
		} else {
			truth.Or(dst, dst, leafTruth[i], batch.Rows)
		}
		for word := range reasons.Words {
			reasons.Words[word] |= leafReasons[i].Words[word]
		}
	}
	return dst, reasons
}

func TestExecuteScheduleGroupSlotAliases(t *testing.T) {
	const rows = 65
	tests := []struct {
		name                string
		opcode              program.Opcode
		truthDst, reasonDst schema.SlotID
	}{
		{"all first truth last reason", program.OpcodeAll, 1, 3},
		{"all middle truth fresh reason", program.OpcodeAll, 2, 4},
		{"any last truth first reason", program.OpcodeAny, 3, 1},
		{"any fresh truth middle reason", program.OpcodeAny, 4, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, operands := executorGroupProgram(t, tt.opcode, tt.truthDst, tt.reasonDst)
			batch := executorPredicateBatch(t, p, rows)
			wantTruth, wantReasons := expectedGroup(t, p, batch, tt.opcode, operands)

			var executor Executor
			if err := executor.prepare(p, batch); err != nil {
				t.Fatalf("prepare: %v", err)
			}
			executor.executeSchedule(p, batch)
			gotTruth := executor.truthSlot(tt.truthDst, rows)
			gotReasons := executor.reasonSlot(tt.reasonDst, rows)
			if !slices.Equal(gotTruth.Positive, wantTruth.Positive) ||
				!slices.Equal(gotTruth.Negative, wantTruth.Negative) ||
				!slices.Equal(gotReasons.Words, wantReasons.Words) {
				t.Fatalf("group result = %#x/%#x %#x, want %#x/%#x %#x",
					gotTruth.Positive, gotTruth.Negative, gotReasons.Words,
					wantTruth.Positive, wantTruth.Negative, wantReasons.Words)
			}
		})
	}
}

func TestExecuteScheduleNotAliases(t *testing.T) {
	for _, dstSlot := range []schema.SlotID{1, 2} {
		t.Run(string(rune('0'+dstSlot)), func(t *testing.T) {
			p := predicateTestProgram(t)
			leaf := appendExecutorInstruction(p, program.OpcodeEqual, 1, 1, nil, 0, 0)
			root := appendExecutorInstruction(p, program.OpcodeNot, 0, 0, []schema.InstructionID{leaf}, 0, 0)
			p.RootFlags[root-1] = program.RootAssertion
			p.TruthSlots = []schema.SlotID{1, dstSlot}
			p.ReasonSlots = []schema.SlotID{1, dstSlot}
			p.TruthSlotCount = uint32(max(1, dstSlot))
			p.ReasonSlotCount = uint32(max(1, dstSlot))
			batch := executorPredicateBatch(t, p, 65)
			wantTruth, wantReasons := makeLeafOutputs(batch.Rows)
			evalPredicate(wantTruth, wantReasons, batch, p, leaf)
			truth.Not(wantTruth, wantTruth, batch.Rows)

			var executor Executor
			if err := executor.prepare(p, batch); err != nil {
				t.Fatalf("prepare: %v", err)
			}
			executor.executeSchedule(p, batch)
			gotTruth := executor.truthSlot(dstSlot, batch.Rows)
			gotReasons := executor.reasonSlot(dstSlot, batch.Rows)
			if !slices.Equal(gotTruth.Positive, wantTruth.Positive) ||
				!slices.Equal(gotTruth.Negative, wantTruth.Negative) ||
				!slices.Equal(gotReasons.Words, wantReasons.Words) {
				t.Fatal("Not result differs from scalar reference")
			}
		})
	}
}

func TestExecuteScheduleEvidenceLeaf(t *testing.T) {
	p := evidenceEvalTestProgram()
	root := appendExecutorInstruction(
		p, program.OpcodeEvidence, 0, 0, nil,
		testEvidenceKindApproval, testEvidenceStateValid,
	)
	p.RootFlags[root-1] = program.RootEvidence
	p.TruthSlots = []schema.SlotID{1}
	p.ReasonSlots = []schema.SlotID{1}
	p.TruthSlotCount = 1
	p.ReasonSlotCount = 1
	batch := scalarEvidenceBatch(t, p, 65)
	var states EvidenceStateIndex
	if err := states.Bind(p); err != nil {
		t.Fatalf("Bind reference states: %v", err)
	}
	wantTruth, wantReasons := makeLeafOutputs(batch.Rows)
	evalEvidence(wantTruth, wantReasons, batch, p, &states, EvidencePredicate{
		Kind: testEvidenceKindApproval, State: testEvidenceStateValid,
	})

	var executor Executor
	if err := executor.prepare(p, batch); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	executor.executeSchedule(p, batch)
	gotTruth := executor.truthSlot(1, batch.Rows)
	gotReasons := executor.reasonSlot(1, batch.Rows)
	if !slices.Equal(gotTruth.Positive, wantTruth.Positive) ||
		!slices.Equal(gotTruth.Negative, wantTruth.Negative) ||
		!slices.Equal(gotReasons.Words, wantReasons.Words) {
		t.Fatal("Evidence result differs from scalar reference")
	}
}

const (
	executionSymbolActive schema.SymbolID = iota + 1
	executionSymbolInactive
	executionSymbolYes
	executionSymbolNo
	executionSymbolApproval
	executionSymbolValid
	executionSymbolStale
	executionSymbolConflicting
	executionSymbolApprove
	executionSymbolReject
	executionSymbolRevise
	executionSymbolEscalate
)

func executionTestProgram(t testing.TB, requirementCount int) *program.Program {
	t.Helper()
	p := evidenceSymbolTestProgram(
		"active", "inactive", "yes", "no", "approval", "valid", "stale", "conflicting",
		"approve", "reject", "revise", "escalate",
	)
	if err := policyindex.BuildSchema(&p.FieldIndex, []schema.ValueKind{
		schema.ValueKindSymbol,
		schema.ValueKindSymbol,
	}); err != nil {
		t.Fatalf("BuildSchema: %v", err)
	}
	p.FieldKinds = []schema.ValueKind{schema.ValueKindSymbol, schema.ValueKindSymbol}
	p.ValueKinds = []schema.ValueKind{schema.ValueKindSymbol, schema.ValueKindSymbol}
	p.ValueRefs = []uint32{uint32(executionSymbolActive), uint32(executionSymbolYes)}
	p.EvidenceKindNames = []schema.SymbolID{executionSymbolApproval}
	p.EvidenceStateNames = []schema.SymbolID{executionSymbolValid, executionSymbolStale, executionSymbolConflicting}

	applicability := appendExecutorInstruction(p, program.OpcodeEqual, 1, 1, nil, 0, 0)
	assertion := appendExecutorInstruction(p, program.OpcodeEqual, 2, 2, nil, 0, 0)
	evidence := appendExecutorInstruction(p, program.OpcodeEvidence, 0, 0, nil, 1, 1)
	p.RootFlags[applicability-1] = program.RootApplicability
	p.RootFlags[assertion-1] = program.RootAssertion
	p.RootFlags[evidence-1] = program.RootEvidence
	p.TruthSlots = []schema.SlotID{1, 2, 3}
	p.ReasonSlots = []schema.SlotID{1, 2, 3}
	p.TruthSlotCount = 3
	p.ReasonSlotCount = 3

	p.Outcomes = result.OutcomeTable{
		Names:      []schema.SymbolID{executionSymbolApprove, executionSymbolReject, executionSymbolRevise, executionSymbolEscalate},
		Precedence: []uint8{1, 4, 2, 3},
		Terminal:   []bool{true, true, false, true},
	}
	p.Remediations = result.RemediationTable{
		Kinds:         []result.RemediationKind{result.RemediationAddEvidence},
		Fields:        []schema.FieldID{0},
		Values:        []schema.ValueID{0},
		EvidenceKinds: []schema.EvidenceKindID{1},
	}

	p.RequirementIDs = make([]schema.RequirementID, requirementCount)
	p.RequirementRoots = make([]schema.InstructionID, requirementCount)
	p.RequirementClauseStarts = make([]uint32, requirementCount)
	p.RequirementClauseCounts = make([]uint16, requirementCount)
	p.RequirementClauseIDs = make([]schema.ClauseID, requirementCount)
	p.ClauseAssertionRoots = make([]schema.InstructionID, requirementCount)
	p.ClauseEvidenceStarts = make([]uint32, requirementCount)
	p.ClauseEvidenceCounts = make([]uint16, requirementCount)
	p.ClauseEvidenceIDs = make([]schema.InstructionID, requirementCount)
	p.ClauseOnSatisfied = make([]schema.OutcomeID, requirementCount)
	p.ClauseOnFalse = make([]schema.OutcomeID, requirementCount)
	p.ClauseRemediationStarts = make([]uint32, requirementCount)
	p.ClauseRemediationCounts = make([]uint16, requirementCount)
	p.ClauseRemediationIDs = []schema.RemediationID{1}
	for row := range requirementCount {
		id := row + 1
		p.RequirementIDs[row] = schema.RequirementID(id)
		p.RequirementRoots[row] = applicability
		p.RequirementClauseStarts[row] = uint32(row)
		p.RequirementClauseCounts[row] = 1
		p.RequirementClauseIDs[row] = schema.ClauseID(id)
		p.ClauseAssertionRoots[row] = assertion
		p.ClauseEvidenceStarts[row] = uint32(row)
		p.ClauseEvidenceCounts[row] = 1
		p.ClauseEvidenceIDs[row] = evidence
		p.ClauseOnSatisfied[row] = 1
		p.ClauseOnFalse[row] = 2
		p.ClauseRemediationCounts[row] = 1
	}

	setExecutionResolutionRows(t, p)
	constraints := policyindex.Constraints{
		Rows:        make([]uint32, requirementCount),
		Fields:      make([]schema.FieldID, requirementCount),
		ValueStarts: make([]uint32, requirementCount),
		ValueCounts: make([]uint32, requirementCount),
		Values:      make([]schema.SymbolID, requirementCount),
	}
	for row := range requirementCount {
		constraints.Rows[row] = uint32(row)
		constraints.Fields[row] = 1
		constraints.ValueStarts[row] = uint32(row)
		constraints.ValueCounts[row] = 1
		constraints.Values[row] = executionSymbolActive
	}
	var indexBuilder policyindex.PolicyBuilder
	if err := indexBuilder.Build(&p.ApplicabilityIndex, uint32(requirementCount), constraints); err != nil {
		t.Fatalf("Build applicability index: %v", err)
	}
	return p
}

func setExecutionResolutionRows(t testing.TB, p *program.Program) {
	t.Helper()
	rows := len(p.ClauseAssertionRoots) * truth.ReasonCount
	p.Resolutions = result.ResolutionTable{
		OutcomeIDs:        make([]schema.OutcomeID, rows),
		RemediationStarts: make([]uint32, rows),
		RemediationCounts: make([]uint16, rows),
		RemediationIDs:    p.ClauseRemediationIDs,
	}
	for clause := range len(p.ClauseAssertionRoots) {
		base := clause * truth.ReasonCount
		for reason := range truth.ReasonCount {
			p.Resolutions.OutcomeIDs[base+reason] = 4
		}
		p.Resolutions.OutcomeIDs[base+int(truth.ReasonMissing-1)] = 3
		p.Resolutions.RemediationCounts[base+int(truth.ReasonMissing-1)] = 1
	}
	if err := p.ValidateResultTables(); err != nil {
		t.Fatalf("ValidateResultTables: %v", err)
	}
}

func executionPathBatch(t testing.TB, p *program.Program) Batch {
	t.Helper()
	const rows = 7
	var builder Builder
	if err := builder.Begin(p, rows, 3, rows); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for row := uint32(0); row < rows; row++ {
		if err := builder.SetRequestID(row, schema.RequestID(row+1)); err != nil {
			t.Fatal(err)
		}
	}
	facts := []struct {
		app, assertion schema.SymbolID
	}{
		{executionSymbolInactive, executionSymbolYes},
		{executionSymbolActive, executionSymbolYes},
		{executionSymbolActive, executionSymbolNo},
		{executionSymbolActive, 0},
		{executionSymbolActive, executionSymbolYes},
		{executionSymbolActive, executionSymbolYes},
		{0, executionSymbolNo},
	}
	for row, fact := range facts {
		if fact.app != 0 {
			if err := builder.SetSymbol(uint32(row), 1, fact.app); err != nil {
				t.Fatal(err)
			}
		}
		if fact.assertion != 0 {
			if err := builder.SetSymbol(uint32(row), 2, fact.assertion); err != nil {
				t.Fatal(err)
			}
		}
	}
	records := []EvidenceRecord{
		evidenceRecord(1, 1, 1),
		evidenceRecord(2, 1, 2),
		evidenceRecord(3, 1, 3),
	}
	for row, record := range records {
		if err := builder.SetEvidence(uint32(row), record); err != nil {
			t.Fatal(err)
		}
	}
	if err := builder.SetEvidenceCSR(
		[]uint32{0, 1, 2, 3, 4, 5, 6, 7},
		[]uint32{0, 0, 0, 0, 1, 2, 0},
	); err != nil {
		t.Fatal(err)
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return batch
}

func TestExecutePaths(t *testing.T) {
	p := executionTestProgram(t, 1)
	batch := executionPathBatch(t, p)
	var executor Executor
	var got result.Batch
	if err := executor.Execute(&got, p, batch); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !slices.Equal(got.OutcomeIDs, []schema.OutcomeID{0, 1, 2, 3, 4, 4, 3}) {
		t.Fatalf("outcomes = %v", got.OutcomeIDs)
	}
	if !slices.Equal(got.RequirementOffsets, []uint32{0, 0, 1, 2, 3, 4, 5, 6}) ||
		!slices.Equal(got.RequirementIDs, []schema.RequirementID{1, 1, 1, 1, 1, 1}) {
		t.Fatalf("requirements = %v/%v", got.RequirementOffsets, got.RequirementIDs)
	}
	if !slices.Equal(got.DriverOffsets, []uint32{0, 0, 1, 2, 3, 4, 5, 6}) ||
		!slices.Equal(got.DriverRequirements, []schema.RequirementID{1, 1, 1, 1, 1, 1}) ||
		!slices.Equal(got.DriverClauses, []schema.ClauseID{1, 1, 1, 1, 1, 1}) ||
		!slices.Equal(got.DriverNodes, []schema.NodeID{2, 2, 2, 3, 3, 1}) ||
		!slices.Equal(got.DriverReasons, []schema.ReasonID{0, 0, truth.ReasonMissing, truth.ReasonStale, truth.ReasonConflict, truth.ReasonMissing}) {
		t.Fatalf("drivers = offsets %v requirements %v clauses %v nodes %v reasons %v",
			got.DriverOffsets, got.DriverRequirements, got.DriverClauses, got.DriverNodes, got.DriverReasons)
	}
	if !slices.Equal(got.ReasonOffsets, []uint32{0, 0, 0, 0, 1, 2, 3, 4}) ||
		!slices.Equal(got.ReasonIDs, []schema.ReasonID{truth.ReasonMissing, truth.ReasonStale, truth.ReasonConflict, truth.ReasonMissing}) {
		t.Fatalf("reasons = %v/%v", got.ReasonOffsets, got.ReasonIDs)
	}
	if !slices.Equal(got.RemediationOffsets, []uint32{0, 0, 0, 0, 1, 1, 1, 2}) ||
		!slices.Equal(got.RemediationIDs, []schema.RemediationID{1, 1}) {
		t.Fatalf("remediations = %v/%v", got.RemediationOffsets, got.RemediationIDs)
	}
	if !slices.Equal(got.EvidenceOffsets, make([]uint32, batch.Rows+1)) || len(got.EvidenceIDs) != 0 {
		t.Fatalf("evidence = %v/%v, want empty ranges", got.EvidenceOffsets, got.EvidenceIDs)
	}
}

func singleExecutionBatch(t testing.TB, p *program.Program) Batch {
	t.Helper()
	var builder Builder
	if err := builder.Begin(p, 1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetRequestID(0, 1); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetSymbol(0, 1, executionSymbolActive); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetSymbol(0, 2, executionSymbolYes); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetEvidence(0, evidenceRecord(1, 1, 1)); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetEvidenceCSR([]uint32{0, 1}, []uint32{0}); err != nil {
		t.Fatal(err)
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestExecuteOutcomePrecedence(t *testing.T) {
	tests := []struct {
		name       string
		satisfied  [2]schema.OutcomeID
		precedence [2]uint8
		want       schema.OutcomeID
		wantReq    schema.RequirementID
		wantClause schema.ClauseID
	}{
		{"higher precedence", [2]schema.OutcomeID{1, 2}, [2]uint8{1, 4}, 2, 2, 2},
		{"lower ID tie", [2]schema.OutcomeID{2, 1}, [2]uint8{4, 4}, 1, 2, 2},
		{"same outcome keeps first", [2]schema.OutcomeID{2, 2}, [2]uint8{1, 4}, 2, 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := executionTestProgram(t, 2)
			p.ClauseOnSatisfied[0], p.ClauseOnSatisfied[1] = tt.satisfied[0], tt.satisfied[1]
			p.Outcomes.Precedence[0], p.Outcomes.Precedence[1] = tt.precedence[0], tt.precedence[1]
			setExecutionResolutionRows(t, p)
			batch := singleExecutionBatch(t, p)
			var executor Executor
			var got result.Batch
			if err := executor.Execute(&got, p, batch); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got.OutcomeIDs[0] != tt.want || !slices.Equal(got.DriverRequirements, []schema.RequirementID{tt.wantReq}) ||
				!slices.Equal(got.DriverClauses, []schema.ClauseID{tt.wantClause}) {
				t.Fatalf("winner = outcome %d requirement %v clause %v, want %d/%d/%d",
					got.OutcomeIDs[0], got.DriverRequirements, got.DriverClauses, tt.want, tt.wantReq, tt.wantClause)
			}
		})
	}
}

func TestExecuteDriverSelection(t *testing.T) {
	p := executionTestProgram(t, 2)
	p.ClauseOnSatisfied[0], p.ClauseOnSatisfied[1] = 2, 2
	setExecutionResolutionRows(t, p)
	batch := singleExecutionBatch(t, p)
	var executor Executor
	var got result.Batch
	if err := executor.Execute(&got, p, batch); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.DriverRequirements, []schema.RequirementID{1}) ||
		!slices.Equal(got.DriverClauses, []schema.ClauseID{1}) ||
		!slices.Equal(got.DriverNodes, []schema.NodeID{2}) {
		t.Fatalf("driver = requirement %v clause %v node %v", got.DriverRequirements, got.DriverClauses, got.DriverNodes)
	}
}

func TestExecuteDefiniteOutcomeDropsDominatedReasons(t *testing.T) {
	p := executionTestProgram(t, 1)
	var builder Builder
	if err := builder.Begin(p, 1, 2, 2); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetRequestID(0, 1); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetSymbol(0, 1, executionSymbolActive); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetSymbol(0, 2, executionSymbolYes); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetEvidence(0, evidenceRecord(1, 1, 1)); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetEvidence(1, evidenceRecord(2, 1, 2)); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetEvidenceCSR([]uint32{0, 2}, []uint32{0, 1}); err != nil {
		t.Fatal(err)
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var executor Executor
	var got result.Batch
	if err := executor.Execute(&got, p, batch); err != nil {
		t.Fatal(err)
	}
	if got.OutcomeIDs[0] != 1 || len(got.ReasonIDs) != 0 || got.ReasonOffsets[1] != 0 {
		t.Fatalf("definite result = outcome %d reasons %v offsets %v, want outcome 1 without reasons",
			got.OutcomeIDs[0], got.ReasonIDs, got.ReasonOffsets)
	}
}

func executionRowsBatch(t testing.TB, p *program.Program, rows uint32) Batch {
	t.Helper()
	var builder Builder
	if err := builder.Begin(p, rows, 0, 0); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	for row := uint32(0); row < rows; row++ {
		if err := builder.SetRequestID(row, schema.RequestID(row+1)); err != nil {
			t.Fatal(err)
		}
		if err := builder.SetSymbol(row, 1, executionSymbolActive); err != nil {
			t.Fatal(err)
		}
		if err := builder.SetSymbol(row, 2, executionSymbolYes); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return batch
}

func assertExecutionRows(t testing.TB, got *result.Batch, rows uint32) {
	t.Helper()
	if got.Rows != rows || len(got.OutcomeIDs) != int(rows) ||
		len(got.RequirementOffsets) != int(rows)+1 || len(got.DriverOffsets) != int(rows)+1 ||
		len(got.EvidenceOffsets) != int(rows)+1 || len(got.ReasonOffsets) != int(rows)+1 ||
		len(got.RemediationOffsets) != int(rows)+1 {
		t.Fatalf("result shape = rows %d fixed %d offsets %d/%d/%d/%d/%d, want %d",
			got.Rows, len(got.OutcomeIDs), len(got.RequirementOffsets), len(got.DriverOffsets),
			len(got.EvidenceOffsets), len(got.ReasonOffsets), len(got.RemediationOffsets), rows)
	}
	if len(got.RequirementIDs) != int(rows) || len(got.DriverNodes) != int(rows) ||
		len(got.ReasonIDs) != int(rows) || len(got.RemediationIDs) != int(rows) || len(got.EvidenceIDs) != 0 {
		t.Fatalf("edge lengths = requirements %d drivers %d reasons %d remediations %d evidence %d, want %d/%d/%d/%d/0",
			len(got.RequirementIDs), len(got.DriverNodes), len(got.ReasonIDs),
			len(got.RemediationIDs), len(got.EvidenceIDs), rows, rows, rows, rows)
	}
	for row := uint32(0); row < rows; row++ {
		if got.OutcomeIDs[row] != 3 || got.RequirementOffsets[row+1] != row+1 ||
			got.DriverOffsets[row+1] != row+1 || got.EvidenceOffsets[row+1] != 0 ||
			got.ReasonOffsets[row+1] != row+1 || got.RemediationOffsets[row+1] != row+1 ||
			got.RequirementIDs[row] != 1 || got.DriverRequirements[row] != 1 ||
			got.DriverClauses[row] != 1 || got.DriverNodes[row] != 3 ||
			got.DriverReasons[row] != truth.ReasonMissing || got.ReasonIDs[row] != truth.ReasonMissing ||
			got.RemediationIDs[row] != 1 {
			t.Fatalf("row %d = outcome %d offsets %d/%d/%d/%d/%d requirement %d driver %d/%d/%d/%d reason %d remediation %d",
				row, got.OutcomeIDs[row], got.RequirementOffsets[row+1], got.DriverOffsets[row+1],
				got.EvidenceOffsets[row+1], got.ReasonOffsets[row+1], got.RemediationOffsets[row+1],
				got.RequirementIDs[row], got.DriverRequirements[row], got.DriverClauses[row],
				got.DriverNodes[row], got.DriverReasons[row], got.ReasonIDs[row], got.RemediationIDs[row])
		}
	}
}

func assertExecutorTailClear(t testing.TB, e *Executor, p *program.Program, rows uint32) {
	t.Helper()
	if rows == 0 || rows&63 == 0 {
		return
	}
	words := truth.WordCount(rows)
	tail := ^(uint64(1)<<(rows&63) - 1)
	for slot := uint32(0); slot < p.TruthSlotCount; slot++ {
		for plane := range 2 {
			word := (int(slot)*2+plane)*words + words - 1
			if e.truthWords[word]&tail != 0 {
				t.Fatalf("truth slot %d plane %d dirty tail = %#x", slot+1, plane, e.truthWords[word]&tail)
			}
		}
	}
	for slot := uint32(0); slot < p.ReasonSlotCount; slot++ {
		for reason := range truth.ReasonCount {
			word := (int(slot)*truth.ReasonCount+reason)*words + words - 1
			if e.reasonWords[word]&tail != 0 {
				t.Fatalf("reason slot %d plane %d dirty tail = %#x", slot+1, reason+1, e.reasonWords[word]&tail)
			}
		}
	}
}

func TestExecutorBoundaries(t *testing.T) {
	for _, rows := range []uint32{0, 1, 64, 65} {
		t.Run(string(rune('A'+rows%26)), func(t *testing.T) {
			p := executionTestProgram(t, 1)
			batch := executionRowsBatch(t, p, rows)
			var executor Executor
			var got result.Batch
			if err := executor.Execute(&got, p, batch); err != nil {
				t.Fatalf("Execute(%d): %v", rows, err)
			}
			assertExecutionRows(t, &got, rows)
			assertExecutorTailClear(t, &executor, p, rows)
		})
	}
}

func poisonCapacity[T any](dst []T, value T) {
	dst = dst[:cap(dst)]
	for i := range dst {
		dst[i] = value
	}
}

func poisonExecutor(e *Executor) {
	poisonCapacity(e.truthWords, uint64(math.MaxUint64))
	poisonCapacity(e.reasonWords, uint64(math.MaxUint64))
	poisonCapacity(e.candidateWords, uint64(math.MaxUint64))
	poisonCapacity(e.selectorValues, schema.SymbolID(math.MaxUint32))
	poisonCapacity(e.selectorPresent, uint8(math.MaxUint8))
}

func poisonResult(dst *result.Batch) {
	poisonCapacity(dst.OutcomeIDs, schema.OutcomeID(math.MaxUint32))
	poisonCapacity(dst.RequirementOffsets, uint32(math.MaxUint32))
	poisonCapacity(dst.RequirementIDs, schema.RequirementID(math.MaxUint32))
	poisonCapacity(dst.DriverOffsets, uint32(math.MaxUint32))
	poisonCapacity(dst.DriverRequirements, schema.RequirementID(math.MaxUint32))
	poisonCapacity(dst.DriverClauses, schema.ClauseID(math.MaxUint32))
	poisonCapacity(dst.DriverNodes, schema.NodeID(math.MaxUint32))
	poisonCapacity(dst.DriverReasons, schema.ReasonID(math.MaxUint8))
	poisonCapacity(dst.EvidenceOffsets, uint32(math.MaxUint32))
	poisonCapacity(dst.EvidenceIDs, schema.EvidenceID(math.MaxUint32))
	poisonCapacity(dst.ReasonOffsets, uint32(math.MaxUint32))
	poisonCapacity(dst.ReasonIDs, schema.ReasonID(math.MaxUint8))
	poisonCapacity(dst.RemediationOffsets, uint32(math.MaxUint32))
	poisonCapacity(dst.RemediationIDs, schema.RemediationID(math.MaxUint32))
}

func cloneResultBatch(src result.Batch) result.Batch {
	dst := src
	dst.OutcomeIDs = slices.Clone(src.OutcomeIDs)
	dst.RequirementOffsets = slices.Clone(src.RequirementOffsets)
	dst.RequirementIDs = slices.Clone(src.RequirementIDs)
	dst.DriverOffsets = slices.Clone(src.DriverOffsets)
	dst.DriverRequirements = slices.Clone(src.DriverRequirements)
	dst.DriverClauses = slices.Clone(src.DriverClauses)
	dst.DriverNodes = slices.Clone(src.DriverNodes)
	dst.DriverReasons = slices.Clone(src.DriverReasons)
	dst.EvidenceOffsets = slices.Clone(src.EvidenceOffsets)
	dst.EvidenceIDs = slices.Clone(src.EvidenceIDs)
	dst.ReasonOffsets = slices.Clone(src.ReasonOffsets)
	dst.ReasonIDs = slices.Clone(src.ReasonIDs)
	dst.RemediationOffsets = slices.Clone(src.RemediationOffsets)
	dst.RemediationIDs = slices.Clone(src.RemediationIDs)
	return dst
}

func TestExecutorReuse(t *testing.T) {
	programA := executionTestProgram(t, 1)
	largeA := executionRowsBatch(t, programA, 65)
	smallA := executionRowsBatch(t, programA, 1)
	programB := executionTestProgram(t, 2)
	batchB := executionRowsBatch(t, programB, 64)

	var executor Executor
	var got result.Batch
	if err := executor.Execute(&got, programA, largeA); err != nil {
		t.Fatal(err)
	}
	poisonExecutor(&executor)
	poisonResult(&got)
	if err := executor.Execute(&got, programA, smallA); err != nil {
		t.Fatal(err)
	}
	assertExecutionRows(t, &got, 1)
	assertExecutorTailClear(t, &executor, programA, 1)

	poisonExecutor(&executor)
	poisonResult(&got)
	if err := executor.Execute(&got, programA, largeA); err != nil {
		t.Fatal(err)
	}
	var freshExecutor Executor
	var want result.Batch
	if err := freshExecutor.Execute(&want, programA, largeA); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("large -> small -> large result differs from fresh execution")
	}

	if err := executor.Execute(&got, programB, batchB); err != nil {
		t.Fatal(err)
	}
	if err := executor.Execute(&got, programA, largeA); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("Program A -> B -> A result differs from fresh execution")
	}
}

func executeRecover(e *Executor, dst *result.Batch, p *program.Program, batch Batch) (err error, recovered any) {
	defer func() {
		recovered = recover()
	}()
	err = e.Execute(dst, p, batch)
	return err, nil
}

func assertExecutorRejectedAtomically(
	t *testing.T,
	executor *Executor,
	dst *result.Batch,
	bound *program.Program,
	p *program.Program,
	batch Batch,
) {
	t.Helper()
	want := cloneResultBatch(*dst)
	err, recovered := executeRecover(executor, dst, p, batch)
	if recovered != nil {
		t.Errorf("Execute panicked on malformed input: %v", recovered)
	} else if !errors.Is(err, ErrInvalidProgram) {
		t.Errorf("Execute error = %v, want %v", err, ErrInvalidProgram)
	}
	if !reflect.DeepEqual(*dst, want) {
		t.Error("rejected Execute changed destination")
	}
	if executor.program != bound || executor.states.program != bound {
		t.Errorf("rejected Execute changed bindings to program %p/states %p, want %p", executor.program, executor.states.program, bound)
	}
}

func TestExecutorRejectsAtomically(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*program.Program, *Batch)
	}{
		{
			name: "program",
			mutate: func(p *program.Program, _ *Batch) {
				p.Opcodes = nil
			},
		},
		{
			name: "selector columns",
			mutate: func(_ *program.Program, batch *Batch) {
				batch.SymbolValues = batch.SymbolValues[:len(batch.SymbolValues)-1]
			},
		},
		{
			name: "evidence CSR",
			mutate: func(_ *program.Program, batch *Batch) {
				batch.EvidenceOffsets = batch.EvidenceOffsets[:len(batch.EvidenceOffsets)-1]
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bound := executionTestProgram(t, 1)
			boundBatch := executionRowsBatch(t, bound, 1)
			candidateBase := executionTestProgram(t, 2)
			candidate := *candidateBase
			candidateBatch := executionRowsBatch(t, candidateBase, 1)
			test.mutate(&candidate, &candidateBatch)

			var executor Executor
			var dst result.Batch
			if err := executor.Execute(&dst, bound, boundBatch); err != nil {
				t.Fatal(err)
			}
			assertExecutorRejectedAtomically(t, &executor, &dst, bound, &candidate, candidateBatch)
		})
	}
}

func TestExecutorAllocations(t *testing.T) {
	p := executionTestProgram(t, 2)
	batch := executionRowsBatch(t, p, 65)
	var executor Executor
	var dst result.Batch
	if err := executor.Execute(&dst, p, batch); err != nil {
		t.Fatal(err)
	}
	var runErr error
	allocs := testing.AllocsPerRun(1000, func() {
		runErr = executor.Execute(&dst, p, batch)
	})
	if runErr != nil {
		t.Fatalf("warm Execute: %v", runErr)
	}
	if allocs != 0 {
		t.Fatalf("warm Execute allocations = %g, want 0", allocs)
	}
}

func TestExecuteTerminalResolutionDropsRemediation(t *testing.T) {
	t.Run("active clause", func(t *testing.T) {
		p := executionTestProgram(t, 1)
		row := int(truth.ReasonStale - 1)
		p.Resolutions.OutcomeIDs[row] = 4
		p.Resolutions.RemediationStarts[row] = 0
		p.Resolutions.RemediationCounts[row] = 1
		if err := p.ValidateResultTables(); err != nil {
			t.Fatal(err)
		}

		var builder Builder
		if err := builder.Begin(p, 1, 1, 1); err != nil {
			t.Fatal(err)
		}
		if err := builder.SetRequestID(0, 1); err != nil {
			t.Fatal(err)
		}
		if err := builder.SetSymbol(0, 1, executionSymbolActive); err != nil {
			t.Fatal(err)
		}
		if err := builder.SetSymbol(0, 2, executionSymbolYes); err != nil {
			t.Fatal(err)
		}
		if err := builder.SetEvidence(0, evidenceRecord(1, 1, 2)); err != nil {
			t.Fatal(err)
		}
		if err := builder.SetEvidenceCSR([]uint32{0, 1}, []uint32{0}); err != nil {
			t.Fatal(err)
		}
		batch, err := builder.Finish()
		if err != nil {
			t.Fatal(err)
		}

		var executor Executor
		var got result.Batch
		if err := executor.Execute(&got, p, batch); err != nil {
			t.Fatal(err)
		}
		if got.OutcomeIDs[0] != 4 || len(got.RemediationIDs) != 0 || got.RemediationOffsets[1] != 0 {
			t.Fatalf("terminal clause resolution = outcome %d remediations %v offsets %v, want outcome 4 without remediation",
				got.OutcomeIDs[0], got.RemediationIDs, got.RemediationOffsets)
		}
	})

	t.Run("unresolved applicability", func(t *testing.T) {
		p := executionTestProgram(t, 1)
		row := int(truth.ReasonMissing - 1)
		p.Resolutions.OutcomeIDs[row] = 4
		p.Resolutions.RemediationStarts[row] = 0
		p.Resolutions.RemediationCounts[row] = 1
		if err := p.ValidateResultTables(); err != nil {
			t.Fatal(err)
		}

		var builder Builder
		if err := builder.Begin(p, 1, 0, 0); err != nil {
			t.Fatal(err)
		}
		if err := builder.SetRequestID(0, 1); err != nil {
			t.Fatal(err)
		}
		if err := builder.SetSymbol(0, 2, executionSymbolYes); err != nil {
			t.Fatal(err)
		}
		batch, err := builder.Finish()
		if err != nil {
			t.Fatal(err)
		}

		var executor Executor
		var got result.Batch
		if err := executor.Execute(&got, p, batch); err != nil {
			t.Fatal(err)
		}
		if got.OutcomeIDs[0] != 4 || len(got.RemediationIDs) != 0 || got.RemediationOffsets[1] != 0 {
			t.Fatalf("terminal applicability resolution = outcome %d remediations %v offsets %v, want outcome 4 without remediation",
				got.OutcomeIDs[0], got.RemediationIDs, got.RemediationOffsets)
		}
	})
}

func executionIntegerProgram(t testing.TB) *program.Program {
	t.Helper()
	p := executionTestProgram(t, 1)
	if err := policyindex.BuildSchema(&p.FieldIndex, []schema.ValueKind{
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
	}); err != nil {
		t.Fatal(err)
	}
	p.FieldKinds = []schema.ValueKind{schema.ValueKindSymbol, schema.ValueKindInteger}
	p.ValueKinds = []schema.ValueKind{schema.ValueKindSymbol, schema.ValueKindInteger}
	p.ValueRefs = []uint32{uint32(executionSymbolActive), 1}
	p.IntegerValues = []int64{7}
	return p
}

func executionIntegerBatch(t testing.TB, p *program.Program) Batch {
	t.Helper()
	var builder Builder
	if err := builder.Begin(p, 1, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetRequestID(0, 1); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetSymbol(0, 1, executionSymbolActive); err != nil {
		t.Fatal(err)
	}
	if err := builder.SetInteger(0, 2, 7); err != nil {
		t.Fatal(err)
	}
	batch, err := builder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return batch
}

func TestExecutorRejectsMalformedTypedColumnsAtomically(t *testing.T) {
	p := executionIntegerProgram(t)
	batch := executionIntegerBatch(t, p)
	var executor Executor
	var dst result.Batch
	if err := executor.Execute(&dst, p, batch); err != nil {
		t.Fatal(err)
	}
	bad := batch
	bad.IntegerValues = bad.IntegerValues[:0]
	assertExecutorRejectedAtomically(t, &executor, &dst, p, p, bad)
}

func TestExecutorSameProgramBindIsNoOp(t *testing.T) {
	p := executionTestProgram(t, 1)
	batch := executionRowsBatch(t, p, 1)
	var executor Executor
	var dst result.Batch
	if err := executor.Execute(&dst, p, batch); err != nil {
		t.Fatal(err)
	}
	p.InstructionSourceStarts[0] = p.InstructionSourceEnds[0] + 1
	if err := executor.Execute(&dst, p, batch); err != nil {
		t.Fatalf("same immutable Program Execute rescanned static columns: %v", err)
	}
}
