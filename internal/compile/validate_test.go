package compile

import (
	"os"
	"reflect"
	"testing"

	"github.com/sebishogun/verifoxx/internal/adapters/jsonpolicy"
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func TestValidateNilInputsAppendOnce(t *testing.T) {
	seed := Diagnostic{Code: CodeColumnLength, Node: schema.NodeID(1)}
	fields := schema.NewBuilder().Finish()
	tests := []struct {
		name   string
		doc    *ast.Document
		fields *schema.Schema
	}{
		{"nil doc", nil, fields},
		{"nil fields", &ast.Document{}, nil},
		{"nil both", nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dst := []Diagnostic{seed}
			got := Validate(dst, tc.doc, tc.fields)
			if len(got) != len(dst)+1 {
				t.Fatalf("len = %d, want %d (exactly one append)", len(got), len(dst)+1)
			}
			if got[0] != seed {
				t.Fatalf("seed prefix lost: got[0] = %+v, want %+v", got[0], seed)
			}
			if got[1].Code != CodeInvalidDocument || got[1].Table != TableDocument || got[1].Row != 0 {
				t.Fatalf("appended = %+v, want invalid_document on TableDocument row 0", got[1])
			}
		})
	}
}

func TestValidateNilAppendReusesCapacity(t *testing.T) {
	seed := Diagnostic{Code: CodeUnreachableNode}
	dst := make([]Diagnostic, 1, 4)
	dst[0] = seed
	got := Validate(dst, nil, nil)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if &got[0] != &dst[0] {
		t.Fatal("Validate reallocated despite sufficient capacity")
	}
}

func TestValidateMethodNilAppendOnce(t *testing.T) {
	var v Validator
	seed := Diagnostic{Code: CodeInvalidField, Field: schema.FieldID(3)}
	fields := schema.NewBuilder().Finish()
	dst := []Diagnostic{seed}
	got := v.Validate(dst, nil, fields)
	if len(got) != 2 || got[0] != seed ||
		got[1].Code != CodeInvalidDocument || got[1].Table != TableDocument || got[1].Row != 0 {
		t.Fatalf("method nil append = %+v", got)
	}
}

func TestValidateNonNilEmptyUnchanged(t *testing.T) {
	var doc ast.Document
	fields := schema.NewBuilder().Finish()
	seed := []Diagnostic{{Code: CodeCycle}}
	got := Validate(seed, &doc, fields)
	if len(got) != 1 || got[0].Code != CodeCycle {
		t.Fatalf("package call changed dst: %+v", got)
	}
	var v Validator
	got = v.Validate(seed, &doc, fields)
	if len(got) != 1 || got[0].Code != CodeCycle {
		t.Fatalf("method call changed dst: %+v", got)
	}
}

// fixture decodes the canonical valid-full.json policy through jsonpolicy with
// a locally built field schema and interner, returning the document and the
// frozen schema used to decode it.
func fixture(t *testing.T) (*ast.Document, *schema.Schema) {
	t.Helper()
	syms := schema.NewSymbolInterner(16)
	b := schema.NewBuilder()
	add := func(name string, kind schema.ValueKind, group schema.FieldGroup) {
		id, err := syms.Intern([]byte(name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.AddField(id, kind, group); err != nil {
			t.Fatal(err)
		}
	}
	add("subject.trust", schema.ValueKindSymbol, schema.FieldGroupSubject)
	add("context.environment", schema.ValueKindPresence, schema.FieldGroupContext)
	add("context.usage", schema.ValueKindSymbol, schema.FieldGroupContext)

	src, err := os.ReadFile("../../testdata/policies/valid-full.json")
	if err != nil {
		t.Fatal(err)
	}
	fields := b.Finish()
	ab := ast.NewBuilder(ast.Hints{
		Nodes: 8, CompareNodes: 4, CompareListValues: 4, GroupNodes: 2,
		ChildEdges: 4, NotNodes: 1, EvidenceNodes: 2,
		Values: 16, SymbolValues: 16, SymbolBytes: 512,
		EvidenceKinds: 4, EvidenceStates: 8, Outcomes: 8,
		Remediations: 4, Clauses: 2, ClauseEvidenceEdges: 2,
		ClauseRemediationEdges: 4, Requirements: 2, RequirementClauseEdges: 2,
		SourceBytes: len(src),
	})
	if err := jsonpolicy.Decode(ab, src, fields, syms, jsonpolicy.Limits{}); err != nil {
		t.Fatalf("decode valid-full.json: %v", err)
	}
	return ab.Document(), fields
}

func TestZeroValueValidatorAcceptsCanonical(t *testing.T) {
	doc, fields := fixture(t)
	var v Validator
	got := v.Validate(nil, doc, fields)
	if len(got) != 0 {
		t.Fatalf("canonical document produced %d diagnostics: %+v", len(got), got)
	}
	seeded := []Diagnostic{{Code: CodeCycle}}
	got = v.Validate(seeded, doc, fields)
	if len(got) != 1 || got[0].Code != CodeCycle {
		t.Fatalf("canonical document changed seeded dst: %+v", got)
	}
	if got := Validate(nil, doc, fields); len(got) != 0 {
		t.Fatalf("package Validate on canonical produced %d diagnostics: %+v", len(got), got)
	}
}

func TestValidateDoesNotModifyInputs(t *testing.T) {
	doc, fields := fixture(t)
	wantDoc, wantFields := fixture(t)

	var v Validator
	v.Validate(nil, doc, fields)

	if !reflect.DeepEqual(*doc, *wantDoc) {
		t.Fatal("Validate mutated ast.Document")
	}
	if !reflect.DeepEqual(*fields, *wantFields) {
		t.Fatal("Validate mutated schema.Schema")
	}
}

// TestValidateStructuralPrefix proves the public Validate path returns the
// phase-1 structural diagnostics as an exact prefix for a structurally corrupt
// document. Task 7.3/7.4 semantic and graph phases append after the structural
// block, so pubGot may be longer than structGot but must never reorder, drop,
// or rewrite the structural diagnostics.
func TestValidateStructuralPrefix(t *testing.T) {
	doc, fields := buildMinimal(t)
	doc.NodeKinds[1] = ast.NodeKindInvalid
	doc.ValueKinds[0] = schema.ValueKindPresence
	doc.ClauseAssertionRoots[0] = 0
	doc.RequirementClauseStarts[0] = 2

	var pub Validator
	pubGot := pub.Validate(nil, doc, fields)
	var phase1 Validator
	structGot := phase1.validateStructure(nil, doc, fields)
	if len(structGot) == 0 {
		t.Fatal("structurally corrupt document produced no diagnostics")
	}
	if len(pubGot) < len(structGot) {
		t.Fatalf("Validate returned %d diagnostics, fewer than the %d structural diagnostics", len(pubGot), len(structGot))
	}
	if !reflect.DeepEqual(pubGot[:len(structGot)], structGot) {
		t.Fatalf("Validate structural prefix mismatch:\n got  %+v\n want %+v", pubGot[:len(structGot)], structGot)
	}
}
