package jsonpolicy

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"math"
	"os"
	"reflect"
	"testing"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func readPolicyFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../../testdata/policies/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func policyBuilder(sourceLen int) *ast.Builder {
	return ast.NewBuilder(ast.Hints{
		Nodes: 64, CompareNodes: 32, CompareListValues: 16, GroupNodes: 16,
		ChildEdges: 32, NotNodes: 8, EvidenceNodes: 16,
		Values: 64, SymbolValues: 48, SymbolBytes: 2048, IntegerValues: 16,
		BooleanValues: 8, TimestampValues: 8, EvidenceKinds: 16, EvidenceStates: 16,
		Outcomes: 8, Remediations: 16, Clauses: 16, ClauseEvidenceEdges: 16,
		ClauseRemediationEdges: 16, Requirements: 16, RequirementClauseEdges: 16,
		SourceBytes: sourceLen,
	})
}

func decodePolicy(t *testing.T, source []byte, limits Limits) *ast.Builder {
	t.Helper()
	b := policyBuilder(len(source))
	if err := Decode(b, source, testSchema(t), testInterner(t), limits); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	return b
}

func TestDecodeFullPolicyFixture(t *testing.T) {
	source := readPolicyFixture(t, "valid-full.json")
	b := decodePolicy(t, source, Limits{})
	d := b.Document()
	metadata, ok := d.PolicyMetadata()
	if !ok || metadata.ContentHash != sha256.Sum256(source) {
		t.Fatalf("metadata = (%+v, %v)", metadata, ok)
	}
	root, ok := d.RequirementRoot(1)
	if !ok {
		t.Fatal("requirement R1 missing")
	}
	field, op, value, ok := d.Compare(root)
	if !ok || field != fieldSubjectTrust || op != ast.CompareOpEqual {
		t.Fatalf("applicability = (%d, %v, %d, %v)", field, op, value, ok)
	}
	if got, _ := d.SymbolValue(value); string(got) != "external" {
		t.Fatalf("applicability value = %q", got)
	}
	clauses, ok := d.RequirementClauses(1)
	if !ok || len(clauses) != 1 {
		t.Fatalf("RequirementClauses = (%v, %v)", clauses, ok)
	}
	assertion, resolution, ok := d.Clause(clauses[0])
	if !ok || resolution != (ast.Resolution{
		OnSatisfied: 1, OnFalse: 2, OnMissing: 4, OnStale: 4,
		OnUnclear: 4, OnUnverifiable: 4, OnConflict: 4,
	}) {
		t.Fatalf("Clause = (%d, %+v, %v)", assertion, resolution, ok)
	}
	if kind, _ := d.Kind(assertion); kind != ast.NodeKindAll {
		t.Fatalf("assertion kind = %v, want all", kind)
	}
	evidence, ok := d.ClauseEvidence(clauses[0])
	if !ok || len(evidence) != 1 {
		t.Fatalf("ClauseEvidence = (%v, %v)", evidence, ok)
	}
	if kind, state, ok := d.Evidence(evidence[0]); !ok || kind != 1 || state != 1 {
		t.Fatalf("Evidence = (%d, %d, %v)", kind, state, ok)
	}
	remediations, ok := d.ClauseRemediations(clauses[0])
	if !ok || len(remediations) != 2 {
		t.Fatalf("ClauseRemediations = (%v, %v)", remediations, ok)
	}
	kind, fieldID, valueID, evidenceKind, ok := d.Remediation(remediations[0])
	if !ok || kind != ast.RemediationKindSetField || fieldID != fieldContextUsage || evidenceKind != 0 {
		t.Fatalf("set remediation = (%v, %d, %d, %d, %v)", kind, fieldID, valueID, evidenceKind, ok)
	}
	if got, _ := d.SymbolValue(valueID); string(got) != "standard" {
		t.Fatalf("set remediation value = %q", got)
	}
	kind, fieldID, valueID, evidenceKind, ok = d.Remediation(remediations[1])
	if !ok || kind != ast.RemediationKindAddEvidence || fieldID != 0 || valueID != 0 || evidenceKind != 2 {
		t.Fatalf("evidence remediation = (%v, %d, %d, %d, %v)", kind, fieldID, valueID, evidenceKind, ok)
	}
	for _, id := range remediations {
		span, ok := d.RemediationSpan(id)
		if !ok || span.Start >= span.End || source[span.Start] != '{' || source[span.End-1] != '}' {
			t.Fatalf("RemediationSpan(%d) = (%+v, %v)", id, span, ok)
		}
	}
	clauseSpan, ok := d.ClauseSpan(clauses[0])
	if !ok || source[clauseSpan.Start] != '{' || source[clauseSpan.End-1] != '}' {
		t.Fatalf("ClauseSpan = (%+v, %v)", clauseSpan, ok)
	}
	quoted := []byte(`"External requests require approval."`)
	start := bytes.Index(source, quoted)
	span, ok := d.RequirementSpan(1)
	if !ok || span != (ast.SourceSpan{Start: uint32(start), End: uint32(start + len(quoted))}) {
		t.Fatalf("RequirementSpan = (%+v, %v)", span, ok)
	}
}

const outcomeCatalog = `"outcomes":[{"name":"Approve","precedence":1,"terminal":true},{"name":"Reject","precedence":2,"terminal":true},{"name":"Revise","precedence":3,"terminal":false},{"name":"Escalate","precedence":4,"terminal":true}]`

func rootWithRequirements(requirements string) string {
	return `{"schema_version":1,"name":"p","version":"1",` +
		`"evidence_kinds":[{"name":"approval_record"},{"name":"usage_adjustment"}],` +
		`"evidence_states":[{"name":"current"}],` + outcomeCatalog + `,"requirements":` + requirements + `}`
}

func minimalClause() string {
	return `{"assert":{"op":"exists","field":"context.environment"},` +
		`"evidence":[],"resolution":{"satisfied":"Approve","false":"Reject",` +
		`"missing":"Escalate","stale":"Escalate","unclear":"Escalate",` +
		`"unverifiable":"Escalate","conflict":"Escalate"},"remediations":[]}`
}

func minimalRequirement(id string) string {
	return `{"id":"` + id + `","source":"source","applies":{"op":"exists","field":"context.environment"},"clauses":[` + minimalClause() + `]}`
}

func rejectPolicy(t *testing.T, source string, limits Limits, code ErrorCode) *Error {
	t.Helper()
	b := policyBuilder(len(source))
	err := Decode(b, []byte(source), testSchema(t), testInterner(t), limits)
	var decodeErr *Error
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error = %v, want *Error", err)
	}
	if decodeErr.Code != code {
		t.Fatalf("code = %s, want %s (%v)", decodeErr.Code, code, err)
	}
	if b.Len() != 0 || len(b.Document().RequirementIDs) != 0 {
		t.Fatalf("failed decode left AST content")
	}
	return decodeErr
}

func TestDecodeTwoRequirementsAndRejectDuplicate(t *testing.T) {
	source := rootWithRequirements(`[` + minimalRequirement("R1") + `,` + minimalRequirement("R2") + `]`)
	b := decodePolicy(t, []byte(source), Limits{})
	if _, ok := b.Document().RequirementRoot(1); !ok {
		t.Fatal("R1 missing")
	}
	if _, ok := b.Document().RequirementRoot(2); !ok {
		t.Fatal("R2 missing")
	}
	duplicate := rootWithRequirements(`[` + minimalRequirement("R1") + `,` + minimalRequirement("R1") + `]`)
	rejectPolicy(t, duplicate, Limits{}, CodeDuplicateID)
}

func TestDecodeRequirementKeyPermutations(t *testing.T) {
	resolution := `{"conflict":"Escalate","unverifiable":"Escalate","unclear":"Escalate","stale":"Escalate","missing":"Escalate","false":"Reject","satisfied":"Approve"}`
	remediation := `{"value":"standard","field":"context.usage","kind":"set_field"},{"evidence_kind":"usage_adjustment","kind":"add_evidence"}`
	clause := `{"remediations":[` + remediation + `],"resolution":` + resolution + `,"evidence":[],"assert":{"field":"context.environment","op":"exists"}}`
	requirement := `{"clauses":[` + clause + `],"applies":{"field":"context.environment","op":"exists"},"source":"source","id":"R1"}`
	b := decodePolicy(t, []byte(rootWithRequirements(`[`+requirement+`]`)), Limits{})
	clauses, ok := b.Document().RequirementClauses(1)
	if !ok || len(clauses) != 1 {
		t.Fatalf("RequirementClauses = (%v, %v)", clauses, ok)
	}
	remediations, _ := b.Document().ClauseRemediations(clauses[0])
	if len(remediations) != 2 {
		t.Fatalf("remediations = %v", remediations)
	}
}

func TestDecodeRequirementRejects(t *testing.T) {
	valid := minimalRequirement("R1")
	tests := []struct {
		name string
		reqs string
		code ErrorCode
	}{
		{"bad id prefix", `[` + minimalRequirement("X1") + `]`, CodeMalformed},
		{"zero id", `[` + minimalRequirement("R0") + `]`, CodeMalformed},
		{"leading zero", `[` + minimalRequirement("R01") + `]`, CodeMalformed},
		{"id overflow", `[` + minimalRequirement("R4294967296") + `]`, CodeLimit},
		{"empty clauses", `[{"id":"R1","source":"s","applies":{"op":"exists","field":"context.environment"},"clauses":[]}]`, CodeInvalidArity},
		{"non evidence evidence", `[{"id":"R1","source":"s","applies":{"op":"exists","field":"context.environment"},"clauses":[{"assert":{"op":"exists","field":"context.environment"},"evidence":[{"op":"exists","field":"context.environment"}],"resolution":{"satisfied":"Approve","false":"Reject","missing":"Escalate","stale":"Escalate","unclear":"Escalate","unverifiable":"Escalate","conflict":"Escalate"},"remediations":[]}]}]`, CodeInvalidType},
		{"unknown outcome", `[` + bytesReplace(valid, `"satisfied":"Approve"`, `"satisfied":"Unknown"`) + `]`, CodeInvalidReference},
		{"unknown field remediation", `[` + bytesReplace(valid, `"remediations":[]`, `"remediations":[{"kind":"set_field","field":"no.field","value":"x"}]`) + `]`, CodeInvalidReference},
		{"unknown evidence remediation", `[` + bytesReplace(valid, `"remediations":[]`, `"remediations":[{"kind":"add_evidence","evidence_kind":"unknown"}]`) + `]`, CodeInvalidReference},
		{"missing requirement key", `[{"id":"R1","source":"s","clauses":[` + minimalClause() + `]}]`, CodeMissingKey},
		{"unknown requirement key", `[` + bytesReplace(valid, `"clauses":`, `"extra":1,"clauses":`) + `]`, CodeUnknownKey},
		{"duplicate requirement key", `[` + bytesReplace(valid, `"source":"source"`, `"source":"source","source":"again"`) + `]`, CodeDuplicateKey},
		{"missing clause key", `[{"id":"R1","source":"s","applies":{"op":"exists","field":"context.environment"},"clauses":[{"assert":{"op":"exists","field":"context.environment"},"evidence":[],"resolution":{"satisfied":"Approve","false":"Reject","missing":"Escalate","stale":"Escalate","unclear":"Escalate","unverifiable":"Escalate","conflict":"Escalate"}}]}]`, CodeMissingKey},
		{"unknown clause key", `[` + bytesReplace(valid, `"evidence":[]`, `"evidence":[],"extra":1`) + `]`, CodeUnknownKey},
		{"missing resolution key", `[` + bytesReplace(valid, `,"conflict":"Escalate"`, ``) + `]`, CodeMissingKey},
		{"duplicate resolution key", `[` + bytesReplace(valid, `"satisfied":"Approve"`, `"satisfied":"Approve","satisfied":"Approve"`) + `]`, CodeDuplicateKey},
		{"bad remediation kind", `[` + bytesReplace(valid, `"remediations":[]`, `"remediations":[{"kind":"other"}]`) + `]`, CodeMalformed},
		{"wrong remediation keys", `[` + bytesReplace(valid, `"remediations":[]`, `"remediations":[{"kind":"add_evidence","field":"context.usage"}]`) + `]`, CodeInvalidArity},
		{"missing remediation kind", `[` + bytesReplace(valid, `"remediations":[]`, `"remediations":[{"evidence_kind":"usage_adjustment"}]`) + `]`, CodeMissingKey},
		{"duplicate remediation key", `[` + bytesReplace(valid, `"remediations":[]`, `"remediations":[{"kind":"add_evidence","kind":"add_evidence","evidence_kind":"usage_adjustment"}]`) + `]`, CodeDuplicateKey},
		{"unknown remediation key", `[` + bytesReplace(valid, `"remediations":[]`, `"remediations":[{"kind":"add_evidence","evidence_kind":"usage_adjustment","extra":1}]`) + `]`, CodeUnknownKey},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rejectPolicy(t, rootWithRequirements(tc.reqs), Limits{}, tc.code)
		})
	}
}

func bytesReplace(source, old, replacement string) string {
	return string(bytes.Replace([]byte(source), []byte(old), []byte(replacement), 1))
}

func TestDecodeRequirementLimits(t *testing.T) {
	two := `[` + minimalRequirement("R1") + `,` + minimalRequirement("R2") + `]`
	rejectPolicy(t, rootWithRequirements(two), Limits{MaxRequirements: 1}, CodeLimit)
	rejectPolicy(t, rootWithRequirements(two), Limits{MaxArrayItems: 1}, CodeLimit)
	requirement := `[{"id":"R1","source":"s","applies":{"op":"exists","field":"context.environment"},"clauses":[` + minimalClause() + `,` + minimalClause() + `]}]`
	rejectPolicy(t, rootWithRequirements(requirement), Limits{MaxClauses: 1}, CodeLimit)
	fixture := readPolicyFixture(t, "valid-full.json")
	rejectPolicy(t, string(fixture), Limits{MaxValues: 17}, CodeLimit)
	rejectPolicy(t, string(fixture), Limits{MaxSymbolBytes: 140}, CodeLimit)
}

func TestDecodeRequirementsBeforeCatalogs(t *testing.T) {
	source := `{"schema_version":1,"name":"p","version":"1","requirements":[` + minimalRequirement("R1") + `],"evidence_kinds":[],"evidence_states":[],` + outcomeCatalog + `}`
	rejectPolicy(t, source, Limits{}, CodeInvalidReference)
}

func TestDecodeRequirementNilDependencies(t *testing.T) {
	source := []byte(rootWithRequirements(`[` + minimalRequirement("R1") + `]`))
	for _, tc := range []struct {
		name    string
		fields  *schema.Schema
		symbols *schema.Interner
	}{
		{"nil fields", nil, testInterner(t)},
		{"nil symbols", testSchema(t), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := policyBuilder(len(source))
			err := Decode(b, source, tc.fields, tc.symbols, Limits{})
			var decodeErr *Error
			if !errors.As(err, &decodeErr) || decodeErr.Code != CodeInvalidReference {
				t.Fatalf("error = %v, want invalid reference", err)
			}
		})
	}
}

func TestDecodeRequirementRollbackAndMalformedFixture(t *testing.T) {
	valid := readPolicyFixture(t, "valid-full.json")
	b := policyBuilder(len(valid))
	if err := Decode(b, valid, testSchema(t), testInterner(t), Limits{}); err != nil {
		t.Fatal(err)
	}
	bad := readPolicyFixture(t, "malformed-truncated.json")
	err := Decode(b, bad, testSchema(t), testInterner(t), Limits{})
	var decodeErr *Error
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error = %v, want *Error", err)
	}
	if b.Len() != 0 || len(b.Document().RequirementIDs) != 0 {
		t.Fatal("failed decode did not roll back")
	}
}

func TestDecodeRequirementDeterministicAndInternerReadOnly(t *testing.T) {
	source := readPolicyFixture(t, "valid-full.json")
	fields := testSchema(t)
	symbols := testInterner(t)
	beforeLen, beforeBytes := symbols.Len(), symbols.ByteLen()
	b1, b2 := policyBuilder(len(source)), policyBuilder(len(source))
	if err := Decode(b1, source, fields, symbols, Limits{}); err != nil {
		t.Fatal(err)
	}
	if err := Decode(b2, source, fields, symbols, Limits{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*b1.Document(), *b2.Document()) {
		t.Fatal("repeated decode produced different AST documents")
	}
	if symbols.Len() != beforeLen || symbols.ByteLen() != beforeBytes {
		t.Fatalf("shared interner changed: (%d, %d) -> (%d, %d)", beforeLen, beforeBytes, symbols.Len(), symbols.ByteLen())
	}
}

func TestDecodeRemediationMaskCheckedBeforeFieldResolution(t *testing.T) {
	valid := minimalRequirement("R1")
	tests := []struct {
		name string
		reqs string
	}{
		{"add_evidence with unknown field", `[` + bytesReplace(valid, `"remediations":[]`, `"remediations":[{"kind":"add_evidence","evidence_kind":"usage_adjustment","field":"no.field"}]`) + `]`},
		{"set_field with unknown evidence kind", `[` + bytesReplace(valid, `"remediations":[]`, `"remediations":[{"kind":"set_field","field":"context.usage","value":"standard","evidence_kind":"unknown"}]`) + `]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rejectPolicy(t, rootWithRequirements(tc.reqs), Limits{}, CodeInvalidArity)
		})
	}
}

func TestDecodeRemediationFieldTokenReparseWithShuffledKeys(t *testing.T) {
	valid := minimalRequirement("R1")
	shuffled := `[` + bytesReplace(valid, `"remediations":[]`, `"remediations":[{"value":"standard","field":"context.usage","kind":"set_field"}]`) + `]`
	b := decodePolicy(t, []byte(rootWithRequirements(shuffled)), Limits{})
	clauses, ok := b.Document().RequirementClauses(1)
	if !ok || len(clauses) != 1 {
		t.Fatalf("RequirementClauses = (%v, %v)", clauses, ok)
	}
	remediations, ok := b.Document().ClauseRemediations(clauses[0])
	if !ok || len(remediations) != 1 {
		t.Fatalf("ClauseRemediations = (%v, %v)", remediations, ok)
	}
	kind, fieldID, valueID, _, ok := b.Document().Remediation(remediations[0])
	if !ok || kind != ast.RemediationKindSetField || fieldID != fieldContextUsage {
		t.Fatalf("set remediation = (%v, %d, %d, %v)", kind, fieldID, valueID, ok)
	}
	if got, _ := b.Document().SymbolValue(valueID); string(got) != "standard" {
		t.Fatalf("set remediation value = %q", got)
	}
}

func TestDecodeDuplicateRequirementIDPrunesWork(t *testing.T) {
	source := rootWithRequirements(`[` + minimalRequirement("R1") + `,{"id":"R1","source":"s","applies":`)
	je := rejectPolicy(t, source, Limits{}, CodeDuplicateID)
	want := bytes.LastIndex([]byte(source), []byte(`"id":"R1"`)) + len(`"id":`)
	if je.Offset != want {
		t.Fatalf("duplicate offset = %d, want %d", je.Offset, want)
	}
}

func TestDecodeMalformedFixtureCodes(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		limits Limits
		code   ErrorCode
	}{
		{"excessive depth", "malformed-depth.json", Limits{MaxDepth: 2}, CodeLimit},
		{"invalid arity", "malformed-arity.json", Limits{}, CodeInvalidArity},
		{"invalid reference", "malformed-reference.json", Limits{}, CodeInvalidReference},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := readPolicyFixture(t, tc.file)
			b := policyBuilder(len(source))
			err := Decode(b, source, testSchema(t), testInterner(t), tc.limits)
			var je *Error
			if !errors.As(err, &je) {
				t.Fatalf("error = %v, want *Error", err)
			}
			if je.Code != tc.code {
				t.Fatalf("code = %s (%d), want %s (%d)", je.Code, je.Code, tc.code, tc.code)
			}
			d := b.Document()
			if len(d.InputBytes) != 0 || len(d.NodeKinds) != 0 || len(d.ValueKinds) != 0 ||
				len(d.EvidenceKindNames) != 0 || len(d.EvidenceStateNames) != 0 ||
				len(d.OutcomeNames) != 0 || len(d.RemediationKinds) != 0 ||
				len(d.ClauseAssertionRoots) != 0 || len(d.RequirementIDs) != 0 {
				t.Fatalf("failed decode left AST content: %+v", d)
			}
			if _, ok := d.PolicyMetadata(); ok {
				t.Fatal("failed decode left metadata set")
			}
		})
	}
}

func TestDecodeRequirementIDMax(t *testing.T) {
	source := rootWithRequirements(`[` + minimalRequirement("R"+"4294967295") + `]`)
	b := decodePolicy(t, []byte(source), Limits{})
	if _, ok := b.Document().RequirementRoot(schema.RequirementID(math.MaxUint32)); !ok {
		t.Fatal("max uint32 requirement ID missing")
	}
}
