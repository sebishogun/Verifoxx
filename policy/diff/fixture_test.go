package diff_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	policyDiff "github.com/sebishogun/nornrune/policy/diff"
)

func TestPublicAnalyzerFixtureCorpus(t *testing.T) {
	fields := policyDiff.FieldSchema{Fields: []policyDiff.FieldSpec{
		{Name: "request.allowed", Kind: policyDiff.FieldKindBoolean, Group: policyDiff.FieldGroupContext},
	}}
	domain := policyDiff.Domain{
		Fields: []policyDiff.FieldDomain{{
			Name: "request.allowed",
			Kind: policyDiff.FieldKindBoolean,
			Values: []policyDiff.Value{
				{State: policyDiff.ValueMissing, Kind: policyDiff.FieldKindBoolean},
				{State: policyDiff.ValuePresent, Kind: policyDiff.FieldKindBoolean},
				{State: policyDiff.ValuePresent, Kind: policyDiff.FieldKindBoolean, Boolean: true},
			},
			Closed: true,
		}},
		MaxCandidates: 3,
		BatchRows:     2,
	}
	matrix := corpusRiskMatrix(t)
	tests := []struct {
		name, oldFile, newFile string
		want                   policyDiff.Outcome
		wantCandidates         uint64
	}{
		{name: "equivalent", oldFile: "equivalent-old.json", newFile: "equivalent-new.json", want: policyDiff.Equivalent},
		{name: "widened", oldFile: "widened-old.json", newFile: "widened-new.json", want: policyDiff.Widened, wantCandidates: 3},
		{name: "narrowed", oldFile: "narrowed-old.json", newFile: "narrowed-new.json", want: policyDiff.Narrowed, wantCandidates: 3},
		{name: "native frontend equivalent", oldFile: "equivalent-old.json", newFile: "native-frontend-equivalent.json", want: policyDiff.Equivalent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldSource := readDiffFixture(t, test.oldFile)
			newSource := readDiffFixture(t, test.newFile)
			var analyzer policyDiff.Analyzer
			var result policyDiff.Result
			if err := analyzer.Compare(
				context.Background(), &result, oldSource, newSource, fields, domain, matrix, nil,
			); err != nil {
				t.Fatalf("Compare() error = %v", err)
			}
			if result.Outcome != test.want || !result.Complete || result.Candidates != test.wantCandidates {
				t.Fatalf("result = %+v, want complete %s over %d candidates", result, test.want, test.wantCandidates)
			}
		})
	}
}

func corpusRiskMatrix(t *testing.T) policyDiff.RiskMatrix {
	t.Helper()
	var matrix policyDiff.RiskMatrix
	decisions := [...]policyDiff.Decision{
		policyDiff.Approve, policyDiff.Reject, policyDiff.Revise, policyDiff.Escalate,
	}
	for _, oldDecision := range decisions {
		for _, newDecision := range decisions {
			transition := policyDiff.Transition{Class: policyDiff.Changed, Allowed: true}
			if oldDecision == newDecision {
				transition.Class = policyDiff.Equivalent
			}
			if oldDecision == policyDiff.Reject && newDecision == policyDiff.Approve {
				transition.Class = policyDiff.Widened
			}
			if oldDecision == policyDiff.Approve && newDecision == policyDiff.Reject {
				transition.Class = policyDiff.Narrowed
			}
			if err := matrix.Set(oldDecision, newDecision, transition); err != nil {
				t.Fatal(err)
			}
		}
	}
	return matrix
}

func readDiffFixture(t *testing.T, name string) []byte {
	t.Helper()
	source, err := os.ReadFile(filepath.Join("..", "..", "testdata", "diff", name))
	if err != nil {
		t.Fatal(err)
	}
	return source
}
