package ast

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestTemplateContextsAreBounded(t *testing.T) {
	valid := []TemplateContext{
		TemplateContextAssumption,
		TemplateContextDecision,
		TemplateContextUnresolved,
		TemplateContextEvidenceMissing,
		TemplateContextEvidencePresent,
	}
	for _, context := range valid {
		if !context.Valid() {
			t.Errorf("TemplateContext(%d) must be valid", context)
		}
	}
	if TemplateContextInvalid.Valid() || TemplateContext(6).Valid() || TemplateContext(255).Valid() {
		t.Fatal("invalid or out-of-range TemplateContext reported valid")
	}
}

func TestAddTemplateCompilesLiteralAndTypedPlaceholders(t *testing.T) {
	b := NewBuilder(Hints{Templates: 1, TemplateOps: 15, TemplateBytes: 64})
	text := []byte("{policy_name}{policy_version}{request_id}{outcome}{requirement_id}{clause_id}{node_id}{reason}{evidence_kind}{evidence_state}{required_evidence_state}{evidence_id}")
	id, err := b.AddTemplate(text, TemplateContextEvidencePresent)
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 {
		t.Fatalf("TemplateID = %d, want 1", id)
	}
	d := b.Document()
	wantOps := []TemplateOp{
		TemplateOpPolicyName,
		TemplateOpPolicyVersion,
		TemplateOpRequestID,
		TemplateOpOutcome,
		TemplateOpRequirementID,
		TemplateOpClauseID,
		TemplateOpNodeID,
		TemplateOpReason,
		TemplateOpEvidenceKind,
		TemplateOpEvidenceState,
		TemplateOpRequiredEvidenceState,
		TemplateOpEvidenceID,
	}
	if !reflect.DeepEqual(d.TemplateOps, wantOps) {
		t.Fatalf("TemplateOps = %v, want %v", d.TemplateOps, wantOps)
	}
	if !reflect.DeepEqual(d.TemplateArgs, make([]uint32, len(wantOps))) {
		t.Fatalf("TemplateArgs = %v, want zero placeholder args", d.TemplateArgs)
	}
	if !reflect.DeepEqual(d.TemplateOpStarts, []uint32{0}) || !reflect.DeepEqual(d.TemplateOpCounts, []uint16{uint16(len(wantOps))}) {
		t.Fatalf("template operation range = (%v, %v)", d.TemplateOpStarts, d.TemplateOpCounts)
	}
	if !reflect.DeepEqual(d.TemplateLiteralStarts, []uint32{0}) || len(d.TemplateBytes) != 0 {
		t.Fatalf("literal storage = starts %v bytes %q", d.TemplateLiteralStarts, d.TemplateBytes)
	}
	if !reflect.DeepEqual(d.TemplateContexts, []TemplateContext{TemplateContextEvidencePresent}) {
		t.Fatalf("TemplateContexts = %v", d.TemplateContexts)
	}
}

func TestAddTemplateCoalescesLiteralsAndEscapedBraces(t *testing.T) {
	b := NewBuilder(Hints{})
	id, err := b.AddTemplate([]byte("Policy {{name}}: {policy_name}."), TemplateContextAssumption)
	if err != nil {
		t.Fatal(err)
	}
	d := b.Document()
	if id != 1 {
		t.Fatalf("TemplateID = %d, want 1", id)
	}
	if got, want := string(d.TemplateBytes), "Policy {name}: ."; got != want {
		t.Fatalf("TemplateBytes = %q, want %q", got, want)
	}
	wantOps := []TemplateOp{TemplateOpLiteral, TemplateOpPolicyName, TemplateOpLiteral}
	if !reflect.DeepEqual(d.TemplateOps, wantOps) {
		t.Fatalf("TemplateOps = %v, want %v", d.TemplateOps, wantOps)
	}
	if want := []uint32{15, 0, 1}; !reflect.DeepEqual(d.TemplateArgs, want) {
		t.Fatalf("TemplateArgs = %v, want %v", d.TemplateArgs, want)
	}
	if got := d.TemplateMaxBytes[0]; got != 16 {
		t.Fatalf("TemplateMaxBytes = %d, want 16 fixed literal bytes", got)
	}
}

func TestAddTemplateEnforcesPlaceholderContexts(t *testing.T) {
	tests := []struct {
		name    string
		context TemplateContext
		text    string
		wantErr error
	}{
		{"assumption policy", TemplateContextAssumption, "{policy_name}", nil},
		{"assumption request", TemplateContextAssumption, "{request_id}", nil},
		{"assumption rejects outcome", TemplateContextAssumption, "{outcome}", ErrInvalidTemplateContext},
		{"decision node", TemplateContextDecision, "{node_id}", nil},
		{"decision rejects reason", TemplateContextDecision, "{reason}", ErrInvalidTemplateContext},
		{"unresolved reason", TemplateContextUnresolved, "{reason}", nil},
		{"missing required state", TemplateContextEvidenceMissing, "{required_evidence_state}", nil},
		{"missing rejects actual state", TemplateContextEvidenceMissing, "{evidence_state}", ErrInvalidTemplateContext},
		{"missing rejects evidence id", TemplateContextEvidenceMissing, "{evidence_id}", ErrInvalidTemplateContext},
		{"present actual state", TemplateContextEvidencePresent, "{evidence_state}", nil},
		{"present evidence id", TemplateContextEvidencePresent, "{evidence_id}", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBuilder(Hints{})
			_, err := b.AddTemplate([]byte(tt.text), tt.context)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("AddTemplate error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestAddTemplateRejectsMalformedSyntaxWithoutMutation(t *testing.T) {
	tests := []string{
		"{",
		"}",
		"{}",
		"{unknown}",
		"{policy_{name}}",
		"{{{",
		"}}}",
	}
	for _, text := range tests {
		t.Run(text, func(t *testing.T) {
			b := NewBuilder(Hints{})
			if _, err := b.AddTemplate([]byte("before"), TemplateContextAssumption); err != nil {
				t.Fatal(err)
			}
			d := b.Document()
			before := templateColumnLengths(d)
			if _, err := b.AddTemplate([]byte(text), TemplateContextAssumption); !errors.Is(err, ErrInvalidTemplate) {
				t.Fatalf("AddTemplate(%q) error = %v, want ErrInvalidTemplate", text, err)
			}
			if after := templateColumnLengths(d); after != before {
				t.Fatalf("rejected template mutated columns: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestAddTemplateEnforcesHardByteAndOperationLimits(t *testing.T) {
	b := NewBuilder(Hints{})
	if _, err := b.AddTemplate([]byte(strings.Repeat("x", MaxTemplateBytes)), TemplateContextAssumption); err != nil {
		t.Fatalf("maximum byte template failed: %v", err)
	}
	before := templateColumnLengths(b.Document())
	if _, err := b.AddTemplate([]byte(strings.Repeat("x", MaxTemplateBytes+1)), TemplateContextAssumption); !errors.Is(err, ErrTemplateTooLarge) {
		t.Fatalf("over-byte-limit error = %v, want ErrTemplateTooLarge", err)
	}
	if after := templateColumnLengths(b.Document()); after != before {
		t.Fatalf("over-byte-limit template mutated columns: before=%+v after=%+v", before, after)
	}

	exactOps := strings.Repeat("{request_id}x", MaxTemplateOps/2)
	if _, err := b.AddTemplate([]byte(exactOps), TemplateContextAssumption); err != nil {
		t.Fatalf("maximum operation template failed: %v", err)
	}
	before = templateColumnLengths(b.Document())
	overOps := exactOps + "{request_id}"
	if _, err := b.AddTemplate([]byte(overOps), TemplateContextAssumption); !errors.Is(err, ErrTemplateTooLarge) {
		t.Fatalf("over-operation-limit error = %v, want ErrTemplateTooLarge", err)
	}
	if after := templateColumnLengths(b.Document()); after != before {
		t.Fatalf("over-operation-limit template mutated columns: before=%+v after=%+v", before, after)
	}
}

func TestTemplateResetRetainsCapacityAndRestartsIDs(t *testing.T) {
	b := NewBuilder(Hints{Templates: 2, TemplateOps: 8, TemplateBytes: 64})
	if _, err := b.AddTemplate([]byte("before {request_id}"), TemplateContextAssumption); err != nil {
		t.Fatal(err)
	}
	d := b.Document()
	byteCap, opCap, argCap, headerCap := cap(d.TemplateBytes), cap(d.TemplateOps), cap(d.TemplateArgs), cap(d.TemplateOpStarts)
	b.Reset()
	if lengths := templateColumnLengths(d); lengths != (templateLengths{}) {
		t.Fatalf("Reset left active template data: %+v", lengths)
	}
	if cap(d.TemplateBytes) != byteCap || cap(d.TemplateOps) != opCap || cap(d.TemplateArgs) != argCap || cap(d.TemplateOpStarts) != headerCap {
		t.Fatal("Reset discarded template storage capacity")
	}
	id, err := b.AddTemplate([]byte("after"), TemplateContextAssumption)
	if err != nil {
		t.Fatal(err)
	}
	if id != 1 || string(d.TemplateBytes) != "after" {
		t.Fatalf("template after Reset = id %d bytes %q", id, d.TemplateBytes)
	}
}

type templateLengths struct {
	bytes, starts, counts, literals, maxima, contexts, ops, args int
}

func templateColumnLengths(d *Document) templateLengths {
	return templateLengths{
		bytes:    len(d.TemplateBytes),
		starts:   len(d.TemplateOpStarts),
		counts:   len(d.TemplateOpCounts),
		literals: len(d.TemplateLiteralStarts),
		maxima:   len(d.TemplateMaxBytes),
		contexts: len(d.TemplateContexts),
		ops:      len(d.TemplateOps),
		args:     len(d.TemplateArgs),
	}
}
