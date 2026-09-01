package jsondiff

import (
	"encoding/json"
	"testing"

	policydiff "github.com/sebishogun/nornrune/policy/diff"
)

func validConfigJSON(t *testing.T) []byte {
	t.Helper()
	type transition struct {
		Old     string `json:"old"`
		New     string `json:"new"`
		Class   string `json:"class"`
		Allowed bool   `json:"allowed"`
	}
	type value struct {
		State  string `json:"state"`
		String string `json:"string,omitempty"`
	}
	type field struct {
		Name   string  `json:"name"`
		Kind   string  `json:"kind"`
		Group  string  `json:"group"`
		Closed bool    `json:"closed"`
		Values []value `json:"values"`
	}
	payload := struct {
		Fields        []field      `json:"fields"`
		EvidenceSets  []any        `json:"evidence_sets"`
		Transitions   []transition `json:"transitions"`
		MaxCandidates uint64       `json:"max_candidates"`
		BatchRows     uint32       `json:"batch_rows"`
	}{
		Fields:        []field{{Name: "requester.trust", Kind: "string", Group: "subject", Closed: true, Values: []value{{State: "missing"}, {State: "present", String: "external"}}}},
		EvidenceSets:  []any{map[string]any{"records": []any{}}},
		MaxCandidates: 2,
		BatchRows:     2,
	}
	decisions := []string{"Approve", "Reject", "Revise", "Escalate"}
	for _, old := range decisions {
		for _, next := range decisions {
			class := "changed"
			if old == next {
				class = "equivalent"
			}
			payload.Transitions = append(payload.Transitions, transition{Old: old, New: next, Class: class, Allowed: true})
		}
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestDecodeConfigStrictBoundedContract(t *testing.T) {
	config, err := DecodeConfig(validConfigJSON(t), Limits{MaxBytes: 1 << 20, MaxFields: 16, MaxValues: 64, MaxEvidenceSets: 16, MaxEvidenceRecords: 64})
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if len(config.Fields.Fields) != 1 || config.Fields.Fields[0].Kind != policydiff.FieldKindString {
		t.Fatalf("field schema: %+v", config.Fields)
	}
	if cardinality, complete, err := policydiff.ValidateDomain(config.Domain); err != nil || cardinality != 2 || !complete {
		t.Fatalf("domain: (%d,%v,%v)", cardinality, complete, err)
	}
	if err := config.Matrix.Validate(); err != nil {
		t.Fatalf("matrix: %v", err)
	}

	valid := validConfigJSON(t)
	for _, source := range [][]byte{
		append(append([]byte(nil), valid...), []byte(` {}`)...),
		[]byte(`{"unknown":1}`),
		append([]byte(`{"fields":[],`), valid[1:]...),
		[]byte(`null`),
		valid[:len(valid)-1],
	} {
		if _, err := DecodeConfig(source, Limits{MaxBytes: 1 << 20, MaxFields: 16, MaxValues: 64, MaxEvidenceSets: 16, MaxEvidenceRecords: 64}); err == nil {
			t.Fatalf("accepted malformed config %q", source)
		}
	}
	if _, err := DecodeConfig(valid, Limits{MaxBytes: 8}); err == nil {
		t.Fatal("accepted oversized config")
	}
}
