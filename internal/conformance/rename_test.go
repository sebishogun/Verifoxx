package conformance_test

import (
	"encoding/json"
	"os"
	"testing"
)

func TestCanonicalResultsRetainDecisionsAfterRename(t *testing.T) {
	content, err := os.ReadFile("../../results/requests.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Policy struct {
			Name string `json:"name"`
		} `json:"policy"`
		Results []struct {
			RequestID string `json:"request_id"`
			Decision  string `json:"decision"`
		} `json:"results"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	if document.Policy.Name != "nornrune" {
		t.Errorf("policy name = %q, want nornrune", document.Policy.Name)
	}
	want := [...]string{"Approve", "Reject", "Revise", "Escalate", "Escalate"}
	if len(document.Results) != len(want) {
		t.Fatalf("results = %d, want %d", len(document.Results), len(want))
	}
	for row := range want {
		requestID := "R" + string(rune('1'+row))
		if document.Results[row].RequestID != requestID || document.Results[row].Decision != want[row] {
			t.Errorf("result[%d] = (%q,%q), want (%q,%q)", row, document.Results[row].RequestID, document.Results[row].Decision, requestID, want[row])
		}
	}
}
