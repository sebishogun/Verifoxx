package jsondiff

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strconv"
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
		Fields         []field      `json:"fields"`
		EvidenceSets   []any        `json:"evidence_sets"`
		Transitions    []transition `json:"transitions"`
		MaxCandidates  uint64       `json:"max_candidates"`
		BatchRows      uint32       `json:"batch_rows"`
		EvidenceClosed bool         `json:"evidence_closed"`
	}{
		Fields:         []field{{Name: "requester.trust", Kind: "string", Group: "subject", Closed: true, Values: []value{{State: "missing"}, {State: "present", String: "external"}}}},
		EvidenceSets:   []any{map[string]any{"records": []any{}}},
		MaxCandidates:  2,
		BatchRows:      2,
		EvidenceClosed: true,
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

func TestDecodeConfigEvidenceClosureIsExplicit(t *testing.T) {
	openSource := bytes.Replace(validConfigJSON(t), []byte(`"evidence_closed":true`), []byte(`"evidence_closed":false`), 1)
	config, err := DecodeConfig(openSource, Limits{MaxBytes: 1 << 20, MaxFields: 16, MaxValues: 64, MaxEvidenceSets: 16, MaxEvidenceRecords: 64})
	if err != nil {
		t.Fatalf("decode open evidence config: %v", err)
	}
	if _, complete, err := policydiff.ValidateDomain(config.Domain); err != nil || complete {
		t.Fatalf("open evidence domain: complete=%v err=%v", complete, err)
	}
}

func TestDecodeConfigAcceptsOverflowingUnusedDimensionProduct(t *testing.T) {
	var payload map[string]any
	if err := json.Unmarshal(validConfigJSON(t), &payload); err != nil {
		t.Fatal(err)
	}
	fields := payload["fields"].([]any)
	for row := 1; row < 64; row++ {
		fields = append(fields, map[string]any{
			"name": "unused." + strconv.Itoa(row), "kind": "boolean", "group": "context", "closed": true,
			"values": []any{map[string]any{"state": "missing"}, map[string]any{"state": "present"}},
		})
	}
	payload["fields"] = fields
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	config, err := DecodeConfig(encoded, Limits{MaxBytes: 1 << 20, MaxFields: 64, MaxValues: 128, MaxEvidenceSets: 16, MaxEvidenceRecords: 64})
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if len(config.Domain.Fields) != 64 {
		t.Fatalf("domain fields = %d, want 64", len(config.Domain.Fields))
	}
}

func TestPreflightConfigRejectsNullScalars(t *testing.T) {
	valid := validConfigJSON(t)
	for _, test := range []struct {
		name string
		old  string
		new  string
	}{
		{name: "root boolean", old: `"evidence_closed":true`, new: `"evidence_closed":null`},
		{name: "field boolean", old: `"closed":true`, new: `"closed":null`},
		{name: "string value", old: `"string":"external"`, new: `"string":null`},
		{name: "transition boolean", old: `"allowed":true`, new: `"allowed":null`},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := bytes.Replace(valid, []byte(test.old), []byte(test.new), 1)
			if err := preflightConfigJSON(source, Limits{MaxFields: 16, MaxValues: 64, MaxEvidenceSets: 16, MaxEvidenceRecords: 64}); err == nil {
				t.Fatal("preflightConfigJSON() error = nil")
			}
		})
	}
}

func TestDecodeConfigRejectsMalformedUTF8BeforeNormalization(t *testing.T) {
	source := bytes.Replace(validConfigJSON(t), []byte("external"), []byte{0xff}, 1)
	if _, err := DecodeConfig(source, Limits{MaxBytes: 1 << 20, MaxFields: 16, MaxValues: 64, MaxEvidenceSets: 16, MaxEvidenceRecords: 64}); err == nil {
		t.Fatal("DecodeConfig() error = nil")
	}
}

func TestDecodeConfigRejectsCountsBeforeMaterializing(t *testing.T) {
	const rows = 1 << 16
	source := make([]byte, 0, rows*3+256)
	source = append(source, `{"fields":[{"name":"requester.trust","kind":"string","group":"subject","closed":true,"values":[`...)
	for row := 0; row < rows; row++ {
		if row != 0 {
			source = append(source, ',')
		}
		source = append(source, '{', '}')
	}
	source = append(source, `]}],"evidence_sets":[],"transitions":[],"max_candidates":1,"batch_rows":1,"evidence_closed":true}`...)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := DecodeConfig(source, Limits{
		MaxBytes: len(source), MaxFields: 1, MaxValues: 1, MaxEvidenceSets: 1, MaxEvidenceRecords: 1,
	})
	runtime.ReadMemStats(&after)
	if err == nil {
		t.Fatal("DecodeConfig() error = nil")
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated >= 1<<20 {
		t.Fatalf("DecodeConfig() allocated %d bytes before rejecting count limit, want < 1 MiB", allocated)
	}
}
