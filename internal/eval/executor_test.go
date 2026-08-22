package eval

import (
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
