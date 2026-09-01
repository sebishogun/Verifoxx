package diff_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sebishogun/nornrune/policies/nornrune"
	"github.com/sebishogun/nornrune/policy/diff"
)

func fuzzSchemaAndDomain(budget uint64) (diff.FieldSchema, diff.Domain) {
	fields := []diff.FieldSpec{
		{Name: "requester.team", Kind: diff.FieldKindString, Group: diff.FieldGroupSubject},
		{Name: "requester.trust", Kind: diff.FieldKindString, Group: diff.FieldGroupSubject},
		{Name: "action.type", Kind: diff.FieldKindString, Group: diff.FieldGroupAction},
		{Name: "action.output", Kind: diff.FieldKindString, Group: diff.FieldGroupResource},
		{Name: "action.dataset", Kind: diff.FieldKindString, Group: diff.FieldGroupResource},
		{Name: "environment.execution_env", Kind: diff.FieldKindString, Group: diff.FieldGroupContext},
		{Name: "environment.usage", Kind: diff.FieldKindString, Group: diff.FieldGroupContext},
	}
	domain := diff.Domain{MaxCandidates: budget, BatchRows: 64, EvidenceSets: []diff.EvidenceSet{{}}}
	for _, field := range fields {
		domain.Fields = append(domain.Fields, diff.FieldDomain{
			Name: field.Name, Kind: field.Kind, Closed: true,
			Values: []diff.Value{{Kind: field.Kind, State: diff.ValueMissing}, {Kind: field.Kind, State: diff.ValuePresent, String: "candidate"}},
		})
	}
	return diff.FieldSchema{Fields: fields}, domain
}

func fuzzMatrix() diff.RiskMatrix {
	var matrix diff.RiskMatrix
	for old := diff.Approve; old <= diff.Escalate; old++ {
		for next := diff.Approve; next <= diff.Escalate; next++ {
			class := diff.Changed
			if old == next {
				class = diff.Equivalent
			}
			_ = matrix.Set(old, next, diff.Transition{Class: class, Allowed: true})
		}
	}
	return matrix
}

func FuzzCompare(f *testing.F) {
	f.Add("aggregate_counts", uint64(128))
	f.Add("aggregate_totals", uint64(127))
	f.Fuzz(func(t *testing.T, replacement string, budget uint64) {
		if len(replacement) > 64 {
			replacement = replacement[:64]
		}
		oldSource := []byte(nornrune.Source())
		newSource := []byte(strings.ReplaceAll(nornrune.Source(), "aggregate_counts", replacement))
		fields, domain := fuzzSchemaAndDomain(budget)
		var analyzer diff.Analyzer
		var forward diff.Result
		err := analyzer.Compare(context.Background(), &forward, oldSource, newSource, fields, domain, fuzzMatrix(), nil)
		if err != nil {
			return
		}
		if !forward.Outcome.Valid() || (forward.Outcome == diff.Equivalent && !forward.Complete) {
			t.Fatalf("invalid forward result: %+v", forward)
		}
		var reverse diff.Result
		if err := analyzer.Compare(context.Background(), &reverse, newSource, oldSource, fields, domain, fuzzMatrix(), nil); err != nil {
			return
		}
		if forward.Outcome != reverse.Outcome {
			t.Fatalf("asymmetric result: forward=%s reverse=%s", forward.Outcome, reverse.Outcome)
		}
		if forward.HasCounterexample {
			name := forward.Counterexample.Fields[0].Name
			oldSource[0] ^= 0xff
			newSource[0] ^= 0xff
			if forward.Counterexample.Fields[0].Name != name {
				t.Fatal("result borrowed fuzz input")
			}
		}
	})
}
