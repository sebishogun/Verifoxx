package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

func diffConfigJSON(t *testing.T, maxCandidates uint64) []byte {
	t.Helper()
	type value struct {
		State  string `json:"state"`
		String string `json:"string,omitempty"`
	}
	type field struct {
		Name   string  `json:"name"`
		Kind   string  `json:"kind"`
		Group  string  `json:"group"`
		Values []value `json:"values"`
		Closed bool    `json:"closed"`
	}
	type transition struct {
		Old     string `json:"old"`
		New     string `json:"new"`
		Class   string `json:"class"`
		Allowed bool   `json:"allowed"`
	}
	present := []struct{ name, group, value string }{
		{"requester.team", "subject", "trusted_internal"},
		{"requester.trust", "subject", "external"},
		{"action.type", "action", "aggregate_analysis"},
		{"action.output", "resource", "aggregate_counts"},
		{"action.dataset", "resource", "protected_dataset"},
		{"environment.execution_env", "context", "approved_local"},
		{"environment.usage", "context", "standard"},
	}
	payload := struct {
		Fields         []field          `json:"fields"`
		EvidenceSets   []map[string]any `json:"evidence_sets"`
		Transitions    []transition     `json:"transitions"`
		MaxCandidates  uint64           `json:"max_candidates"`
		BatchRows      uint32           `json:"batch_rows"`
		EvidenceClosed bool             `json:"evidence_closed"`
	}{MaxCandidates: maxCandidates, BatchRows: 64, EvidenceClosed: true}
	for _, item := range present {
		payload.Fields = append(payload.Fields, field{
			Name: item.name, Kind: "string", Group: item.group, Closed: true,
			Values: []value{{State: "missing"}, {State: "present", String: item.value}},
		})
	}
	payload.EvidenceSets = []map[string]any{
		{"records": []any{}},
		{"records": []map[string]string{
			{"kind": "approval_record", "state": "valid", "timing": "before_execution"},
			{"kind": "execution_environment_attestation", "state": "verified"},
		}},
	}
	decisions := []string{"Approve", "Reject", "Revise", "Escalate"}
	for _, old := range decisions {
		for _, next := range decisions {
			class := "changed"
			allowed := false
			if old == next {
				class = "equivalent"
				allowed = true
			}
			payload.Transitions = append(payload.Transitions, transition{Old: old, New: next, Class: class, Allowed: allowed})
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func diffDependencies(t *testing.T, maxCandidates uint64) dependencies {
	t.Helper()
	return diffDependenciesWithConfig(t, diffConfigJSON(t, maxCandidates))
}

func diffDependenciesWithConfig(t *testing.T, config []byte) dependencies {
	t.Helper()
	changed := strings.ReplaceAll(nornrune.Source(), `"aggregate_counts"`, `"aggregate_totals"`)
	files := map[string][]byte{
		"old.json":    []byte(nornrune.Source()),
		"new.json":    []byte(changed),
		"same.json":   []byte(nornrune.Source()),
		"domain.json": config,
	}
	return dependencies{
		readFile: func(path string) ([]byte, error) {
			value, ok := files[path]
			if !ok {
				return nil, errors.New("missing file")
			}
			return append([]byte(nil), value...), nil
		},
		now:     func() time.Time { return time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC) },
		version: "test-engine",
	}
}

func TestDiffCommandReturnsInconclusiveExitForIncompleteAllowedChange(t *testing.T) {
	var config map[string]any
	if err := json.Unmarshal(diffConfigJSON(t, 256), &config); err != nil {
		t.Fatal(err)
	}
	fields := config["fields"].([]any)
	for _, raw := range fields {
		field := raw.(map[string]any)
		if field["name"] == "action.output" {
			field["closed"] = false
		}
	}
	for _, transition := range config["transitions"].([]any) {
		transition.(map[string]any)["allowed"] = true
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLIWithDependencies(t, diffDependenciesWithConfig(t, encoded),
		"diff", "--old-policy", "old.json", "--new-policy", "new.json", "--domain", "domain.json")
	if code != 4 || stderr != "" || !strings.Contains(stdout, `"complete":false`) || !strings.Contains(stdout, `"outcome":"changed"`) {
		t.Fatalf("incomplete allowed diff = (%d,%q,%q)", code, stdout, stderr)
	}
}

func TestDiffCommandExitCodesAndFormats(t *testing.T) {
	deps := diffDependencies(t, 256)
	code, stdout, stderr := runCLIWithDependencies(t, deps,
		"diff", "--old-policy", "old.json", "--new-policy", "new.json", "--domain", "domain.json", "--format", "json")
	if code != 3 || stderr != "" || !strings.Contains(stdout, `"outcome":"changed"`) || !strings.Contains(stdout, `"counterexample":{`) {
		t.Fatalf("forbidden diff = (%d,%q,%q)", code, stdout, stderr)
	}
	code, stdout, stderr = runCLIWithDependencies(t, deps,
		"diff", "--old-policy", "old.json", "--new-policy", "same.json", "--domain", "domain.json", "--format", "text")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Outcome: equivalent") {
		t.Fatalf("equivalent diff = (%d,%q,%q)", code, stdout, stderr)
	}

	limited := diffDependencies(t, 1)
	code, stdout, stderr = runCLIWithDependencies(t, limited,
		"diff", "--old-policy", "old.json", "--new-policy", "new.json", "--domain", "domain.json")
	if code != 4 || stderr != "" || !strings.Contains(stdout, `"outcome":"inconclusive"`) {
		t.Fatalf("inconclusive diff = (%d,%q,%q)", code, stdout, stderr)
	}
}

func TestDiffCommandRejectsMultipleStdinAndWriteFailures(t *testing.T) {
	deps := diffDependencies(t, 256)
	code, _, stderr := runCLIWithDependencies(t, deps,
		"diff", "--old-policy", "-", "--new-policy", "-", "--domain", "domain.json")
	if code != 2 || !strings.Contains(stderr, "only one input may read from stdin") {
		t.Fatalf("multiple stdin = (%d,%q)", code, stderr)
	}
	var stderrBuffer bytes.Buffer
	code = executeWithDependencies(
		[]string{"diff", "--old-policy", "old.json", "--new-policy", "same.json", "--domain", "domain.json"},
		bytes.NewReader(nil), errorWriter{}, &stderrBuffer, deps,
	)
	if code != 1 {
		t.Fatalf("write failure code = %d, want 1", code)
	}
}

func TestDiffCommandFixtureCorpus(t *testing.T) {
	deps := dependencies{
		readFile: func(path string) ([]byte, error) {
			if path == "domain.json" {
				return diffFixtureConfigJSON(t), nil
			}
			return os.ReadFile(filepath.Join("..", "..", "..", "testdata", "diff", path))
		},
		now:     func() time.Time { return time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC) },
		version: "test-engine",
	}
	tests := []struct {
		name, oldFile, newFile, outcome string
	}{
		{name: "equivalent", oldFile: "equivalent-old.json", newFile: "equivalent-new.json", outcome: "equivalent"},
		{name: "widened", oldFile: "widened-old.json", newFile: "widened-new.json", outcome: "widened"},
		{name: "narrowed", oldFile: "narrowed-old.json", newFile: "narrowed-new.json", outcome: "narrowed"},
		{name: "native frontend equivalent", oldFile: "equivalent-old.json", newFile: "native-frontend-equivalent.json", outcome: "equivalent"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := runCLIWithDependencies(t, deps,
				"diff", "--old-policy", test.oldFile, "--new-policy", test.newFile,
				"--domain", "domain.json", "--format", "json")
			if code != 0 || stderr != "" || !strings.Contains(stdout, `"outcome":"`+test.outcome+`"`) {
				t.Fatalf("diff = (%d,%q,%q), want outcome %q", code, stdout, stderr, test.outcome)
			}
		})
	}
}

func diffFixtureConfigJSON(t *testing.T) []byte {
	t.Helper()
	decisions := []string{"Approve", "Reject", "Revise", "Escalate"}
	transitions := make([]map[string]any, 0, 16)
	for _, oldDecision := range decisions {
		for _, newDecision := range decisions {
			class := "changed"
			if oldDecision == newDecision {
				class = "equivalent"
			} else if oldDecision == "Reject" && newDecision == "Approve" {
				class = "widened"
			} else if oldDecision == "Approve" && newDecision == "Reject" {
				class = "narrowed"
			}
			transitions = append(transitions, map[string]any{
				"old": oldDecision, "new": newDecision, "class": class, "allowed": true,
			})
		}
	}
	payload := map[string]any{
		"fields": []map[string]any{{
			"name": "request.allowed", "kind": "boolean", "group": "context", "closed": true,
			"values": []map[string]any{
				{"state": "missing"},
				{"state": "present", "boolean": false},
				{"state": "present", "boolean": true},
			},
		}},
		"evidence_sets":  []any{},
		"transitions":    transitions,
		"max_candidates": 3,
		"batch_rows":     2,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
