package jsonbatch

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/fixtures"
	policyindex "github.com/sebishogun/nornrune/internal/index"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
)

func decoderTestProgram(t testing.TB) *program.Program {
	t.Helper()
	values := []string{
		"p",
		"requester.team",
		"context.count",
		"context.enabled",
		"context.at",
		"context.present",
		"approval_record",
		"valid",
		"stale",
	}
	p := &program.Program{
		PolicyName:         1,
		ProgramSymbolCount: uint32(len(values)),
		FieldNames:         []schema.SymbolID{2, 3, 4, 5, 6},
		FieldKinds:         []schema.ValueKind{schema.ValueKindSymbol, schema.ValueKindInteger, schema.ValueKindBoolean, schema.ValueKindTimestamp, schema.ValueKindPresence},
		FieldGroups:        []schema.FieldGroup{schema.FieldGroupSubject, schema.FieldGroupContext, schema.FieldGroupContext, schema.FieldGroupContext, schema.FieldGroupContext},
		EvidenceKindNames:  []schema.SymbolID{7},
		EvidenceStateNames: []schema.SymbolID{8, 9},
	}
	for _, value := range values {
		p.SymbolStarts = append(p.SymbolStarts, uint32(len(p.SymbolBytes)))
		p.SymbolLengths = append(p.SymbolLengths, uint32(len(value)))
		p.SymbolBytes = append(p.SymbolBytes, value...)
	}
	slots := 4
	for slots < 2*len(values) {
		slots <<= 1
	}
	p.SymbolHashes = make([]uint64, slots)
	p.SymbolIDs = make([]schema.SymbolID, slots)
	mask := uint64(slots - 1)
	for i, value := range values {
		hash := schema.HashSymbol([]byte(value))
		slot := int(hash & mask)
		for p.SymbolIDs[slot] != 0 {
			slot = (slot + 1) & int(mask)
		}
		p.SymbolHashes[slot] = hash
		p.SymbolIDs[slot] = schema.SymbolID(i + 1)
	}
	if err := policyindex.BuildSchema(&p.FieldIndex, p.FieldKinds); err != nil {
		t.Fatalf("BuildSchema: %v", err)
	}
	return p
}

func fixtureDecoderProgram(t testing.TB) *program.Program {
	t.Helper()
	fieldNames := []string{
		"requester.team", "requester.trust", "action.type", "action.output",
		"action.dataset", "environment.execution_env", "environment.usage",
	}
	kindNames := []string{"approval_record", "execution_environment_attestation", "usage_limit_adjustment"}
	stateNames := []string{"valid", "verified", "approved", "conflicting", "stale", "unclear", "unverifiable", "invalid"}
	values := make([]string, 0, 1+len(fieldNames)+len(kindNames)+len(stateNames))
	values = append(values, "nornrune")
	values = append(values, fieldNames...)
	values = append(values, kindNames...)
	values = append(values, stateNames...)
	p := &program.Program{PolicyName: 1, ProgramSymbolCount: uint32(len(values))}
	for _, value := range values {
		p.SymbolStarts = append(p.SymbolStarts, uint32(len(p.SymbolBytes)))
		p.SymbolLengths = append(p.SymbolLengths, uint32(len(value)))
		p.SymbolBytes = append(p.SymbolBytes, value...)
	}
	for i := range fieldNames {
		p.FieldNames = append(p.FieldNames, schema.SymbolID(2+i))
		p.FieldKinds = append(p.FieldKinds, schema.ValueKindSymbol)
		p.FieldGroups = append(p.FieldGroups, schema.FieldGroupContext)
	}
	for i := range kindNames {
		p.EvidenceKindNames = append(p.EvidenceKindNames, schema.SymbolID(2+len(fieldNames)+i))
	}
	for i := range stateNames {
		p.EvidenceStateNames = append(p.EvidenceStateNames, schema.SymbolID(2+len(fieldNames)+len(kindNames)+i))
	}
	slots := 4
	for slots < 2*len(values) {
		slots <<= 1
	}
	p.SymbolHashes = make([]uint64, slots)
	p.SymbolIDs = make([]schema.SymbolID, slots)
	mask := uint64(slots - 1)
	for i, value := range values {
		hash := schema.HashSymbol([]byte(value))
		slot := int(hash & mask)
		for p.SymbolIDs[slot] != 0 {
			slot = (slot + 1) & int(mask)
		}
		p.SymbolHashes[slot] = hash
		p.SymbolIDs[slot] = schema.SymbolID(i + 1)
	}
	if err := policyindex.BuildSchema(&p.FieldIndex, p.FieldKinds); err != nil {
		t.Fatalf("BuildSchema: %v", err)
	}
	return p
}

func requireDecodeError(t *testing.T, err error, input Input, code ErrorCode) *Error {
	t.Helper()
	var got *Error
	if !errors.As(err, &got) {
		t.Fatalf("error = %v, want *Error", err)
	}
	if got.Input != input || got.Code != code {
		t.Fatalf("error = %+v, want input=%s code=%s", got, input, code)
	}
	return got
}

func TestScannerDecodesStringsIntegersAndLiterals(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		var s scanner
		s.reset(InputRequests, []byte(`"a\n\u4e2d\ud83d\ude00"`), Limits{})
		got, err := s.parseString(&s.valueScratch)
		if err != nil || string(got) != "a\n中😀" {
			t.Fatalf("parseString = (%q, %v)", got, err)
		}
		if err := s.finish(); err != nil {
			t.Fatalf("finish: %v", err)
		}
	})

	for _, tc := range []struct {
		src  string
		want int64
	}{
		{"0", 0},
		{"-1", -1},
		{"9223372036854775807", math.MaxInt64},
		{"-9223372036854775808", math.MinInt64},
	} {
		t.Run(tc.src, func(t *testing.T) {
			var s scanner
			s.reset(InputEvidence, []byte(tc.src), Limits{})
			got, err := s.parseInteger()
			if err != nil || got != tc.want {
				t.Fatalf("parseInteger = (%d, %v), want %d", got, err, tc.want)
			}
		})
	}

	var s scanner
	s.reset(InputRequests, []byte(`{"a":[true,false,null,{"b":-2}]}`), Limits{MaxDepth: 4})
	if err := s.skipValue(1); err != nil {
		t.Fatalf("skipValue: %v", err)
	}
	if err := s.finish(); err != nil {
		t.Fatalf("finish: %v", err)
	}
	for _, source := range []string{"1.5", "1e10", "-0.25E-2"} {
		s.reset(InputRequests, []byte(source), Limits{})
		if err := s.skipValue(1); err != nil {
			t.Errorf("skipValue(%q): %v", source, err)
		}
	}
}

func TestScannerReturnsBoundedPositionalErrors(t *testing.T) {
	tests := []struct {
		name   string
		input  Input
		src    []byte
		limits Limits
		code   ErrorCode
		call   func(*scanner) error
	}{
		{"truncated string", InputRequests, []byte(`"x`), Limits{}, CodeTruncated, func(s *scanner) error { _, err := s.parseString(&s.valueScratch); return err }},
		{"bad escape", InputEvidence, []byte(`"\q"`), Limits{}, CodeMalformed, func(s *scanner) error { _, err := s.parseString(&s.valueScratch); return err }},
		{"invalid utf8", InputRequests, []byte{'"', 0xff, '"'}, Limits{}, CodeInvalidUTF8, func(s *scanner) error { _, err := s.parseString(&s.valueScratch); return err }},
		{"string limit", InputEvidence, []byte(`"long"`), Limits{MaxStringBytes: 3}, CodeLimit, func(s *scanner) error { _, err := s.parseString(&s.valueScratch); return err }},
		{"integer overflow", InputRequests, []byte(`9223372036854775808`), Limits{}, CodeLimit, func(s *scanner) error { _, err := s.parseInteger(); return err }},
		{"non-integer number", InputRequests, []byte(`1.5`), Limits{}, CodeInvalidType, func(s *scanner) error { _, err := s.parseInteger(); return err }},
		{"leading zero", InputEvidence, []byte(`01`), Limits{}, CodeMalformed, func(s *scanner) error { _, err := s.parseInteger(); return err }},
		{"depth", InputRequests, []byte(`[[[]]]`), Limits{MaxDepth: 2}, CodeLimit, func(s *scanner) error { return s.skipValue(1) }},
		{"trailing", InputEvidence, []byte(`null x`), Limits{}, CodeTrailing, func(s *scanner) error {
			if err := s.skipValue(1); err != nil {
				return err
			}
			return s.finish()
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s scanner
			s.reset(tc.input, tc.src, tc.limits)
			err := tc.call(&s)
			got := requireDecodeError(t, err, tc.input, tc.code)
			if got.Offset < 0 || got.Offset > len(tc.src) {
				t.Fatalf("error offset %d outside [0,%d]", got.Offset, len(tc.src))
			}
		})
	}
}

func TestScannerEnforcesInternalDepthCeilingWithZeroLimits(t *testing.T) {
	const depth = internalMaxDepth + 1
	source := []byte(strings.Repeat("[", depth) + strings.Repeat("]", depth))
	var s scanner
	s.reset(InputRequests, source, Limits{})
	err := s.skipValue(1)
	requireDecodeError(t, err, InputRequests, CodeLimit)
}

func TestScannerClassifiesNULAsMalformed(t *testing.T) {
	var s scanner
	s.reset(InputEvidence, []byte{0}, Limits{})
	err := s.skipValue(1)
	requireDecodeError(t, err, InputEvidence, CodeMalformed)
}

func TestInputAndErrorCodeNamesAreStable(t *testing.T) {
	if InputRequests.String() != "requests" || InputEvidence.String() != "evidence" {
		t.Fatalf("input names = (%q, %q)", InputRequests, InputEvidence)
	}
	if CodeMalformed.String() != "malformed" || CodeLimit.String() != "limit_exceeded" {
		t.Fatalf("code names = (%q, %q)", CodeMalformed, CodeLimit)
	}
}

func TestCountBatchShapesInArbitraryRootOrder(t *testing.T) {
	requests := []byte(`{"requests":[{"evidence_refs":["E1","E2"],"id":"R1","facts":{}},{"id":"R2"}],"pack":"p","schema_version":1}`)
	evidence := []byte(`{"evidence":[{"id":"E1"},{"id":"E2"},{"id":"E3"}],"schema_version":1,"pack":"p"}`)
	var d Decoder
	requestShape, err := d.count(InputRequests, requests, Limits{})
	if err != nil {
		t.Fatalf("count requests: %v", err)
	}
	if requestShape.requests != 2 || requestShape.refs != 2 || requestShape.evidence != 0 {
		t.Fatalf("request shape = %+v, want 2 requests and 2 refs", requestShape)
	}
	evidenceShape, err := d.count(InputEvidence, evidence, Limits{})
	if err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	if evidenceShape.evidence != 3 || evidenceShape.requests != 0 || evidenceShape.refs != 0 {
		t.Fatalf("evidence shape = %+v, want 3 evidence rows", evidenceShape)
	}
}

func TestCountRejectsInvalidRootsAndLimits(t *testing.T) {
	tests := []struct {
		name   string
		input  Input
		source string
		limits Limits
		code   ErrorCode
	}{
		{"unknown key", InputRequests, `{"schema_version":1,"pack":"p","requests":[],"extra":0}`, Limits{}, CodeUnknownKey},
		{"duplicate key", InputEvidence, `{"schema_version":1,"schema_version":1,"pack":"p","evidence":[]}`, Limits{}, CodeDuplicateKey},
		{"missing key", InputRequests, `{"schema_version":1,"requests":[]}`, Limits{}, CodeMissingKey},
		{"version", InputEvidence, `{"schema_version":2,"pack":"p","evidence":[]}`, Limits{}, CodeInvalidVersion},
		{"payload type", InputRequests, `{"schema_version":1,"pack":"p","requests":{}}`, Limits{}, CodeInvalidType},
		{"row type", InputEvidence, `{"schema_version":1,"pack":"p","evidence":[null]}`, Limits{}, CodeInvalidType},
		{"request limit", InputRequests, `{"schema_version":1,"pack":"p","requests":[{},{}]}`, Limits{MaxRequests: 1}, CodeLimit},
		{"evidence limit", InputEvidence, `{"schema_version":1,"pack":"p","evidence":[{},{}]}`, Limits{MaxEvidence: 1}, CodeLimit},
		{"reference limit", InputRequests, `{"schema_version":1,"pack":"p","requests":[{"evidence_refs":["E1","E2"]}]}`, Limits{MaxEvidenceRefs: 1}, CodeLimit},
		{"source limit", InputRequests, `{"schema_version":1,"pack":"p","requests":[]}`, Limits{MaxRequestBytes: 4}, CodeLimit},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d Decoder
			_, err := d.count(tc.input, []byte(tc.source), tc.limits)
			requireDecodeError(t, err, tc.input, tc.code)
		})
	}
}

func TestBindBuildsProgramCatalogLookups(t *testing.T) {
	p := decoderTestProgram(t)
	var d Decoder
	if err := d.bind(p); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if got, ok := d.lookupField([]byte("requester.team")); !ok || got != 1 {
		t.Fatalf("lookupField = (%d, %v), want (1, true)", got, ok)
	}
	if got, ok := d.lookupField([]byte("unknown")); ok || got != 0 {
		t.Fatalf("unknown lookupField = (%d, %v), want (0, false)", got, ok)
	}
	if got, ok := d.lookupEvidenceKind([]byte("approval_record")); !ok || got != 1 {
		t.Fatalf("lookupEvidenceKind = (%d, %v), want (1, true)", got, ok)
	}
	if got, ok := d.lookupEvidenceState([]byte("stale")); !ok || got != 2 {
		t.Fatalf("lookupEvidenceState = (%d, %v), want (2, true)", got, ok)
	}

	fieldCap := cap(d.fieldTable.keys)
	if err := d.bind(p); err != nil {
		t.Fatalf("repeat bind: %v", err)
	}
	if cap(d.fieldTable.keys) != fieldCap {
		t.Fatalf("repeat bind field capacity = %d, want %d", cap(d.fieldTable.keys), fieldCap)
	}
}

func TestBindRejectsMalformedProgramCatalogs(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*program.Program)
	}{
		{"nil", nil},
		{"zero policy name", func(p *program.Program) { p.PolicyName = 0 }},
		{"field length", func(p *program.Program) { p.FieldNames = p.FieldNames[:1] }},
		{"field kind mismatch", func(p *program.Program) { p.FieldKinds[0] = schema.ValueKindInteger }},
		{"duplicate field", func(p *program.Program) { p.FieldNames[1] = p.FieldNames[0] }},
		{"zero evidence kind", func(p *program.Program) { p.EvidenceKindNames[0] = 0 }},
		{"duplicate evidence state", func(p *program.Program) { p.EvidenceStateNames[1] = p.EvidenceStateNames[0] }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var p *program.Program
			if tc.mutate != nil {
				base := decoderTestProgram(t)
				clone := *base
				clone.FieldNames = append([]schema.SymbolID(nil), base.FieldNames...)
				clone.FieldKinds = append([]schema.ValueKind(nil), base.FieldKinds...)
				clone.FieldGroups = append([]schema.FieldGroup(nil), base.FieldGroups...)
				clone.EvidenceKindNames = append([]schema.SymbolID(nil), base.EvidenceKindNames...)
				clone.EvidenceStateNames = append([]schema.SymbolID(nil), base.EvidenceStateNames...)
				tc.mutate(&clone)
				p = &clone
			}
			var d Decoder
			if err := d.bind(p); !errors.Is(err, ErrInvalidProgram) {
				t.Fatalf("bind error = %v, want %v", err, ErrInvalidProgram)
			}
		})
	}
}

func TestDecodeEvidenceRowsAndStateQualifiers(t *testing.T) {
	p := decoderTestProgram(t)
	var d Decoder
	if err := d.bind(p); err != nil {
		t.Fatalf("bind: %v", err)
	}
	var b eval.Builder
	if err := b.Begin(p, 0, 2, 0); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	source := []byte(`{"pack":"p","evidence":[` +
		`{"attributes":{"status":"valid","subject":"alpha","reviewer":"r","timing":"before","timestamp":42},"type":"approval_record","id":"E2"},` +
		`{"id":"E1","type":"approval_record","attributes":{"status":"valid","timestamp_state":"stale"}}` +
		`],"schema_version":1}`)
	if err := d.decodeEvidence(&b, source, Limits{}, 2); err != nil {
		t.Fatalf("decodeEvidence: %v", err)
	}
	batch, err := b.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if got := batch.Evidence.IDs; len(got) != 2 || got[0] != 2 || got[1] != 1 {
		t.Fatalf("evidence IDs = %v, want [2 1]", got)
	}
	if got := batch.Evidence.States; len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("evidence states = %v, want [valid stale]", got)
	}
	if batch.Evidence.Timestamps[0] != 42 {
		t.Fatalf("timestamp = %d, want 42", batch.Evidence.Timestamps[0])
	}
	for _, id := range []schema.SymbolID{
		batch.Evidence.Subjects[0], batch.Evidence.Reviewers[0], batch.Evidence.Timings[0],
	} {
		if id <= schema.SymbolID(p.ProgramSymbolCount) {
			t.Fatalf("extension symbol ID %d does not exceed ProgramSymbolCount %d", id, p.ProgramSymbolCount)
		}
	}
	if row, ok := d.lookupEvidenceRow(1); !ok || row != 1 {
		t.Fatalf("lookupEvidenceRow(E1) = (%d, %v), want (1, true)", row, ok)
	}
}

func TestDecodeEvidenceQualifierPrecedenceIsOrderIndependent(t *testing.T) {
	p := fixtureDecoderProgram(t)
	var d Decoder
	if err := d.bind(p); err != nil {
		t.Fatalf("bind: %v", err)
	}
	var b eval.Builder
	if err := b.Begin(p, 0, 2, 0); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	source := []byte(`{"schema_version":1,"pack":"nornrune","evidence":[` +
		`{"id":"E1","type":"approval_record","attributes":{"status":"valid","timestamp_state":"stale","attestation_state":"invalid"}},` +
		`{"id":"E2","type":"approval_record","attributes":{"status":"valid","attestation_state":"invalid","timestamp_state":"stale"}}]}`)
	if err := d.decodeEvidence(&b, source, Limits{}, 2); err != nil {
		t.Fatalf("decodeEvidence: %v", err)
	}
	batch, err := b.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if got := batch.Evidence.States; !slices.Equal(got, []schema.EvidenceStateID{8, 8}) {
		t.Fatalf("evidence states = %v, want invalid independent of key order", got)
	}
}

func TestDecodeEvidenceRejectsInvalidRecords(t *testing.T) {
	tests := []struct {
		name string
		rows uint32
		body string
		code ErrorCode
	}{
		{"duplicate ID", 2, `{"id":"E1","type":"approval_record","attributes":{"status":"valid"}},{"id":"E1","type":"approval_record","attributes":{"status":"valid"}}`, CodeDuplicateID},
		{"missing status", 1, `{"id":"E1","type":"approval_record","attributes":{}}`, CodeMissingKey},
		{"unknown kind", 1, `{"id":"E1","type":"unknown","attributes":{"status":"valid"}}`, CodeInvalidReference},
		{"unknown state", 1, `{"id":"E1","type":"approval_record","attributes":{"status":"unknown"}}`, CodeInvalidReference},
		{"unknown attribute", 1, `{"id":"E1","type":"approval_record","attributes":{"status":"valid","other":"x"}}`, CodeUnknownKey},
		{"null timestamp", 1, `{"id":"E1","type":"approval_record","attributes":{"status":"valid","timestamp":null}}`, CodeInvalidType},
		{"bad ID", 1, `{"id":"E01","type":"approval_record","attributes":{"status":"valid"}}`, CodeInvalidID},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := decoderTestProgram(t)
			var d Decoder
			if err := d.bind(p); err != nil {
				t.Fatalf("bind: %v", err)
			}
			var b eval.Builder
			if err := b.Begin(p, 0, tc.rows, 0); err != nil {
				t.Fatalf("Begin: %v", err)
			}
			source := []byte(`{"schema_version":1,"pack":"p","evidence":[` + tc.body + `]}`)
			err := d.decodeEvidence(&b, source, Limits{}, tc.rows)
			requireDecodeError(t, err, InputEvidence, tc.code)
		})
	}
}

func TestCanonicalExternalID(t *testing.T) {
	for _, tc := range []struct {
		value  string
		prefix byte
		want   uint32
		ok     bool
	}{
		{"R1", 'R', 1, true},
		{"E4294967295", 'E', math.MaxUint32, true},
		{"E0", 'E', 0, false},
		{"E01", 'E', 0, false},
		{"E4294967296", 'E', 0, false},
		{"X1", 'E', 0, false},
	} {
		got, ok := canonicalID([]byte(tc.value), tc.prefix)
		if got != tc.want || ok != tc.ok {
			t.Errorf("canonicalID(%q, %q) = (%d, %v), want (%d, %v)", tc.value, tc.prefix, got, ok, tc.want, tc.ok)
		}
	}
}

func TestDecodeRequestFactsAndEvidenceCSR(t *testing.T) {
	p := decoderTestProgram(t)
	var d Decoder
	if err := d.bind(p); err != nil {
		t.Fatalf("bind: %v", err)
	}
	var b eval.Builder
	if err := b.Begin(p, 2, 2, 2); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	evidence := []byte(`{"schema_version":1,"pack":"p","evidence":[` +
		`{"id":"E1","type":"approval_record","attributes":{"status":"valid"}},` +
		`{"id":"E2","type":"approval_record","attributes":{"status":"valid"}}]}`)
	if err := d.decodeEvidence(&b, evidence, Limits{}, 2); err != nil {
		t.Fatalf("decodeEvidence: %v", err)
	}
	requests := []byte(`{"pack":"p","requests":[` +
		`{"context":{"present":true,"at":-1,"enabled":false,"count":0},"evidence_refs":["E2","E1"],"requester":{"team":"alpha"},"id":"R2"},` +
		`{"id":"R1","requester":{"team":null}}` +
		`],"schema_version":1}`)
	if err := d.decodeRequests(&b, requests, Limits{}, 2, 2); err != nil {
		t.Fatalf("decodeRequests: %v", err)
	}
	batch, err := b.Finish()
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if got := batch.RequestIDs; len(got) != 2 || got[0] != 2 || got[1] != 1 {
		t.Fatalf("RequestIDs = %v, want [2 1]", got)
	}
	if batch.SymbolValues[0] == 0 || batch.SymbolValues[1] != 0 {
		t.Fatalf("SymbolValues = %v, want present alpha then missing", batch.SymbolValues)
	}
	if batch.IntegerValues[0] != 0 || batch.TimestampValues[0] != -1 || batch.Boolean(0, 0) {
		t.Fatalf("typed values = integers %v timestamps %v booleans %#x", batch.IntegerValues, batch.TimestampValues, batch.BooleanValues)
	}
	for field := schema.FieldID(1); field <= 5; field++ {
		if !batch.Present(field, 0) {
			t.Errorf("field %d row 0 is missing", field)
		}
		if batch.Present(field, 1) {
			t.Errorf("field %d row 1 unexpectedly present", field)
		}
	}
	if got := batch.EvidenceOffsets; len(got) != 3 || got[0] != 0 || got[1] != 2 || got[2] != 2 {
		t.Fatalf("EvidenceOffsets = %v, want [0 2 2]", got)
	}
	if got := batch.EvidenceRefs; len(got) != 2 || got[0] != 1 || got[1] != 0 {
		t.Fatalf("EvidenceRefs = %v, want [1 0]", got)
	}
}

func TestDecodeRequestsRejectsInvalidRows(t *testing.T) {
	tests := []struct {
		name string
		rows uint32
		refs uint32
		body string
		code ErrorCode
	}{
		{"duplicate ID", 2, 0, `{"id":"R1"},{"id":"R1"}`, CodeDuplicateID},
		{"missing ID", 1, 0, `{"requester":{"team":"x"}}`, CodeMissingKey},
		{"unknown field", 1, 0, `{"id":"R1","requester":{"unknown":"x"}}`, CodeInvalidReference},
		{"wrong type", 1, 0, `{"id":"R1","context":{"count":"x"}}`, CodeInvalidType},
		{"non-integer number", 1, 0, `{"id":"R1","context":{"count":1.5}}`, CodeInvalidType},
		{"missing reference", 1, 1, `{"id":"R1","evidence_refs":["E2"]}`, CodeInvalidReference},
		{"duplicate reference", 1, 2, `{"id":"R1","evidence_refs":["E1","E1"]}`, CodeDuplicateReference},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := decoderTestProgram(t)
			var d Decoder
			if err := d.bind(p); err != nil {
				t.Fatalf("bind: %v", err)
			}
			var b eval.Builder
			if err := b.Begin(p, tc.rows, 1, tc.refs); err != nil {
				t.Fatalf("Begin: %v", err)
			}
			evidence := []byte(`{"schema_version":1,"pack":"p","evidence":[{"id":"E1","type":"approval_record","attributes":{"status":"valid"}}]}`)
			if err := d.decodeEvidence(&b, evidence, Limits{}, 1); err != nil {
				t.Fatalf("decodeEvidence: %v", err)
			}
			source := []byte(`{"schema_version":1,"pack":"p","requests":[` + tc.body + `]}`)
			err := d.decodeRequests(&b, source, Limits{}, tc.rows, tc.refs)
			requireDecodeError(t, err, InputRequests, tc.code)
		})
	}
}

func TestDecodeBaselineNornRunePacks(t *testing.T) {
	p := fixtureDecoderProgram(t)
	requests := []byte(fixtures.RequestsJSON())
	evidence := []byte(fixtures.EvidenceJSON())
	var d Decoder
	var b eval.Builder
	batch, err := d.Decode(&b, p, requests, evidence, Limits{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if batch.Rows != 5 || !slices.Equal(batch.RequestIDs, []schema.RequestID{1, 2, 3, 4, 5}) {
		t.Fatalf("requests = %v rows=%d", batch.RequestIDs, batch.Rows)
	}
	wantOffsets := []uint32{0, 2, 4, 6, 7, 10}
	wantRefs := []uint32{0, 1, 0, 1, 0, 1, 0, 1, 2, 3}
	if !slices.Equal(batch.EvidenceOffsets, wantOffsets) || !slices.Equal(batch.EvidenceRefs, wantRefs) {
		t.Fatalf("evidence CSR = %v %v, want %v %v", batch.EvidenceOffsets, batch.EvidenceRefs, wantOffsets, wantRefs)
	}
	if !slices.Equal(batch.Evidence.IDs, []schema.EvidenceID{1, 2, 3, 4}) ||
		!slices.Equal(batch.Evidence.Kinds, []schema.EvidenceKindID{1, 2, 3, 1}) ||
		!slices.Equal(batch.Evidence.States, []schema.EvidenceStateID{1, 2, 3, 4}) {
		t.Fatalf("evidence identity = IDs %v kinds %v states %v", batch.Evidence.IDs, batch.Evidence.Kinds, batch.Evidence.States)
	}
	const rows = 5
	wantTeams := []string{"external_partner", "external_partner", "internal_team", "external_partner", "internal_team"}
	for row, want := range wantTeams {
		id := batch.SymbolValues[row]
		got, ok := b.Symbol(id)
		if !ok || string(got) != want {
			t.Errorf("requester.team row %d = (%q, %v), want %q", row, got, ok, want)
		}
		for field := schema.FieldID(1); field <= 7; field++ {
			if !batch.Present(field, uint32(row)) {
				t.Errorf("field %d row %d is missing", field, row)
			}
		}
	}
	if len(batch.SymbolValues) != 7*rows {
		t.Fatalf("symbol column length = %d, want %d", len(batch.SymbolValues), 7*rows)
	}
}

func TestDecodeFailureAbortsAndRecovers(t *testing.T) {
	p := fixtureDecoderProgram(t)
	requests := []byte(fixtures.RequestsJSON())
	evidence := []byte(fixtures.EvidenceJSON())
	var d Decoder
	var b eval.Builder
	first, err := d.Decode(&b, p, requests, evidence, Limits{})
	if err != nil {
		t.Fatalf("first Decode: %v", err)
	}
	wantCap := cap(first.RequestIDs)
	bad := []byte(`{"schema_version":1,"pack":"nornrune","requests":[{"id":"R1","unknown":"x"}]}`)
	if _, err := d.Decode(&b, p, bad, evidence, Limits{}); err == nil {
		t.Fatal("malformed Decode succeeded")
	}
	if _, err := b.Finish(); !errors.Is(err, eval.ErrInvalidBuilder) {
		t.Fatalf("Finish after failed Decode = %v, want %v", err, eval.ErrInvalidBuilder)
	}
	second, err := d.Decode(&b, p, requests, evidence, Limits{})
	if err != nil {
		t.Fatalf("recovery Decode: %v", err)
	}
	if cap(second.RequestIDs) != wantCap || !slices.Equal(second.RequestIDs, []schema.RequestID{1, 2, 3, 4, 5}) {
		t.Fatalf("recovered requests = %v cap=%d, want cap=%d", second.RequestIDs, cap(second.RequestIDs), wantCap)
	}
}

func TestDecodeWarmPathAllocatesZero(t *testing.T) {
	p := fixtureDecoderProgram(t)
	requests := []byte(fixtures.RequestsJSON())
	evidence := []byte(fixtures.EvidenceJSON())
	var d Decoder
	var b eval.Builder
	decode := func() {
		if _, err := d.Decode(&b, p, requests, evidence, Limits{}); err != nil {
			panic(err)
		}
	}
	decode()
	if got := testing.AllocsPerRun(100, decode); got != 0 {
		t.Fatalf("warm Decode allocations = %v, want 0", got)
	}
}
