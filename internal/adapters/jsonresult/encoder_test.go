package jsonresult

import (
	"bytes"
	"math"
	"os"
	"slices"
	"testing"

	"github.com/sebishogun/verifoxx/internal/adapters/jsonbatch"
	"github.com/sebishogun/verifoxx/internal/adapters/jsonpolicy"
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/buildinfo"
	"github.com/sebishogun/verifoxx/internal/compile"
	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/fixtures"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

type encodingFixture struct {
	program    *program.Program
	requestIDs []schema.RequestID
	batch      result.Batch
	golden     []byte
}

func TestAppendJSONStringEscapesCanonicalBytes(t *testing.T) {
	tests := []struct {
		name  string
		value []byte
		want  string
	}{
		{"empty", nil, `""`},
		{"ascii", []byte("plain/value"), `"plain/value"`},
		{"quote and slash", []byte{'"', '\\'}, `"\"\\"`},
		{"short controls", []byte{'\b', '\f', '\n', '\r', '\t'}, `"\b\f\n\r\t"`},
		{"generic controls", []byte{0, 1, 0x1f}, `"\u0000\u0001\u001f"`},
		{"html sensitive", []byte("<>&"), `"\u003c\u003e\u0026"`},
		{"line separators", []byte("a\u2028b\u2029c"), `"a\u2028b\u2029c"`},
		{"valid utf8", []byte{'c', 'a', 'f', 0xc3, 0xa9}, "\"caf\xc3\xa9\""},
		{"invalid utf8", []byte{0xff, 'x', 0xc3}, `"\ufffdx\ufffd"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendJSONString([]byte("prefix:"), tt.value)
			if string(got) != "prefix:"+tt.want {
				t.Fatalf("appendJSONString = %q, want %q", got, "prefix:"+tt.want)
			}
		})
	}
}

func TestAppendPrimitives(t *testing.T) {
	if got := string(appendPrefixedID(nil, 'R', math.MaxUint32)); got != "R4294967295" {
		t.Fatalf("prefixed ID = %q", got)
	}
	var hash [32]byte
	for i := range hash {
		hash[i] = byte(i)
	}
	if got := string(appendHash(nil, hash)); got != "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f" {
		t.Fatalf("hash = %q", got)
	}
}

func TestEncoderRequiresValidBind(t *testing.T) {
	prefix := []byte("prefix")
	var encoder Encoder
	got, err := encoder.Append(prefix, nil, nil, []byte(buildinfo.Version()))
	if err != ErrInvalidProgram || !bytes.Equal(got, prefix) {
		t.Fatalf("unbound Append = (%q,%v), want unchanged prefix and %v", got, err, ErrInvalidProgram)
	}
	if err := encoder.Bind(nil); err != ErrInvalidProgram {
		t.Fatalf("Bind(nil) = %v, want %v", err, ErrInvalidProgram)
	}
	var nilEncoder *Encoder
	if err := nilEncoder.Bind(&program.Program{}); err != ErrInvalidProgram {
		t.Fatalf("nil Bind = %v, want %v", err, ErrInvalidProgram)
	}
}

func TestEncoderMatchesGoldenDeterministically(t *testing.T) {
	fixture := loadEncodingFixture(t)
	var encoder Encoder
	if err := encoder.Bind(fixture.program); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	got, err := encoder.Append(nil, fixture.requestIDs, &fixture.batch, []byte(buildinfo.Version()))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !bytes.Equal(got, fixture.golden) {
		t.Fatalf("encoded output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, fixture.golden)
	}
	if err := encoder.Bind(fixture.program); err != nil {
		t.Fatalf("same-Program Bind: %v", err)
	}

	prefix := []byte("caller-prefix:")
	dst := make([]byte, len(prefix), len(prefix)+len(fixture.golden))
	copy(dst, prefix)
	got, err = encoder.Append(dst, fixture.requestIDs, &fixture.batch, []byte(buildinfo.Version()))
	if err != nil {
		t.Fatalf("reused Append: %v", err)
	}
	want := append(slices.Clone(prefix), fixture.golden...)
	if !bytes.Equal(got, want) {
		t.Fatalf("prefixed output mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestEncoderFailedBindPreservesPreviousProgram(t *testing.T) {
	fixture := loadEncodingFixture(t)
	var encoder Encoder
	if err := encoder.Bind(fixture.program); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	malformed := *fixture.program
	malformed.PolicyName = 0
	if err := encoder.Bind(&malformed); err != ErrInvalidProgram {
		t.Fatalf("Bind(malformed) = %v, want %v", err, ErrInvalidProgram)
	}
	got, err := encoder.Append(nil, fixture.requestIDs, &fixture.batch, []byte(buildinfo.Version()))
	if err != nil {
		t.Fatalf("Append after failed Bind: %v", err)
	}
	if !bytes.Equal(got, fixture.golden) {
		t.Fatal("failed Bind replaced the previously usable Program")
	}
}

func TestEncoderRejectsMalformedResultAtomically(t *testing.T) {
	fixture := loadEncodingFixture(t)
	var encoder Encoder
	if err := encoder.Bind(fixture.program); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	prefix := []byte("prefix")

	zeroRequestIDs := slices.Clone(fixture.requestIDs)
	zeroRequestIDs[0] = 0
	zeroDrivers := firstResultRow(&fixture.batch)
	zeroDrivers.DriverOffsets[1] = 0
	zeroDrivers.DriverRequirements = nil
	zeroDrivers.DriverClauses = nil
	zeroDrivers.DriverNodes = nil
	zeroDrivers.DriverReasons = nil
	zeroDrivers.DriverExplanations = nil
	multipleDrivers := firstResultRow(&fixture.batch)
	multipleDrivers.DriverOffsets[1] = 2
	multipleDrivers.DriverRequirements = append(multipleDrivers.DriverRequirements, multipleDrivers.DriverRequirements[0])
	multipleDrivers.DriverClauses = append(multipleDrivers.DriverClauses, multipleDrivers.DriverClauses[0])
	multipleDrivers.DriverNodes = append(multipleDrivers.DriverNodes, multipleDrivers.DriverNodes[0])
	multipleDrivers.DriverReasons = append(multipleDrivers.DriverReasons, multipleDrivers.DriverReasons[0])
	multipleDrivers.DriverExplanations = append(multipleDrivers.DriverExplanations, multipleDrivers.DriverExplanations[0])
	invalidProvenance := firstResultRow(&fixture.batch)
	invalidProvenance.DriverExplanations[0] = fixture.program.ClauseExplanationIDs[1]
	wrongClause := firstResultRow(&fixture.batch)
	wrongClause.DriverClauses[0] = 2

	tests := []struct {
		name       string
		requestIDs []schema.RequestID
		batch      *result.Batch
		version    []byte
	}{
		{"nil result", fixture.requestIDs, nil, []byte(buildinfo.Version())},
		{"short request IDs", fixture.requestIDs[:len(fixture.requestIDs)-1], &fixture.batch, []byte(buildinfo.Version())},
		{"zero request ID", zeroRequestIDs, &fixture.batch, []byte(buildinfo.Version())},
		{"zero drivers", fixture.requestIDs[:1], &zeroDrivers, []byte(buildinfo.Version())},
		{"multiple drivers", fixture.requestIDs[:1], &multipleDrivers, []byte(buildinfo.Version())},
		{"invalid provenance", fixture.requestIDs[:1], &invalidProvenance, []byte(buildinfo.Version())},
		{"clause outside requirement", fixture.requestIDs[:1], &wrongClause, []byte(buildinfo.Version())},
		{"empty engine version", fixture.requestIDs, &fixture.batch, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := slices.Clone(prefix)
			got, err := encoder.Append(dst, tt.requestIDs, tt.batch, tt.version)
			if err != ErrInvalidResult {
				t.Fatalf("Append error = %v, want %v", err, ErrInvalidResult)
			}
			if !bytes.Equal(got, prefix) {
				t.Fatalf("Append destination = %q, want unchanged %q", got, prefix)
			}
		})
	}
	got, err := encoder.Append(nil, fixture.requestIDs, &fixture.batch, []byte(buildinfo.Version()))
	if err != nil {
		t.Fatalf("valid Append after failures: %v", err)
	}
	if !bytes.Equal(got, fixture.golden) {
		t.Fatal("failed Append left Encoder unusable")
	}
}

func TestEncoderEncodesEmptyResult(t *testing.T) {
	fixture := loadEncodingFixture(t)
	var encoder Encoder
	if err := encoder.Bind(fixture.program); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	var empty result.Batch
	if err := empty.Reset(0); err != nil {
		t.Fatal(err)
	}
	got, err := encoder.Append(nil, nil, &empty, []byte(buildinfo.Version()))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	want := []byte("{\n  \"schema_version\": 1,\n  \"policy\": {\n    \"name\": \"verifoxx\",\n    \"version\": \"1.0.0\",\n    \"sha256\": \"a92ffd1c00e823652bed47acf3955f5559543eeba4f02ebf16965bc2966d0a22\"\n  },\n  \"engine_version\": \"devel\",\n  \"results\": []\n}\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("empty result mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestEncoderRendersEveryDriverReason(t *testing.T) {
	fixture := loadEncodingFixture(t)
	var encoder Encoder
	if err := encoder.Bind(fixture.program); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	for reason := truth.ReasonMissing; reason <= truth.ReasonConflict; reason++ {
		name, _ := result.ReasonName(reason)
		t.Run(name, func(t *testing.T) {
			batch := firstResultRow(&fixture.batch)
			resolutionRow := int(reason - 1)
			batch.OutcomeIDs[0] = fixture.program.Resolutions.OutcomeIDs[resolutionRow]
			batch.DriverReasons[0] = reason
			batch.DriverNodes[0] = fixture.program.ClauseAssertionSourceNodeIDs[0]
			batch.DriverExplanations[0] = fixture.program.Resolutions.ExplanationIDs[resolutionRow]
			batch.EvidenceOffsets = []uint32{0, 0}
			batch.EvidenceIDs = nil
			batch.ReasonOffsets = []uint32{0, 1}
			batch.ReasonIDs = []schema.ReasonID{reason}
			batch.ReasonNodes = []schema.NodeID{batch.DriverNodes[0]}
			batch.ReasonEvidenceIDs = []schema.EvidenceID{0}
			batch.ReasonEvidenceStates = []schema.EvidenceStateID{0}
			batch.RemediationOffsets = []uint32{0, 0}
			batch.RemediationIDs = nil

			got, err := encoder.Append(nil, fixture.requestIDs[:1], &batch, []byte(buildinfo.Version()))
			if err != nil {
				t.Fatalf("Append: %v", err)
			}
			want := []byte(`"reason": "` + name + `"`)
			if !bytes.Contains(got, want) {
				t.Fatalf("encoded result does not contain %q:\n%s", want, got)
			}
		})
	}
}

func TestEncoderRendersTypedRemediations(t *testing.T) {
	fixture := loadEncodingFixture(t)
	compiled, remediationIDs := withTypedRemediations(fixture.program)
	batch := firstResultRow(&fixture.batch)
	batch.RemediationOffsets = []uint32{0, uint32(len(remediationIDs))}
	batch.RemediationIDs = remediationIDs
	var encoder Encoder
	if err := encoder.Bind(fixture.program); err != nil {
		t.Fatalf("Bind first Program: %v", err)
	}
	if _, err := encoder.Append(nil, fixture.requestIDs, &fixture.batch, []byte(buildinfo.Version())); err != nil {
		t.Fatalf("prime first Program: %v", err)
	}
	if err := encoder.Bind(compiled); err != nil {
		t.Fatalf("Bind second Program: %v", err)
	}
	got, err := encoder.Append(nil, fixture.requestIDs[:1], &batch, []byte(buildinfo.Version()))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	want := []byte(`      "remediation": [
        {
          "action": "set_field",
          "field": "usage_symbol",
          "value": "standard"
        },
        {
          "action": "set_field",
          "field": "usage_integer",
          "value": -42
        },
        {
          "action": "set_field",
          "field": "usage_boolean",
          "value": true
        },
        {
          "action": "set_field",
          "field": "usage_timestamp",
          "value": 1700000000
        },
        {
          "action": "add_evidence",
          "evidence_kind": "approval_record"
        }
      ]`)
	if !bytes.Contains(got, want) {
		t.Fatalf("typed remediation mismatch\n--- got ---\n%s\n--- want fragment ---\n%s", got, want)
	}
}

func TestEncoderRollsBackAfterLateRowFailure(t *testing.T) {
	fixture := loadEncodingFixture(t)
	malformed := fixture.batch
	malformed.DriverExplanations = slices.Clone(fixture.batch.DriverExplanations)
	driver := malformed.DriverOffsets[1]
	clauseRow := int(malformed.DriverClauses[driver] - 1)
	malformed.DriverExplanations[driver] = fixture.program.ClauseExplanationIDs[clauseRow*clauseExplanationBranchCount]

	var encoder Encoder
	if err := encoder.Bind(fixture.program); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	prefix := []byte("preserve-this-prefix")
	dst := make([]byte, len(prefix), len(prefix)+len(fixture.golden))
	copy(dst, prefix)
	got, err := encoder.Append(dst, fixture.requestIDs, &malformed, []byte(buildinfo.Version()))
	if err != ErrInvalidResult {
		t.Fatalf("late Append error = %v, want %v", err, ErrInvalidResult)
	}
	if !bytes.Equal(got, prefix) {
		t.Fatalf("late failure returned %q, want %q", got, prefix)
	}
	got, err = encoder.Append(dst[:len(prefix)], fixture.requestIDs, &fixture.batch, []byte(buildinfo.Version()))
	if err != nil {
		t.Fatalf("valid Append after late failure: %v", err)
	}
	if !bytes.Equal(got[len(prefix):], fixture.golden) {
		t.Fatal("late failure left Encoder unusable")
	}
}

func TestEncoderRendersMaximumRequestID(t *testing.T) {
	fixture := loadEncodingFixture(t)
	batch := firstResultRow(&fixture.batch)
	var encoder Encoder
	if err := encoder.Bind(fixture.program); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	got, err := encoder.Append(nil, []schema.RequestID{math.MaxUint32}, &batch, []byte(buildinfo.Version()))
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if !bytes.Contains(got, []byte(`"request_id": "R4294967295"`)) {
		t.Fatalf("maximum request ID missing:\n%s", got)
	}
}

func TestEncoderOverwritesPoisonedScratch(t *testing.T) {
	fixture := loadEncodingFixture(t)
	var reused Encoder
	if err := reused.Bind(fixture.program); err != nil {
		t.Fatalf("Bind reused: %v", err)
	}
	if _, err := reused.Append(make([]byte, 0, len(fixture.golden)), fixture.requestIDs, &fixture.batch, []byte(buildinfo.Version())); err != nil {
		t.Fatalf("prime reused Encoder: %v", err)
	}
	poisonBytes(reused.materialized.Bytes)
	reused.materialized.Outcome = []byte("poison-outcome")
	poisonRanges(reused.materialized.EvidenceIssues)
	poisonRanges(reused.materialized.Assumptions)
	poisonRanges(reused.materialized.Uncertainty)
	poisonRemediations(reused.materialized.Remediations)
	reused.materialized.Rationale = result.TextRange{Start: math.MaxUint32, End: math.MaxUint32}
	reused.materialized.DriverRequirementRow = math.MaxUint32
	reused.materialized.Requirements = []schema.RequirementID{math.MaxUint32}
	reused.materialized.Evidence = []schema.EvidenceID{math.MaxUint32}

	got, err := reused.Append(make([]byte, 0, len(fixture.golden)), fixture.requestIDs, &fixture.batch, []byte(buildinfo.Version()))
	if err != nil {
		t.Fatalf("Append reused: %v", err)
	}
	var fresh Encoder
	if err := fresh.Bind(fixture.program); err != nil {
		t.Fatalf("Bind fresh: %v", err)
	}
	want, err := fresh.Append(make([]byte, 0, len(fixture.golden)), fixture.requestIDs, &fixture.batch, []byte(buildinfo.Version()))
	if err != nil {
		t.Fatalf("Append fresh: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("poisoned reuse differs from fresh output\n--- reused ---\n%s\n--- fresh ---\n%s", got, want)
	}
	if !bytes.Equal(got, fixture.golden) {
		t.Fatal("poisoned reuse differs from golden output")
	}
}

func TestEncoderWarmAppendAllocatesNothing(t *testing.T) {
	fixture := loadEncodingFixture(t)
	var encoder Encoder
	if err := encoder.Bind(fixture.program); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	dst := make([]byte, 0, len(fixture.golden))
	version := []byte(buildinfo.Version())
	if _, err := encoder.Append(dst, fixture.requestIDs, &fixture.batch, version); err != nil {
		t.Fatalf("prime Append: %v", err)
	}
	var encoded []byte
	var encodeErr error
	allocations := testing.AllocsPerRun(100, func() {
		encoded, encodeErr = encoder.Append(dst[:0], fixture.requestIDs, &fixture.batch, version)
	})
	if encodeErr != nil {
		t.Fatalf("Append: %v", encodeErr)
	}
	if allocations != 0 {
		t.Fatalf("warm allocations = %v, want 0", allocations)
	}
	if !bytes.Equal(encoded, fixture.golden) {
		t.Fatal("warm encoded output differs from golden")
	}
}

func loadEncodingFixture(tb testing.TB) encodingFixture {
	tb.Helper()
	fields, symbols := encodingSchema(tb)
	policySource, err := os.ReadFile("../../../policies/verifoxx/policy.json")
	if err != nil {
		tb.Fatal(err)
	}
	builder := ast.NewBuilder(ast.Hints{
		Nodes: 48, CompareNodes: 32, GroupNodes: 12, ChildEdges: 48, EvidenceNodes: 8,
		Values: 96, SymbolValues: 96, SymbolBytes: 4096, EvidenceKinds: 8, EvidenceStates: 16,
		Outcomes: 8, Remediations: 4, Clauses: 8, ClauseEvidenceEdges: 8,
		ClauseRemediationEdges: 4, Requirements: 4, RequirementClauseEdges: 8, SourceBytes: len(policySource),
	})
	if err := jsonpolicy.Decode(builder, policySource, fields, symbols, jsonpolicy.Limits{}); err != nil {
		tb.Fatalf("decode policy: %v", err)
	}
	compiled, err := compile.Lower(builder.Document(), fields, symbols)
	if err != nil {
		tb.Fatalf("compile policy: %v", err)
	}
	var batchBuilder eval.Builder
	requests, err := jsonbatch.Decode(&batchBuilder, compiled, []byte(fixtures.RequestsJSON()), []byte(fixtures.EvidenceJSON()), jsonbatch.Limits{})
	if err != nil {
		tb.Fatalf("decode fixtures: %v", err)
	}
	var decisions result.Batch
	var executor eval.Executor
	if err := executor.Execute(&decisions, compiled, requests); err != nil {
		tb.Fatalf("execute policy: %v", err)
	}
	golden, err := os.ReadFile("../../../testdata/golden/requests.json")
	if err != nil {
		tb.Fatal(err)
	}
	return encodingFixture{program: compiled, requestIDs: requests.RequestIDs, batch: decisions, golden: golden}
}

func encodingSchema(tb testing.TB) (*schema.Schema, *schema.Interner) {
	tb.Helper()
	symbols := schema.NewSymbolInterner(16)
	builder := schema.NewBuilder()
	fields := []struct {
		name  string
		group schema.FieldGroup
	}{
		{"requester.team", schema.FieldGroupSubject},
		{"requester.trust", schema.FieldGroupSubject},
		{"action.type", schema.FieldGroupAction},
		{"action.output", schema.FieldGroupOutput},
		{"action.dataset", schema.FieldGroupResource},
		{"environment.execution_env", schema.FieldGroupContext},
		{"environment.usage", schema.FieldGroupContext},
	}
	for _, field := range fields {
		name, err := symbols.Intern([]byte(field.name))
		if err != nil {
			tb.Fatal(err)
		}
		if _, err := builder.AddField(name, schema.ValueKindSymbol, field.group); err != nil {
			tb.Fatal(err)
		}
	}
	return builder.Finish(), symbols
}

func firstResultRow(source *result.Batch) result.Batch {
	requirementEnd := int(source.RequirementOffsets[1])
	driverEnd := int(source.DriverOffsets[1])
	evidenceEnd := int(source.EvidenceOffsets[1])
	reasonEnd := int(source.ReasonOffsets[1])
	remediationEnd := int(source.RemediationOffsets[1])
	return result.Batch{
		OutcomeIDs: slices.Clone(source.OutcomeIDs[:1]),

		RequirementOffsets: []uint32{0, uint32(requirementEnd)},
		RequirementIDs:     slices.Clone(source.RequirementIDs[:requirementEnd]),

		DriverOffsets:      []uint32{0, uint32(driverEnd)},
		DriverRequirements: slices.Clone(source.DriverRequirements[:driverEnd]),
		DriverClauses:      slices.Clone(source.DriverClauses[:driverEnd]),
		DriverNodes:        slices.Clone(source.DriverNodes[:driverEnd]),
		DriverReasons:      slices.Clone(source.DriverReasons[:driverEnd]),
		DriverExplanations: slices.Clone(source.DriverExplanations[:driverEnd]),

		EvidenceOffsets:      []uint32{0, uint32(evidenceEnd)},
		EvidenceIDs:          slices.Clone(source.EvidenceIDs[:evidenceEnd]),
		ReasonOffsets:        []uint32{0, uint32(reasonEnd)},
		ReasonIDs:            slices.Clone(source.ReasonIDs[:reasonEnd]),
		ReasonNodes:          slices.Clone(source.ReasonNodes[:reasonEnd]),
		ReasonEvidenceIDs:    slices.Clone(source.ReasonEvidenceIDs[:reasonEnd]),
		ReasonEvidenceStates: slices.Clone(source.ReasonEvidenceStates[:reasonEnd]),

		RemediationOffsets: []uint32{0, uint32(remediationEnd)},
		RemediationIDs:     slices.Clone(source.RemediationIDs[:remediationEnd]),
		Rows:               1,
	}
}

func withTypedRemediations(source *program.Program) (*program.Program, []schema.RemediationID) {
	compiled := *source
	compiled.SymbolBytes = slices.Clone(source.SymbolBytes)
	compiled.SymbolStarts = slices.Clone(source.SymbolStarts)
	compiled.SymbolLengths = slices.Clone(source.SymbolLengths)
	compiled.FieldNames = slices.Clone(source.FieldNames)
	compiled.FieldKinds = slices.Clone(source.FieldKinds)
	compiled.FieldGroups = slices.Clone(source.FieldGroups)
	compiled.ValueKinds = slices.Clone(source.ValueKinds)
	compiled.ValueRefs = slices.Clone(source.ValueRefs)
	compiled.IntegerValues = slices.Clone(source.IntegerValues)
	compiled.BooleanValues = slices.Clone(source.BooleanValues)
	compiled.TimestampValues = slices.Clone(source.TimestampValues)
	compiled.Remediations = result.RemediationTable{
		Kinds:         slices.Clone(source.Remediations.Kinds),
		Fields:        slices.Clone(source.Remediations.Fields),
		Values:        slices.Clone(source.Remediations.Values),
		EvidenceKinds: slices.Clone(source.Remediations.EvidenceKinds),
	}

	fieldNames := [...]string{"usage_symbol", "usage_integer", "usage_boolean", "usage_timestamp"}
	fieldKinds := [...]schema.ValueKind{
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
		schema.ValueKindBoolean,
		schema.ValueKindTimestamp,
	}
	fields := make([]schema.FieldID, len(fieldNames))
	for row, name := range fieldNames {
		compiled.FieldNames = append(compiled.FieldNames, appendProgramSymbol(&compiled, name))
		compiled.FieldKinds = append(compiled.FieldKinds, fieldKinds[row])
		compiled.FieldGroups = append(compiled.FieldGroups, schema.FieldGroupContext)
		fields[row] = schema.FieldID(len(compiled.FieldNames))
	}
	valueSymbol := appendProgramSymbol(&compiled, "standard")
	compiled.IntegerValues = append(compiled.IntegerValues, -42)
	compiled.BooleanValues = append(compiled.BooleanValues, 1)
	compiled.TimestampValues = append(compiled.TimestampValues, 1_700_000_000)
	valueRefs := [...]uint32{
		uint32(valueSymbol),
		uint32(len(compiled.IntegerValues)),
		uint32(len(compiled.BooleanValues)),
		uint32(len(compiled.TimestampValues)),
	}
	values := make([]schema.ValueID, len(fieldNames))
	for row, kind := range fieldKinds {
		compiled.ValueKinds = append(compiled.ValueKinds, kind)
		compiled.ValueRefs = append(compiled.ValueRefs, valueRefs[row])
		values[row] = schema.ValueID(len(compiled.ValueKinds))
	}

	ids := make([]schema.RemediationID, 0, len(fields)+1)
	for row := range fields {
		compiled.Remediations.Kinds = append(compiled.Remediations.Kinds, result.RemediationSetField)
		compiled.Remediations.Fields = append(compiled.Remediations.Fields, fields[row])
		compiled.Remediations.Values = append(compiled.Remediations.Values, values[row])
		compiled.Remediations.EvidenceKinds = append(compiled.Remediations.EvidenceKinds, 0)
		ids = append(ids, schema.RemediationID(len(compiled.Remediations.Kinds)))
	}
	compiled.Remediations.Kinds = append(compiled.Remediations.Kinds, result.RemediationAddEvidence)
	compiled.Remediations.Fields = append(compiled.Remediations.Fields, 0)
	compiled.Remediations.Values = append(compiled.Remediations.Values, 0)
	compiled.Remediations.EvidenceKinds = append(compiled.Remediations.EvidenceKinds, 1)
	ids = append(ids, schema.RemediationID(len(compiled.Remediations.Kinds)))
	return &compiled, ids
}

func appendProgramSymbol(compiled *program.Program, value string) schema.SymbolID {
	compiled.SymbolStarts = append(compiled.SymbolStarts, uint32(len(compiled.SymbolBytes)))
	compiled.SymbolLengths = append(compiled.SymbolLengths, uint32(len(value)))
	compiled.SymbolBytes = append(compiled.SymbolBytes, value...)
	compiled.ProgramSymbolCount = uint32(len(compiled.SymbolStarts))
	return schema.SymbolID(compiled.ProgramSymbolCount)
}

func poisonBytes(values []byte) {
	values = values[:cap(values)]
	for row := range values {
		values[row] = 0xa5
	}
}

func poisonRanges(values []result.TextRange) {
	values = values[:cap(values)]
	for row := range values {
		values[row] = result.TextRange{Start: math.MaxUint32, End: math.MaxUint32}
	}
}

func poisonRemediations(values []result.RenderedRemediation) {
	values = values[:cap(values)]
	for row := range values {
		values[row] = result.RenderedRemediation{
			FieldName:        result.TextRange{Start: math.MaxUint32, End: math.MaxUint32},
			Value:            result.TextRange{Start: math.MaxUint32, End: math.MaxUint32},
			EvidenceKindName: result.TextRange{Start: math.MaxUint32, End: math.MaxUint32},
			Kind:             result.RemediationInvalid,
			ValueKind:        schema.ValueKindInvalid,
		}
	}
}
