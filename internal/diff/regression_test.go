package diff

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"
)

func regressionFixture(t *testing.T) (Result, []byte, []byte, [32]byte, [32]byte, [32]byte) {
	t.Helper()
	oldSource := []byte("old policy")
	newSource := []byte("new policy")
	result := Result{
		Outcome: Widened, Complete: true, Forbidden: true, HasCounterexample: true,
		Counterexample: Counterexample{
			Index:  7,
			Fields: []CandidateField{{Name: "requester.trust", Value: Value{Kind: FieldKindString, State: ValuePresent, String: "external"}}},
			Old:    Evaluation{Decision: Reject, OutcomeID: 2},
			New:    Evaluation{Decision: Approve, OutcomeID: 1},
		},
	}
	result.ForbiddenCounterexample = result.Counterexample
	result.HasForbiddenCounterexample = true
	index, _ := transitionIndex(Reject, Approve)
	result.ForbiddenTransitions[index] = 1
	return result, oldSource, newSource, SourceDigest(oldSource), SourceDigest(newSource), CounterexampleDigest(result.Counterexample)
}

func TestRegressionNeverAllowsIncompleteComparison(t *testing.T) {
	result, oldSource, newSource, oldDigest, newDigest, witnessDigest := regressionFixture(t)
	result.Complete = false
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	exception := Exception{
		ID: "EX-7", Reason: "reviewed", Owner: "owner", Expires: now.Add(time.Hour),
		OldDigest: oldDigest, NewDigest: newDigest, WitnessDigest: witnessDigest,
		OldDecision: Reject, NewDecision: Approve,
	}
	decision := CheckRegression(result, oldSource, newSource, []Exception{exception}, now)
	if decision.Allowed || decision.Reason != "comparison is incomplete" {
		t.Fatalf("incomplete comparison decision: %+v", decision)
	}
}

func TestRegressionRequiresExactCurrentException(t *testing.T) {
	result, oldSource, newSource, oldDigest, newDigest, witnessDigest := regressionFixture(t)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.FixedZone("offset", 3600))
	exception := Exception{
		ID: "EX-7", Reason: "reviewed bounded widening", Owner: "policy-team",
		OldDigest: oldDigest, NewDigest: newDigest, WitnessDigest: witnessDigest,
		OldDecision: Reject, NewDecision: Approve,
		Expires: now.UTC().Add(time.Hour),
	}
	decision := CheckRegression(result, oldSource, newSource, []Exception{exception}, now)
	if !decision.Allowed || decision.ExceptionID != exception.ID {
		t.Fatalf("matching exception: %+v", decision)
	}

	tests := []struct {
		name   string
		change func(*Exception)
	}{
		{name: "stale", change: func(value *Exception) { value.Expires = now.UTC() }},
		{name: "old digest", change: func(value *Exception) { value.OldDigest[0] ^= 1 }},
		{name: "new digest", change: func(value *Exception) { value.NewDigest[0] ^= 1 }},
		{name: "witness digest", change: func(value *Exception) { value.WitnessDigest[0] ^= 1 }},
		{name: "old transition", change: func(value *Exception) { value.OldDecision = Escalate }},
		{name: "new transition", change: func(value *Exception) { value.NewDecision = Revise }},
		{name: "missing owner", change: func(value *Exception) { value.Owner = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := exception
			test.change(&changed)
			decision := CheckRegression(result, oldSource, newSource, []Exception{changed}, now)
			if decision.Allowed {
				t.Fatalf("mismatched exception allowed: %+v", decision)
			}
		})
	}
}

func TestRegressionNeverAllowsInconclusiveAndAllowsNonForbidden(t *testing.T) {
	result, oldSource, newSource, oldDigest, newDigest, witnessDigest := regressionFixture(t)
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	exception := Exception{
		ID: "EX-7", Reason: "reviewed", Owner: "owner", Expires: now.Add(time.Hour),
		OldDigest: oldDigest, NewDigest: newDigest, WitnessDigest: witnessDigest,
		OldDecision: Reject, NewDecision: Approve,
	}
	result.Outcome = Inconclusive
	if decision := CheckRegression(result, oldSource, newSource, []Exception{exception}, now); decision.Allowed {
		t.Fatalf("inconclusive result allowed: %+v", decision)
	}
	result.Outcome = Changed
	result.Forbidden = false
	if decision := CheckRegression(result, oldSource, newSource, nil, now); !decision.Allowed {
		t.Fatalf("non-forbidden result rejected: %+v", decision)
	}
}

func TestRegressionExceptionMatchesOnlyOneForbiddenCandidate(t *testing.T) {
	oldSource := []byte("old policy")
	newSource := []byte("new policy")
	firstDifference := Counterexample{
		Index: 0,
		Old:   Evaluation{Decision: Reject},
		New:   Evaluation{Decision: Approve},
	}
	forbiddenWitness := Counterexample{
		Index: 1,
		Old:   Evaluation{Decision: Approve},
		New:   Evaluation{Decision: Reject},
	}
	result := Result{
		Outcome: Changed, Complete: true, Forbidden: true,
		Counterexample: firstDifference, HasCounterexample: true,
		ForbiddenCounterexample: forbiddenWitness, HasForbiddenCounterexample: true,
	}
	forbiddenIndex, _ := transitionIndex(Approve, Reject)
	result.ForbiddenTransitions[forbiddenIndex] = 1
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	exception := Exception{
		ID: "EX-1", Reason: "reviewed", Owner: "policy-team", Expires: now.Add(time.Hour),
		OldDigest: SourceDigest(oldSource), NewDigest: SourceDigest(newSource),
		WitnessDigest: CounterexampleDigest(firstDifference),
		OldDecision:   Reject, NewDecision: Approve,
	}
	if decision := CheckRegression(result, oldSource, newSource, []Exception{exception}, now); decision.Allowed {
		t.Fatalf("exception for unrelated first difference allowed forbidden witness: %+v", decision)
	}
	exception.WitnessDigest = CounterexampleDigest(forbiddenWitness)
	exception.OldDecision = Approve
	exception.NewDecision = Reject
	if decision := CheckRegression(result, oldSource, newSource, []Exception{exception}, now); !decision.Allowed {
		t.Fatalf("exact single forbidden witness rejected: %+v", decision)
	}

	result.ForbiddenTransitions[forbiddenIndex] = 2
	if decision := CheckRegression(result, oldSource, newSource, []Exception{exception}, now); decision.Allowed {
		t.Fatalf("one exception allowed multiple forbidden candidates: %+v", decision)
	}
}

func TestCounterexampleDigestIncludesOwnedTemplateSemantics(t *testing.T) {
	left := Counterexample{Old: Evaluation{AssumptionsDigest: [32]byte{1}}}
	right := Counterexample{Old: Evaluation{AssumptionsDigest: [32]byte{2}}}
	if CounterexampleDigest(left) == CounterexampleDigest(right) {
		t.Fatal("template semantics did not affect witness digest")
	}
}

func TestRegressionExceptionJSONIsStrictAndDeterministic(t *testing.T) {
	_, _, _, oldDigest, newDigest, witnessDigest := regressionFixture(t)
	payload := `[{"id":"EX-7","reason":"reviewed","owner":"policy-team",` +
		`"old_digest":"` + hex.EncodeToString(oldDigest[:]) + `",` +
		`"new_digest":"` + hex.EncodeToString(newDigest[:]) + `",` +
		`"witness_digest":"` + hex.EncodeToString(witnessDigest[:]) + `",` +
		`"old_decision":"Reject","new_decision":"Approve","expires":"2026-09-01T00:00:00Z"}]`
	exceptions, err := DecodeExceptions([]byte(payload), 8)
	if err != nil || len(exceptions) != 1 || exceptions[0].ID != "EX-7" {
		t.Fatalf("decode exceptions: values=%+v err=%v", exceptions, err)
	}
	duplicate := strings.Replace(payload, `"id":"EX-7"`, `"id":"a","id":"EX-7"`, 1)
	for _, malformed := range []string{payload + `{}`, `[{"unknown":1}]`, duplicate, `[] []`, `null`} {
		if _, err := DecodeExceptions([]byte(malformed), 8); err == nil {
			t.Fatalf("accepted malformed exceptions %q", malformed)
		}
	}
	malformedUTF8 := []byte(payload)
	malformedUTF8[strings.Index(payload, "EX-7")] = 0xff
	if _, err := DecodeExceptions(malformedUTF8, 8); err == nil {
		t.Fatal("accepted malformed UTF-8 exception")
	}
}
