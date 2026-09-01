package jsondiff

import (
	"strings"
	"testing"

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
	encoded := AppendResultJSON(nil, result)
	wantPrefix := `{"outcome":"changed","complete":true,"forbidden":true,"candidates":7,"counterexample":{`
	if !strings.HasPrefix(string(encoded), wantPrefix) || !strings.Contains(string(encoded), `"name":"field\"name"`) || !strings.Contains(string(encoded), `"string":"line\nvalue"`) {
		t.Fatalf("JSON output: %s", encoded)
	}
	text := string(AppendResultText(nil, result))
	if !strings.Contains(text, "Outcome: changed") || !strings.Contains(text, "Reject -> Approve") {
		t.Fatalf("text output: %s", text)
	}
}
