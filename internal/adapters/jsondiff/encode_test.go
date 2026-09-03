package jsondiff

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	policydiff "github.com/sebishogun/nornrune/policy/diff"
)

func TestAppendResultStableEscapedJSONAndText(t *testing.T) {
	result := policydiff.Result{
		Outcome: policydiff.Changed, Candidates: 7, Complete: true, Forbidden: true, HasCounterexample: true,
		Counterexample: policydiff.Counterexample{
			Index:  3,
			Fields: []policydiff.CandidateField{{Name: "field\"name", Value: policydiff.Value{Kind: policydiff.FieldKindString, State: policydiff.ValuePresent, String: "line\nvalue"}}},
			Old:    policydiff.Evaluation{Decision: policydiff.Reject},
			New:    policydiff.Evaluation{Decision: policydiff.Approve},
		},
	}
	result.ForbiddenCounterexample = result.Counterexample
	result.HasForbiddenCounterexample = true
	result.Transitions[4] = 7
	result.ForbiddenTransitions[4] = 2
	encoded := AppendResultJSON(nil, result)
	wantPrefix := `{"outcome":"changed","complete":true,"forbidden":true,"candidates":7,"counterexample":{`
	if !strings.HasPrefix(string(encoded), wantPrefix) || !strings.Contains(string(encoded), `"name":"field\"name"`) || !strings.Contains(string(encoded), `"string":"line\nvalue"`) {
		t.Fatalf("JSON output: %s", encoded)
	}
	if !strings.Contains(string(encoded), `"transitions":[`) || !strings.Contains(string(encoded), `"count":7,"forbidden_count":2`) ||
		!strings.Contains(string(encoded), `"forbidden_counterexample":{`) || !strings.Contains(string(encoded), `"assumptions_digest":`) {
		t.Fatalf("JSON output omitted transition or witness semantics: %s", encoded)
	}
	text := string(AppendResultText(nil, result))
	if !strings.Contains(text, "Outcome: changed") || !strings.Contains(text, "Reject -> Approve") {
		t.Fatalf("text output: %s", text)
	}
}

func TestAppendResultJSONEscapesAllValidStringValues(t *testing.T) {
	value := "\x00\a\v\x7f\U000e0001"
	result := policydiff.Result{
		Outcome: policydiff.Changed, HasCounterexample: true,
		Counterexample: policydiff.Counterexample{
			Fields: []policydiff.CandidateField{{
				Name:  value,
				Value: policydiff.Value{Kind: policydiff.FieldKindString, State: policydiff.ValuePresent, String: value},
			}},
			Evidence: []policydiff.Evidence{{
				Kind: value, State: value, Subject: value, Scope: value, Timing: value,
			}},
		},
	}
	encoded := AppendResultJSON(nil, result)
	if !utf8.Valid(encoded) || !json.Valid(encoded) {
		t.Fatalf("AppendResultJSON() produced invalid JSON: %q", encoded)
	}
}
