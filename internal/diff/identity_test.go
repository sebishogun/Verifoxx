package diff

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

func nativeFieldSchema() FieldSchema {
	return FieldSchema{Fields: []FieldSpec{
		{Name: "requester.team", Kind: FieldKindString, Group: FieldGroupSubject},
		{Name: "requester.trust", Kind: FieldKindString, Group: FieldGroupSubject},
		{Name: "action.type", Kind: FieldKindString, Group: FieldGroupAction},
		{Name: "action.output", Kind: FieldKindString, Group: FieldGroupAction},
		{Name: "action.dataset", Kind: FieldKindString, Group: FieldGroupResource},
		{Name: "environment.execution_env", Kind: FieldKindString, Group: FieldGroupContext},
		{Name: "environment.usage", Kind: FieldKindString, Group: FieldGroupContext},
	}}
}

func TestCompilePairRejectsMalformedSourceAndSchemaMismatch(t *testing.T) {
	var analyzer Analyzer
	if _, _, err := analyzer.compilePair([]byte(`{`), []byte(nornrune.Source()), nativeFieldSchema()); err == nil {
		t.Fatal("compile malformed old source: got nil error")
	}

	badSchema := nativeFieldSchema()
	badSchema.Fields = badSchema.Fields[:len(badSchema.Fields)-1]
	if _, _, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), badSchema); err == nil {
		t.Fatal("compile source against incomplete schema: got nil error")
	}
}

func TestCompilePairOwnsSourceAndRejectsNonstandardOutcomes(t *testing.T) {
	var analyzer Analyzer
	oldSource := []byte(nornrune.Source())
	oldProgram, _, err := analyzer.compilePair(oldSource, []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatalf("compile pair: %v", err)
	}
	wantInput := bytes.Clone(oldProgram.InputBytes)
	oldSource[0] ^= 0xff
	if !bytes.Equal(oldProgram.InputBytes, wantInput) {
		t.Fatal("compiled program borrowed caller source")
	}

	nonstandard := strings.ReplaceAll(nornrune.Source(), `"Approve"`, `"Permit"`)
	if _, _, err := analyzer.compilePair([]byte(nonstandard), []byte(nornrune.Source()), nativeFieldSchema()); !errors.Is(err, ErrUnsupportedOutcomes) {
		t.Fatalf("compile nonstandard outcomes: got %v, want %v", err, ErrUnsupportedOutcomes)
	}
}

func TestSemanticIdentityIgnoresPresentationAndPolicyIdentity(t *testing.T) {
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(nornrune.Source())); err != nil {
		t.Fatalf("compact source: %v", err)
	}
	renamed := strings.Replace(compact.String(), `"name":"nornrune"`, `"name":"renamed"`, 1)
	renamed = strings.Replace(renamed, `"version":"1.0.0"`, `"version":"2.0.0"`, 1)

	var analyzer Analyzer
	oldProgram, newProgram, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(renamed), nativeFieldSchema())
	if err != nil {
		t.Fatalf("compile pair: %v", err)
	}
	if !semanticSymbolsEqual(oldProgram, newProgram) {
		t.Fatal("presentation change altered behavior-bearing symbols")
	}
	if !semanticValuesEqual(oldProgram, newProgram) {
		t.Fatal("presentation change altered behavior-bearing values")
	}
	if !semanticIdentity(oldProgram, newProgram) {
		t.Fatal("formatting and policy identity changed semantic identity")
	}

	changedSpans, err := program.Freeze(oldProgram)
	if err != nil {
		t.Fatalf("clone program: %v", err)
	}
	changedSpans.InstructionSourceStarts[0]++
	changedSpans.RequirementSourceEnds[0]++
	changedSpans.OutcomeSourceStarts[0]++
	if !semanticIdentity(oldProgram, &changedSpans) {
		t.Fatal("source-span-only changes changed semantic identity")
	}
}

func TestSemanticIdentityDetectsBehaviorSlabChanges(t *testing.T) {
	var analyzer Analyzer
	baseline, _, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatalf("compile pair: %v", err)
	}

	tests := []struct {
		name   string
		change func(*program.Program)
	}{
		{name: "instruction", change: func(candidate *program.Program) { candidate.Values[0]++ }},
		{name: "resolution", change: func(candidate *program.Program) { candidate.Resolutions.OutcomeIDs[0]++ }},
		{name: "remediation", change: func(candidate *program.Program) { candidate.Remediations.Values[0]++ }},
		{name: "evidence catalog", change: func(candidate *program.Program) { candidate.EvidenceKindNames[0] = candidate.EvidenceKindNames[1] }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate, err := program.Freeze(baseline)
			if err != nil {
				t.Fatalf("clone program: %v", err)
			}
			test.change(&candidate)
			if semanticIdentity(baseline, &candidate) {
				t.Fatal("behavior-bearing change reported identical")
			}
		})
	}
}

func TestResultSemanticsCompareRemediationValuePayloads(t *testing.T) {
	var analyzer Analyzer
	baseline, _, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		t.Fatalf("compile pair: %v", err)
	}
	oldCandidate, err := program.Freeze(baseline)
	if err != nil {
		t.Fatalf("clone program: %v", err)
	}
	candidate, err := program.Freeze(baseline)
	if err != nil {
		t.Fatalf("clone candidate: %v", err)
	}
	if len(candidate.Remediations.Values) == 0 {
		t.Fatal("fixture has no remediation")
	}
	valueID := schema.ValueID(0)
	for row, kind := range candidate.ValueKinds {
		if kind == schema.ValueKindSymbol && row > 1 {
			valueID = schema.ValueID(row + 1)
			break
		}
	}
	if valueID == 0 {
		t.Fatal("fixture has no symbolic remediation payload")
	}
	for _, compiled := range []*program.Program{&oldCandidate, &candidate} {
		compiled.Remediations.Kinds[0] = result.RemediationSetField
		compiled.Remediations.Fields[0] = 1
		compiled.Remediations.Values[0] = valueID
		compiled.Remediations.EvidenceKinds[0] = 0
	}
	valueRow := int(valueID - 1)
	original := candidate.ValueRefs[valueRow]
	for row, kind := range candidate.ValueKinds {
		if kind == candidate.ValueKinds[valueRow] && candidate.ValueRefs[row] != original {
			candidate.ValueRefs[valueRow] = candidate.ValueRefs[row]
			break
		}
	}
	if candidate.ValueRefs[valueRow] == original {
		t.Fatal("fixture has no alternate remediation payload")
	}
	if resultSemanticsEqual(&oldCandidate, &candidate) {
		t.Fatal("different remediation value payloads reported equal")
	}
}
