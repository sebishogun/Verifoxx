package compile

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
)

func TestLowerInstructionUint16Boundaries(t *testing.T) {
	children := make([]schema.NodeID, math.MaxUint16)
	for i := range children {
		children[i] = 1
	}
	doc := &ast.Document{
		NodeKinds:        []ast.NodeKind{ast.NodeKindCompare, ast.NodeKindAll},
		NodeRefs:         []uint32{0, 0},
		GroupChildStarts: []uint32{0},
		GroupChildCounts: []uint16{math.MaxUint16},
		ChildNodeIDs:     children,
	}
	var lowerer Lowerer
	lowerer.nodeCanon = []schema.InstructionID{1, 0}
	start, count, err := lowerer.appendGroupOperands(doc, 2, ast.NodeKindAll)
	if err != nil {
		t.Fatalf("accepted group boundary: %v", err)
	}
	if start != 0 || count != math.MaxUint16 || len(lowerer.candidateOperands) != math.MaxUint16 {
		t.Fatalf("group range = (%d,%d), edges %d", start, count, len(lowerer.candidateOperands))
	}

	mapDoc := &ast.Document{NodeKinds: []ast.NodeKind{ast.NodeKindAll}}
	var sourceMap Lowerer
	sourceMap.nodeCanon = []schema.InstructionID{1}
	sourceMap.nodeFlatStart = []uint32{0}
	sourceMap.nodeFlatCount = []uint16{math.MaxUint16}
	sourceMap.candidateToFinal = []schema.InstructionID{0, 1}
	sourceMap.candidateOperands = make([]schema.InstructionID, math.MaxUint16)
	for i := range sourceMap.candidateOperands {
		sourceMap.candidateOperands[i] = 2
	}
	var mapped program.Program
	mapped.Opcodes = []program.Opcode{program.OpcodeAll}
	if err := sourceMap.buildNodeInstructionMap(&mapped, mapDoc); err != nil {
		t.Fatalf("accepted source-map boundary: %v", err)
	}
	if len(mapped.NodeInstructionCounts) != 1 || mapped.NodeInstructionCounts[0] != math.MaxUint16 ||
		len(mapped.NodeInstructionIDs) != math.MaxUint16 || mapped.NodeInstructionIDs[0] != 1 ||
		mapped.NodeInstructionIDs[len(mapped.NodeInstructionIDs)-1] != 1 {
		t.Fatalf("source-map boundary = counts %v, edges %d", mapped.NodeInstructionCounts, len(mapped.NodeInstructionIDs))
	}

	overflowDoc := &ast.Document{
		NodeKinds:        []ast.NodeKind{ast.NodeKindCompare, ast.NodeKindAll, ast.NodeKindAll},
		NodeRefs:         []uint32{0, 0, 0},
		GroupChildStarts: []uint32{0},
		GroupChildCounts: []uint16{2},
		ChildNodeIDs:     []schema.NodeID{2, 1},
	}
	var overflow Lowerer
	overflow.nodeCanon = []schema.InstructionID{1, 2, 0}
	overflow.nodeFlatStart = []uint32{0, 0, 0}
	overflow.nodeFlatCount = []uint16{1, math.MaxUint16, 0}
	overflow.candidateOperands = make([]schema.InstructionID, math.MaxUint16)
	for i := range overflow.candidateOperands {
		overflow.candidateOperands[i] = 1
	}
	if _, _, err := overflow.appendGroupOperands(overflowDoc, 3, ast.NodeKindAll); !errors.Is(err, ErrProgramTooLarge) {
		t.Fatalf("flattened 65,536 operands error = %v, want %v", err, ErrProgramTooLarge)
	}
}

func TestLowerIntegrationNormalizationOwnershipAndResolution(t *testing.T) {
	fx := buildNormalizeFixture(t)
	doc := fx.doc
	doc.RemediationKinds = append(doc.RemediationKinds, ast.RemediationKindSetField)
	doc.RemediationFields = append(doc.RemediationFields, doc.CompareFields[0])
	doc.RemediationValues = append(doc.RemediationValues, doc.CompareValues[0])
	doc.RemediationEvidenceKinds = append(doc.RemediationEvidenceKinds, 0)
	doc.RemediationSourceStarts = append(doc.RemediationSourceStarts, 0)
	doc.RemediationSourceEnds = append(doc.RemediationSourceEnds, uint32(len(doc.InputBytes)))
	doc.ClauseRemediationIDs = append(doc.ClauseRemediationIDs, 1)
	doc.ClauseRemediationStarts[0] = 0
	doc.ClauseRemediationCounts[0] = 1
	if diagnostics := Validate(nil, doc, fx.fields); len(diagnostics) != 0 {
		t.Fatalf("integration fixture diagnostics: %+v", diagnostics)
	}

	got, err := Lower(doc, fx.fields, fx.syms)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	assertProgramSlots(t, got)
	assertProgramIndexes(t, got)
	if duplicate := requireSingleInstruction(t, got, fx.aDuplicate); duplicate != requireSingleInstruction(t, got, fx.a) {
		t.Fatalf("duplicate compare maps to %d", duplicate)
	}
	inner := nodeInstructionIDs(t, got, fx.deadInnerAll)
	if len(inner) != 2 {
		t.Fatalf("flattened inner group source map = %v", inner)
	}
	outer := requireSingleInstruction(t, got, fx.deadOuterAll)
	if operands := instructionOperands(t, got, outer); len(operands) != 3 {
		t.Fatalf("flattened outer operands = %v", operands)
	}
	covered := 0
	for _, count := range got.OpcodeRunCounts {
		covered += int(count)
	}
	if len(got.OpcodeRunOpcodes) == 0 || covered != got.InstructionCount() {
		t.Fatalf("opcode runs cover %d/%d instructions", covered, got.InstructionCount())
	}
	if !reflect.DeepEqual(got.Resolutions.RemediationIDs, []schema.RemediationID{1}) {
		t.Fatalf("resolution remediation edges = %v", got.Resolutions.RemediationIDs)
	}
	for row, count := range got.Resolutions.RemediationCounts {
		if count != 0 {
			t.Fatalf("terminal resolution row %d exposes %d remediations", row, count)
		}
	}
	assertExactProgramSlices(t, reflect.ValueOf(got).Elem(), "Program")
	want := snapshotExported(reflect.ValueOf(*got))
	zeroDocumentSlices(doc)
	if after := snapshotExported(reflect.ValueOf(*got)); !reflect.DeepEqual(after, want) {
		t.Fatal("source mutation changed integrated Program output")
	}
}
