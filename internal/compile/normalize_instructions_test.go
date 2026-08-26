package compile

import (
	"testing"

	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
)

func TestCompareOpcodeMapsDefined(t *testing.T) {
	opcode, ok := compareOpcode(ast.CompareOpDefined)
	if !ok || opcode != program.OpcodeDefined {
		t.Fatalf("compareOpcode(Defined) = (%v,%v), want (%v,true)", opcode, ok, program.OpcodeDefined)
	}
}

type normalizeFixture struct {
	doc    *ast.Document
	fields *schema.Schema
	syms   *schema.Interner

	a, b, c                    schema.NodeID
	aDuplicate                 schema.NodeID
	inAB, inABDuplicate, inBA  schema.NodeID
	innerAll, outerAll         schema.NodeID
	innerAny, outerAny         schema.NodeID
	duplicateInner, outerDup   schema.NodeID
	reorderedAll               schema.NodeID
	deadInnerAll, deadOuterAll schema.NodeID
}

func buildNormalizeFixture(t *testing.T) normalizeFixture {
	t.Helper()
	syms := schema.NewSymbolInterner(1)
	name, err := syms.Intern([]byte("fact"))
	if err != nil {
		t.Fatal(err)
	}
	builder := schema.NewBuilder()
	field, err := builder.AddField(name, schema.ValueKindSymbol, schema.FieldGroupContext)
	if err != nil {
		t.Fatal(err)
	}
	fields := builder.Finish()

	ab := ast.NewBuilder(ast.Hints{
		Nodes: 16, CompareNodes: 7, CompareListValues: 6, GroupNodes: 9,
		ChildEdges: 23, Values: 8, SymbolValues: 8, SymbolBytes: 64,
		Outcomes: 1, Clauses: 7, Requirements: 5, RequirementClauseEdges: 7,
		SourceBytes: 2,
	})
	if err := ab.SetSource([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	explanations := installValidExplanations(t, ab)
	span := ast.SourceSpan{Start: 0, End: 2}
	addSymbol := func(value string) schema.ValueID {
		id, err := ab.AddSymbolValue([]byte(value))
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	valueA := addSymbol("a")
	valueB := addSymbol("b")
	valueC := addSymbol("c")
	policyName := addSymbol("normalize")
	policyVersion := addSymbol("1")
	outcomeName := addSymbol("approve")
	if err := ab.SetMetadata(policyName, policyVersion); err != nil {
		t.Fatal(err)
	}
	outcome, err := ab.AddOutcome(outcomeName, 1, true, span)
	if err != nil {
		t.Fatal(err)
	}
	resolution := explanations.resolution
	resolution.OnSatisfied = outcome
	resolution.OnFalse = outcome
	resolution.OnMissing = outcome
	resolution.OnStale = outcome
	resolution.OnUnclear = outcome
	resolution.OnUnverifiable = outcome
	resolution.OnConflict = outcome

	addCompare := func(value schema.ValueID) schema.NodeID {
		node, err := ab.AddCompare(field, ast.CompareOpEqual, value, span)
		if err != nil {
			t.Fatal(err)
		}
		return node
	}
	addGroup := func(kind ast.NodeKind, children ...schema.NodeID) schema.NodeID {
		node, err := ab.AddGroup(kind, children, span)
		if err != nil {
			t.Fatal(err)
		}
		return node
	}
	addIn := func(values ...schema.ValueID) schema.NodeID {
		node, err := ab.AddIn(field, values, span)
		if err != nil {
			t.Fatal(err)
		}
		return node
	}

	fx := normalizeFixture{fields: fields, syms: syms}
	fx.a = addCompare(valueA)
	fx.b = addCompare(valueB)
	fx.c = addCompare(valueC)
	fx.aDuplicate = addCompare(valueA)
	fx.inAB = addIn(valueA, valueB)
	fx.inABDuplicate = addIn(valueA, valueB)
	fx.inBA = addIn(valueB, valueA)
	fx.innerAll = addGroup(ast.NodeKindAll, fx.a, fx.b)
	fx.outerAll = addGroup(ast.NodeKindAll, fx.innerAll, fx.c)
	fx.innerAny = addGroup(ast.NodeKindAny, fx.a, fx.b)
	fx.outerAny = addGroup(ast.NodeKindAny, fx.innerAny, fx.c)
	fx.duplicateInner = addGroup(ast.NodeKindAll, fx.a, fx.b)
	fx.outerDup = addGroup(ast.NodeKindAll, fx.duplicateInner, fx.c)
	fx.reorderedAll = addGroup(ast.NodeKindAll, fx.b, fx.a, fx.c)
	fx.deadInnerAll = addGroup(ast.NodeKindAll, fx.b, fx.c)
	fx.deadOuterAll = addGroup(ast.NodeKindAll, fx.deadInnerAll, fx.a)

	addClause := func(root schema.NodeID) schema.ClauseID {
		id, err := ab.AddClause(root, nil, resolution, nil, span)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	clauseA := addClause(fx.aDuplicate)
	clauseInner := addClause(fx.innerAll)
	clauseAny := addClause(fx.outerAny)
	clauseDup := addClause(fx.outerDup)
	clauseIn := addClause(fx.inABDuplicate)
	clauseReordered := addClause(fx.reorderedAll)
	clauseDead := addClause(fx.deadOuterAll)

	addRequirement := func(id schema.RequirementID, root schema.NodeID, clauses ...schema.ClauseID) {
		if err := ab.AddRequirement(id, root, clauses, span); err != nil {
			t.Fatal(err)
		}
	}
	addRequirement(1, fx.outerAll, clauseInner, clauseAny, clauseDup)
	addRequirement(2, fx.a, clauseA)
	addRequirement(3, fx.inAB, clauseIn)
	addRequirement(4, fx.inBA, clauseReordered)
	addRequirement(5, fx.deadOuterAll, clauseDead)

	fx.doc = ab.Document()
	if diagnostics := Validate(nil, fx.doc, fields); len(diagnostics) != 0 {
		t.Fatalf("normalize fixture produced %d diagnostics: %+v", len(diagnostics), diagnostics)
	}
	return fx
}

func nodeInstructionIDs(t *testing.T, p *program.Program, node schema.NodeID) []schema.InstructionID {
	t.Helper()
	index := int(node) - 1
	if index < 0 || index >= len(p.NodeInstructionStarts) || index >= len(p.NodeInstructionCounts) {
		t.Fatalf("node %d source-map row missing", node)
	}
	start := uint64(p.NodeInstructionStarts[index])
	count := uint64(p.NodeInstructionCounts[index])
	if start+count > uint64(len(p.NodeInstructionIDs)) {
		t.Fatalf("node %d source-map range (%d,%d) exceeds %d", node, start, count, len(p.NodeInstructionIDs))
	}
	return p.NodeInstructionIDs[int(start):int(start+count)]
}

func requireSingleInstruction(t *testing.T, p *program.Program, node schema.NodeID) schema.InstructionID {
	t.Helper()
	ids := nodeInstructionIDs(t, p, node)
	if len(ids) != 1 {
		t.Fatalf("node %d maps to %d instructions, want 1: %v", node, len(ids), ids)
	}
	return ids[0]
}

func instructionOperands(t *testing.T, p *program.Program, id schema.InstructionID) []schema.InstructionID {
	t.Helper()
	index := int(id) - 1
	if index < 0 || index >= len(p.OperandStarts) || index >= len(p.OperandCounts) {
		t.Fatalf("instruction %d operand row missing", id)
	}
	start := uint64(p.OperandStarts[index])
	count := uint64(p.OperandCounts[index])
	if start+count > uint64(len(p.Operands)) {
		t.Fatalf("instruction %d operand range (%d,%d) exceeds %d", id, start, count, len(p.Operands))
	}
	return p.Operands[int(start):int(start+count)]
}

func TestNormalizeFlattenCSEAndSourceMaps(t *testing.T) {
	fx := buildNormalizeFixture(t)
	var lowerer Lowerer
	var got program.Program
	if err := lowerer.lowerConstants(&got, fx.doc, fx.fields, fx.syms); err != nil {
		t.Fatalf("lowerConstants: %v", err)
	}
	if err := lowerer.lowerInstructions(&got, fx.doc); err != nil {
		t.Fatalf("lowerInstructions: %v", err)
	}

	if got.InstructionCount() != 10 {
		t.Fatalf("live instructions = %d, want 10", got.InstructionCount())
	}
	if len(got.Operands) != 14 {
		t.Fatalf("live operand edges = %d, want 14", len(got.Operands))
	}
	if len(got.ListValues) != 4 {
		t.Fatalf("live list values = %d, want 4", len(got.ListValues))
	}

	aID := requireSingleInstruction(t, &got, fx.a)
	bID := requireSingleInstruction(t, &got, fx.b)
	cID := requireSingleInstruction(t, &got, fx.c)
	if duplicate := requireSingleInstruction(t, &got, fx.aDuplicate); duplicate != aID {
		t.Fatalf("duplicate compare maps to %d, want %d", duplicate, aID)
	}
	aRow := int(aID) - 1
	if got.InstructionNodes[aRow] != fx.a {
		t.Fatalf("duplicate compare owner = node %d, want first node %d", got.InstructionNodes[aRow], fx.a)
	}
	if got.RootFlags[aRow] != program.RootApplicability|program.RootAssertion {
		t.Fatalf("merged compare flags = %v, want applicability|assertion", got.RootFlags[aRow])
	}

	inAB := requireSingleInstruction(t, &got, fx.inAB)
	if duplicate := requireSingleInstruction(t, &got, fx.inABDuplicate); duplicate != inAB {
		t.Fatalf("duplicate In maps to %d, want %d", duplicate, inAB)
	}
	if inBA := requireSingleInstruction(t, &got, fx.inBA); inBA == inAB {
		t.Fatalf("ordered In lists aliased to instruction %d", inAB)
	}

	innerAll := requireSingleInstruction(t, &got, fx.innerAll)
	if got.Opcodes[innerAll-1] != program.OpcodeAll {
		t.Fatalf("retained root group opcode = %v, want All", got.Opcodes[innerAll-1])
	}
	if operands := instructionOperands(t, &got, innerAll); len(operands) != 2 || operands[0] != aID || operands[1] != bID {
		t.Fatalf("retained inner All operands = %v, want [%d %d]", operands, aID, bID)
	}

	outerAll := requireSingleInstruction(t, &got, fx.outerAll)
	if duplicate := requireSingleInstruction(t, &got, fx.outerDup); duplicate != outerAll {
		t.Fatalf("duplicate flattened group maps to %d, want %d", duplicate, outerAll)
	}
	if operands := instructionOperands(t, &got, outerAll); len(operands) != 3 || operands[0] != aID || operands[1] != bID || operands[2] != cID {
		t.Fatalf("flattened outer All operands = %v, want [%d %d %d]", operands, aID, bID, cID)
	}
	outerRow := int(outerAll) - 1
	if got.RootFlags[outerRow] != program.RootApplicability|program.RootAssertion {
		t.Fatalf("merged group flags = %v, want applicability|assertion", got.RootFlags[outerRow])
	}

	outerAny := requireSingleInstruction(t, &got, fx.outerAny)
	if operands := instructionOperands(t, &got, outerAny); len(operands) != 3 || operands[0] != aID || operands[1] != bID || operands[2] != cID {
		t.Fatalf("flattened outer Any operands = %v, want [%d %d %d]", operands, aID, bID, cID)
	}
	if ids := nodeInstructionIDs(t, &got, fx.innerAny); len(ids) != 2 || ids[0] != aID || ids[1] != bID {
		t.Fatalf("dead inner Any source map = %v, want [%d %d]", ids, aID, bID)
	}

	if duplicateInner := requireSingleInstruction(t, &got, fx.duplicateInner); duplicateInner != innerAll {
		t.Fatalf("duplicate inner group maps to %d, want retained %d", duplicateInner, innerAll)
	}
	if reordered := requireSingleInstruction(t, &got, fx.reorderedAll); reordered == outerAll {
		t.Fatalf("operand-order variant aliased to instruction %d", outerAll)
	}

	deadOuter := requireSingleInstruction(t, &got, fx.deadOuterAll)
	if operands := instructionOperands(t, &got, deadOuter); len(operands) != 3 || operands[0] != bID || operands[1] != cID || operands[2] != aID {
		t.Fatalf("flattened dead-parent All operands = %v, want [%d %d %d]", operands, bID, cID, aID)
	}
	if ids := nodeInstructionIDs(t, &got, fx.deadInnerAll); len(ids) != 2 || ids[0] != bID || ids[1] != cID {
		t.Fatalf("dead inner All source map = %v, want [%d %d]", ids, bID, cID)
	}

	for _, dead := range []schema.NodeID{fx.innerAny, fx.deadInnerAll} {
		for _, owner := range got.InstructionNodes {
			if owner == dead {
				t.Fatalf("dead flattened group node %d retained an instruction", dead)
			}
		}
	}
	if len(got.NodeInstructionStarts) != fx.doc.Len() || len(got.NodeInstructionCounts) != fx.doc.Len() {
		t.Fatalf("source-map rows = %d/%d, want %d", len(got.NodeInstructionStarts), len(got.NodeInstructionCounts), fx.doc.Len())
	}
	if len(got.NodeInstructionIDs) != fx.doc.Len()+2 {
		t.Fatalf("source-map edges = %d, want %d", len(got.NodeInstructionIDs), fx.doc.Len()+2)
	}
}
