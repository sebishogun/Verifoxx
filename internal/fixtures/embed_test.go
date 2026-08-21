package fixtures_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"github.com/sebishogun/verifoxx/internal/fixtures"
)

type policyDocument struct {
	SchemaVersion int                 `json:"schema_version"`
	Pack          string              `json:"pack"`
	Document      string              `json:"document"`
	Requirements  []policyRequirement `json:"requirements"`
}

type policyRequirement struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type requestsDocument struct {
	SchemaVersion int             `json:"schema_version"`
	Pack          string          `json:"pack"`
	Requests      []requestRecord `json:"requests"`
}

type requestRecord struct {
	ID           string      `json:"id"`
	Requester    requester   `json:"requester"`
	Action       requestAct  `json:"action"`
	Environment  environment `json:"environment"`
	EvidenceRefs []string    `json:"evidence_refs"`
}

type requester struct {
	Team  string `json:"team"`
	Trust string `json:"trust"`
}

type requestAct struct {
	Type    string `json:"type"`
	Output  string `json:"output"`
	Dataset string `json:"dataset"`
}

type environment struct {
	ExecutionEnv string `json:"execution_env"`
	Usage        string `json:"usage"`
}

type evidenceDocument struct {
	SchemaVersion int              `json:"schema_version"`
	Pack          string           `json:"pack"`
	Evidence      []evidenceRecord `json:"evidence"`
}

type evidenceRecord struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Attributes map[string]string `json:"attributes"`
}

func decodeStrict(t *testing.T, src string, dst any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader([]byte(src)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		t.Fatalf("decode strict: %v", err)
	}
}

func assertUniqueIDs(t *testing.T, name string, ids []string) {
	t.Helper()
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			t.Errorf("%s contains duplicate id %q", name, id)
		}
		seen[id] = true
	}
}

func findRequest(t *testing.T, doc requestsDocument, id string) requestRecord {
	t.Helper()
	for _, r := range doc.Requests {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("request %s not found", id)
	return requestRecord{}
}

func findEvidence(t *testing.T, doc evidenceDocument, id string) evidenceRecord {
	t.Helper()
	for _, e := range doc.Evidence {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("evidence %s not found", id)
	return evidenceRecord{}
}

func TestEmbeddedInputsPresent(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  string
	}{
		{"policy", fixtures.PolicyJSON()},
		{"requests", fixtures.RequestsJSON()},
		{"evidence", fixtures.EvidenceJSON()},
	} {
		if tc.got == "" {
			t.Errorf("%s fixture is empty", tc.name)
		}
	}
}

func TestEmbeddedJSONValid(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  string
	}{
		{"policy", fixtures.PolicyJSON()},
		{"requests", fixtures.RequestsJSON()},
		{"evidence", fixtures.EvidenceJSON()},
	} {
		if !json.Valid([]byte(tc.got)) {
			t.Errorf("%s fixture is not valid JSON", tc.name)
		}
	}
}

func TestFixtureSchemaVersions(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want int
	}{
		{"policy", fixtures.PolicyJSON(), 1},
		{"requests", fixtures.RequestsJSON(), 1},
		{"evidence", fixtures.EvidenceJSON(), 1},
	}
	for _, tc := range tests {
		var version int
		switch tc.name {
		case "policy":
			var doc policyDocument
			decodeStrict(t, tc.src, &doc)
			version = doc.SchemaVersion
		case "requests":
			var doc requestsDocument
			decodeStrict(t, tc.src, &doc)
			version = doc.SchemaVersion
		case "evidence":
			var doc evidenceDocument
			decodeStrict(t, tc.src, &doc)
			version = doc.SchemaVersion
		}
		if version != tc.want {
			t.Errorf("%s schema_version = %d, want %d", tc.name, version, tc.want)
		}
	}
}

func TestPolicyFixtureExactSourceText(t *testing.T) {
	var doc policyDocument
	decodeStrict(t, fixtures.PolicyJSON(), &doc)

	if doc.Pack != "verifoxx" {
		t.Errorf("policy pack = %q, want %q", doc.Pack, "verifoxx")
	}
	if got, want := len(doc.Requirements), 3; got != want {
		t.Fatalf("policy has %d requirements, want %d", got, want)
	}
	ids := make([]string, len(doc.Requirements))
	for i, r := range doc.Requirements {
		ids[i] = r.ID
	}
	assertUniqueIDs(t, "policy", ids)

	want := []policyRequirement{
		{ID: "R1", Text: "External partners may request aggregate analytical outputs from the protected dataset only if no individual-level information is disclosed and a valid approval record exists before execution."},
		{ID: "R2", Text: "Any processing involving protected data must run in the approved local execution environment. If the execution environment cannot be verified, the request must not be automatically approved."},
		{ID: "R3", Text: "Trusted internal teams may request a temporary increase above the standard usage limit, but only where a specific usage-adjustment approval exists. Disclosure restrictions and pre-execution approval conditions cannot be relaxed. If approval evidence is unclear, stale or conflicting, the case should be escalated rather than assumed safe."},
	}
	for i, w := range want {
		if doc.Requirements[i].ID != w.ID {
			t.Errorf("requirement[%d].id = %q, want %q", i, doc.Requirements[i].ID, w.ID)
		}
		if doc.Requirements[i].Text != w.Text {
			t.Errorf("requirement[%d].text = %q, want %q", i, doc.Requirements[i].Text, w.Text)
		}
	}
}

func TestRequestsFixtureExactFields(t *testing.T) {
	var doc requestsDocument
	decodeStrict(t, fixtures.RequestsJSON(), &doc)

	if doc.Pack != "verifoxx" {
		t.Errorf("requests pack = %q, want %q", doc.Pack, "verifoxx")
	}
	if got, want := len(doc.Requests), 5; got != want {
		t.Fatalf("requests has %d records, want %d", got, want)
	}
	ids := make([]string, len(doc.Requests))
	for i, r := range doc.Requests {
		ids[i] = r.ID
	}
	assertUniqueIDs(t, "requests", ids)

	want := []requestRecord{
		{
			ID:           "R1",
			Requester:    requester{Team: "external_partner", Trust: "external"},
			Action:       requestAct{Type: "aggregate_analysis", Output: "aggregate_counts", Dataset: "protected_dataset"},
			Environment:  environment{ExecutionEnv: "local_approved_env", Usage: "standard"},
			EvidenceRefs: []string{"E1", "E2"},
		},
		{
			ID:           "R2",
			Requester:    requester{Team: "external_partner", Trust: "external"},
			Action:       requestAct{Type: "row_level_export", Output: "individual_records", Dataset: "protected_dataset"},
			Environment:  environment{ExecutionEnv: "local_approved_env", Usage: "standard"},
			EvidenceRefs: []string{"E1", "E2"},
		},
		{
			ID:           "R3",
			Requester:    requester{Team: "internal_team", Trust: "trusted_internal"},
			Action:       requestAct{Type: "aggregate_analysis", Output: "aggregate_counts", Dataset: "protected_dataset"},
			Environment:  environment{ExecutionEnv: "local_approved_env", Usage: "above_standard_limit"},
			EvidenceRefs: []string{"E1", "E2"},
		},
		{
			ID:           "R4",
			Requester:    requester{Team: "external_partner", Trust: "external"},
			Action:       requestAct{Type: "aggregate_analysis", Output: "aggregate_counts", Dataset: "protected_dataset"},
			Environment:  environment{ExecutionEnv: "unverified_remote_env", Usage: "standard"},
			EvidenceRefs: []string{"E1"},
		},
		{
			ID:           "R5",
			Requester:    requester{Team: "internal_team", Trust: "trusted_internal"},
			Action:       requestAct{Type: "aggregate_analysis", Output: "aggregate_counts", Dataset: "protected_dataset"},
			Environment:  environment{ExecutionEnv: "local_approved_env", Usage: "above_standard_limit"},
			EvidenceRefs: []string{"E2", "E3", "E4"},
		},
	}
	for i, w := range want {
		got := doc.Requests[i]
		if got.ID != w.ID {
			t.Errorf("request[%d].id = %q, want %q", i, got.ID, w.ID)
		}
		if got.Requester != w.Requester {
			t.Errorf("request[%d].requester = %+v, want %+v", i, got.Requester, w.Requester)
		}
		if got.Action != w.Action {
			t.Errorf("request[%d].action = %+v, want %+v", i, got.Action, w.Action)
		}
		if got.Environment != w.Environment {
			t.Errorf("request[%d].environment = %+v, want %+v", i, got.Environment, w.Environment)
		}
		if !slices.Equal(got.EvidenceRefs, w.EvidenceRefs) {
			t.Errorf("request[%d].evidence_refs = %v, want %v", i, got.EvidenceRefs, w.EvidenceRefs)
		}
	}
}

func TestEvidenceFixtureExactFields(t *testing.T) {
	var doc evidenceDocument
	decodeStrict(t, fixtures.EvidenceJSON(), &doc)

	if doc.Pack != "verifoxx" {
		t.Errorf("evidence pack = %q, want %q", doc.Pack, "verifoxx")
	}
	if got, want := len(doc.Evidence), 4; got != want {
		t.Fatalf("evidence has %d records, want %d", got, want)
	}
	ids := make([]string, len(doc.Evidence))
	for i, e := range doc.Evidence {
		ids[i] = e.ID
	}
	assertUniqueIDs(t, "evidence", ids)

	want := []evidenceRecord{
		{
			ID:   "E1",
			Type: "approval_record",
			Attributes: map[string]string{
				"status":          "valid",
				"timing":          "before_execution",
				"reviewer":        "designated_reviewer",
				"timestamp_state": "current",
			},
		},
		{
			ID:   "E2",
			Type: "execution_environment_attestation",
			Attributes: map[string]string{
				"subject":           "local_approved_env",
				"status":            "verified",
				"attestation_state": "valid",
			},
		},
		{
			ID:   "E3",
			Type: "usage_limit_adjustment",
			Attributes: map[string]string{
				"status":          "approved",
				"scope":           "trusted_internal_only",
				"adjustment_type": "above_standard_limit",
				"reviewer":        "designated_reviewer",
				"timestamp_state": "current",
			},
		},
		{
			ID:   "E4",
			Type: "approval_record",
			Attributes: map[string]string{
				"status":          "conflicting",
				"timing":          "before_execution",
				"reviewer_state":  "one_valid_one_revoked",
				"timestamp_state": "conflicting",
			},
		},
	}
	for i, w := range want {
		got := doc.Evidence[i]
		if got.ID != w.ID {
			t.Errorf("evidence[%d].id = %q, want %q", i, got.ID, w.ID)
		}
		if got.Type != w.Type {
			t.Errorf("evidence[%d].type = %q, want %q", i, got.Type, w.Type)
		}
		if len(got.Attributes) != len(w.Attributes) {
			t.Errorf("evidence[%d] has %d attributes, want %d", i, len(got.Attributes), len(w.Attributes))
			continue
		}
		for k, v := range w.Attributes {
			if got.Attributes[k] != v {
				t.Errorf("evidence[%d].attributes[%q] = %q, want %q", i, k, got.Attributes[k], v)
			}
		}
	}
}

func TestEvidenceRefsResolve(t *testing.T) {
	var reqDoc requestsDocument
	decodeStrict(t, fixtures.RequestsJSON(), &reqDoc)
	var evDoc evidenceDocument
	decodeStrict(t, fixtures.EvidenceJSON(), &evDoc)

	known := make(map[string]bool, len(evDoc.Evidence))
	for _, e := range evDoc.Evidence {
		known[e.ID] = true
	}
	for _, r := range reqDoc.Requests {
		for _, ref := range r.EvidenceRefs {
			if !known[ref] {
				t.Errorf("request %s references unknown evidence %q", r.ID, ref)
			}
		}
	}
}

func TestHighRiskTranscriptionFacts(t *testing.T) {
	var reqDoc requestsDocument
	decodeStrict(t, fixtures.RequestsJSON(), &reqDoc)
	var evDoc evidenceDocument
	decodeStrict(t, fixtures.EvidenceJSON(), &evDoc)

	r2 := findRequest(t, reqDoc, "R2")
	if r2.Action.Type != "row_level_export" || r2.Action.Output != "individual_records" {
		t.Errorf("R2 must be row-level individual records, action=%+v", r2.Action)
	}

	r4 := findRequest(t, reqDoc, "R4")
	if r4.Environment.ExecutionEnv != "unverified_remote_env" {
		t.Errorf("R4 environment = %q, want %q", r4.Environment.ExecutionEnv, "unverified_remote_env")
	}
	if !slices.Equal(r4.EvidenceRefs, []string{"E1"}) {
		t.Errorf("R4 evidence_refs = %v, want only [E1]", r4.EvidenceRefs)
	}

	r5 := findRequest(t, reqDoc, "R5")
	if !slices.Equal(r5.EvidenceRefs, []string{"E2", "E3", "E4"}) {
		t.Errorf("R5 evidence_refs = %v, want [E2 E3 E4]", r5.EvidenceRefs)
	}

	e4 := findEvidence(t, evDoc, "E4")
	if e4.Attributes["status"] != "conflicting" {
		t.Errorf("E4 status = %q, want %q", e4.Attributes["status"], "conflicting")
	}
	if e4.Attributes["reviewer_state"] != "one_valid_one_revoked" {
		t.Errorf("E4 reviewer_state = %q, want %q", e4.Attributes["reviewer_state"], "one_valid_one_revoked")
	}
	if e4.Attributes["timestamp_state"] != "conflicting" {
		t.Errorf("E4 timestamp_state = %q, want %q", e4.Attributes["timestamp_state"], "conflicting")
	}
}
