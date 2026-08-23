package jsonpolicy

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

const basePolicy = `{"schema_version":1,"name":"verifoxx","version":"1.0.0","assumptions":[],"evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[]}`

const fullPolicy = `{"schema_version":1,"name":"verifoxx","version":"1.0.0","assumptions":[],"evidence_kinds":[{"name":"approval_record"},{"name":"usage_adjustment"}],"evidence_states":[{"name":"current"},{"name":"stale"},{"name":"unclear"}],"outcomes":[{"terminal":true,"precedence":1,"name":"Approve"},{"name":"Reject","precedence":2,"terminal":true},{"name":"Revise","precedence":3,"terminal":false},{"name":"Escalate","precedence":4,"terminal":true}],"requirements":[]}`

// policy assembles a root document with the given catalog array bodies.
func policy(kinds, states, outcomes string) string {
	return `{"schema_version":1,"name":"verifoxx","version":"1.0.0","assumptions":[],"evidence_kinds":` + kinds + `,"evidence_states":` + states + `,"outcomes":` + outcomes + `,"requirements":[]}`
}

// meta assembles a root document with the given identity slots.
func meta(schemaVersion, name, version string) string {
	return `{"schema_version":` + schemaVersion + `,"name":` + name + `,"version":` + version + `,"assumptions":[],"evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[]}`
}

// reqs assembles a root document with the given requirements value.
func reqs(requirements string) string {
	return `{"schema_version":1,"name":"verifoxx","version":"1.0.0","assumptions":[],"evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":` + requirements + `}`
}

func mustDecode(t *testing.T, source []byte, limits Limits) *ast.Builder {
	t.Helper()
	return mustDecodeWith(t, source, limits, schema.NewSymbolInterner(32))
}

func mustDecodeWith(t *testing.T, source []byte, limits Limits, syms *schema.Interner) *ast.Builder {
	t.Helper()
	b := ast.NewBuilder(ast.Hints{
		Values: 16, SymbolValues: 16, SymbolBytes: 512,
		EvidenceKinds: 8, EvidenceStates: 8, Outcomes: 8,
		SourceBytes: len(source), Requirements: 1,
	})
	if err := Decode(b, source, schema.NewBuilder().Finish(), syms, limits); err != nil {
		t.Fatalf("Decode(%q) error: %v", source, err)
	}
	return b
}

// reject decodes source expecting a *Error with the given code.
func reject(t *testing.T, source string, limits Limits, want ErrorCode, wantOffset int) *Error {
	t.Helper()
	err := Decode(ast.NewBuilder(ast.Hints{}), []byte(source), schema.NewBuilder().Finish(), schema.NewSymbolInterner(8), limits)
	var je *Error
	if !errors.As(err, &je) {
		t.Fatalf("source %q: error = %v, want *Error with code %s", source, err, want)
	}
	if je.Code != want {
		t.Fatalf("source %q: code = %s (%d), want %s (%d)", source, je.Code, je.Code, want, want)
	}
	if je.Offset < 0 || je.Offset > len(source) {
		t.Fatalf("source %q: offset %d outside [0, %d]", source, je.Offset, len(source))
	}
	if wantOffset >= 0 && je.Offset != wantOffset {
		t.Fatalf("source %q: offset = %d, want %d", source, je.Offset, wantOffset)
	}
	return je
}

func offsetOf(t *testing.T, source, substr string) int {
	t.Helper()
	i := strings.Index(source, substr)
	if i < 0 {
		t.Fatalf("substring %q not found in %q", substr, source)
	}
	return i
}

func spanOf(t *testing.T, source []byte, literal string) ast.SourceSpan {
	t.Helper()
	quoted := `"` + literal + `"`
	i := bytes.Index(source, []byte(quoted))
	if i < 0 {
		t.Fatalf("literal %q not found in source", literal)
	}
	return ast.SourceSpan{Start: uint32(i), End: uint32(i + len(quoted))}
}

func symbolString(t *testing.T, d *ast.Document, id schema.ValueID) string {
	t.Helper()
	b, ok := d.SymbolValue(id)
	if !ok {
		t.Fatalf("SymbolValue(%d) missing", id)
	}
	return string(b)
}

func TestDecodeMinimalPolicy(t *testing.T) {
	src := []byte(basePolicy)
	b := mustDecode(t, src, Limits{})
	d := b.Document()
	metadata, ok := d.PolicyMetadata()
	if !ok {
		t.Fatal("PolicyMetadata() = (_, false)")
	}
	if got := symbolString(t, d, metadata.Name); got != "verifoxx" {
		t.Fatalf("metadata name = %q, want %q", got, "verifoxx")
	}
	if got := symbolString(t, d, metadata.Version); got != "1.0.0" {
		t.Fatalf("metadata version = %q, want %q", got, "1.0.0")
	}
	if metadata.ContentHash != sha256.Sum256(src) {
		t.Fatalf("content hash = %x, want sha256 of source", metadata.ContentHash)
	}
	if d.Len() != 0 || len(d.EvidenceKindNames) != 0 || len(d.EvidenceStateNames) != 0 ||
		len(d.OutcomeNames) != 0 || len(d.RequirementIDs) != 0 || len(d.SymbolBytes) == 0 {
		t.Fatalf("unexpected content in decoded document: %+v", d)
	}
}

func TestDecodeFullPolicyRoundTrip(t *testing.T) {
	src := []byte(fullPolicy)
	b := mustDecode(t, src, Limits{})
	d := b.Document()

	metadata, ok := d.PolicyMetadata()
	if !ok {
		t.Fatal("PolicyMetadata() = (_, false)")
	}
	if metadata.ContentHash != sha256.Sum256(src) {
		t.Fatalf("content hash = %x, want sha256 of source", metadata.ContentHash)
	}

	for id, want := range []string{"approval_record", "usage_adjustment"} {
		valueID, ok := d.EvidenceKindName(schema.EvidenceKindID(id + 1))
		if !ok || symbolString(t, d, valueID) != want {
			t.Fatalf("EvidenceKindName(%d) = (%d, %v), want symbol %q", id+1, valueID, ok, want)
		}
		if span, ok := d.EvidenceKindSpan(schema.EvidenceKindID(id + 1)); !ok || span != spanOf(t, src, want) {
			t.Fatalf("EvidenceKindSpan(%d) = (%+v, %v), want span of %q", id+1, span, ok, want)
		}
	}
	if _, ok := d.EvidenceKindName(3); ok {
		t.Fatal("EvidenceKindName(3) must fail")
	}

	for id, want := range []string{"current", "stale", "unclear"} {
		valueID, ok := d.EvidenceStateName(schema.EvidenceStateID(id + 1))
		if !ok || symbolString(t, d, valueID) != want {
			t.Fatalf("EvidenceStateName(%d) = (%d, %v), want symbol %q", id+1, valueID, ok, want)
		}
		if span, ok := d.EvidenceStateSpan(schema.EvidenceStateID(id + 1)); !ok || span != spanOf(t, src, want) {
			t.Fatalf("EvidenceStateSpan(%d) = (%+v, %v), want span of %q", id+1, span, ok, want)
		}
	}

	for i, want := range []struct {
		name       string
		precedence uint8
		terminal   bool
	}{
		{"Approve", 1, true},
		{"Reject", 2, true},
		{"Revise", 3, false},
		{"Escalate", 4, true},
	} {
		id := schema.OutcomeID(i + 1)
		name, precedence, terminal, ok := d.Outcome(id)
		if !ok || symbolString(t, d, name) != want.name || precedence != want.precedence || terminal != want.terminal {
			t.Fatalf("Outcome(%d) = (%d, %d, %v, %v), want (%q, %d, %v, true)",
				id, name, precedence, terminal, ok, want.name, want.precedence, want.terminal)
		}
		if span, ok := d.OutcomeSpan(id); !ok || span != spanOf(t, src, want.name) {
			t.Fatalf("OutcomeSpan(%d) = (%+v, %v), want span of %q", id, span, ok, want.name)
		}
	}
	if _, _, _, ok := d.Outcome(5); ok {
		t.Fatal("Outcome(5) must fail")
	}

	if d.Len() != 0 || len(d.RequirementIDs) != 0 {
		t.Fatalf("unexpected nodes or requirements: len=%d requirements=%d", d.Len(), len(d.RequirementIDs))
	}
}

func TestDecodeAcceptsAnyRootKeyOrder(t *testing.T) {
	src := []byte(`{"requirements":[],"outcomes":[],"evidence_states":[],"evidence_kinds":[],"assumptions":[],"version":"1.0.0","name":"verifoxx","schema_version":1}`)
	b := mustDecode(t, src, Limits{})
	metadata, ok := b.Document().PolicyMetadata()
	if !ok {
		t.Fatal("PolicyMetadata() = (_, false)")
	}
	if metadata.ContentHash != sha256.Sum256(src) {
		t.Fatalf("content hash = %x, want sha256 of source", metadata.ContentHash)
	}
}

func TestDecodeEscapedKeys(t *testing.T) {
	src := []byte(`{"schema_version":1,"na\u006de":"verifoxx","version":"1.0.0","assumptions":[],"evidence_kinds":[{"\u006eame":"approval_record"}],"evidence_states":[],"outcomes":[{"name":"Approve","precedence":1,"term\u0069nal":true}],"requirements":[]}`)
	b := mustDecode(t, src, Limits{})
	d := b.Document()
	kind, ok := d.EvidenceKindName(1)
	if !ok || symbolString(t, d, kind) != "approval_record" {
		t.Fatalf("EvidenceKindName(1) = (%d, %v), want approval_record", kind, ok)
	}
	metadata, ok := d.PolicyMetadata()
	if !ok || symbolString(t, d, metadata.Name) != "verifoxx" {
		t.Fatalf("escaped key metadata = (%+v, %v)", metadata, ok)
	}
}

func TestDecodeStringEscapesAndSurrogatePairs(t *testing.T) {
	src := []byte(`{"schema_version":1,"name":"verifoxx","version":"1.0.0","assumptions":[],"evidence_kinds":[{"name":"caf\u00e9 \u4e2d\u6587 \ud83d\ude00 \t\n\"\\\/\b\f\r"}],"evidence_states":[],"outcomes":[],"requirements":[]}`)
	b := mustDecode(t, src, Limits{})
	d := b.Document()
	name, ok := d.EvidenceKindName(1)
	if !ok {
		t.Fatal("EvidenceKindName(1) missing")
	}
	want := "café 中文 😀 \t\n\"\\/\b\f\r"
	if got := symbolString(t, d, name); got != want {
		t.Fatalf("decoded name = %q, want %q", got, want)
	}
}

func TestDecodeAcceptsTrailingWhitespace(t *testing.T) {
	src := []byte(basePolicy + "  \t\n\r\n")
	b := mustDecode(t, src, Limits{})
	if _, ok := b.Document().PolicyMetadata(); !ok {
		t.Fatal("PolicyMetadata() = (_, false)")
	}
}

func TestDecodeRejects(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		limits Limits
		code   ErrorCode
		offset int
	}{
		{"unknown root key", `{"schema_version":1,"name":"verifoxx","version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[],"extra":1}`, Limits{}, CodeUnknownKey, -1},
		{"unknown key in evidence kind", policy(`[{"name":"a","extra":1}]`, `[]`, `[]`), Limits{}, CodeUnknownKey, -1},
		{"unknown key in outcome", policy(`[]`, `[]`, `[{"name":"Approve","precedence":1,"terminal":true,"extra":1}]`), Limits{}, CodeUnknownKey, -1},
		{"duplicate root key", `{"schema_version":1,"name":"verifoxx","name":"dup","version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[]}`, Limits{}, CodeDuplicateKey, -1},
		{"duplicate key in evidence kind", policy(`[{"name":"a","name":"b"}]`, `[]`, `[]`), Limits{}, CodeDuplicateKey, -1},
		{"duplicate key in outcome", policy(`[]`, `[]`, `[{"name":"A","precedence":1,"precedence":2,"terminal":true}]`), Limits{}, CodeDuplicateKey, -1},
		{"empty root object", `{}`, Limits{}, CodeMissingKey, -1},
		{"missing root key", `{"schema_version":1,"name":"verifoxx","version":"1.0.0","evidence_kinds":[],"outcomes":[],"requirements":[]}`, Limits{}, CodeMissingKey, -1},
		{"single root key", `{"name":"x"}`, Limits{}, CodeMissingKey, -1},
		{"missing name in catalog entry", policy(`[{}]`, `[]`, `[]`), Limits{}, CodeMissingKey, -1},
		{"missing outcome key", policy(`[]`, `[]`, `[{"name":"Approve","terminal":true}]`), Limits{}, CodeMissingKey, -1},
		{"missing colon", `{"schema_version" 1,"name":"verifoxx","version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[]}`, Limits{}, CodeMalformed, -1},
		{"trailing comma in root", `{"schema_version":1,"name":"verifoxx","version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[],}`, Limits{}, CodeMalformed, -1},
		{"trailing comma in catalog array", policy(`[{"name":"a"},]`, `[]`, `[]`), Limits{}, CodeMalformed, -1},
		{"trailing comma in catalog entry", policy(`[{"name":"a",}]`, `[]`, `[]`), Limits{}, CodeMalformed, -1},
		{"malformed literal suffix", policy(`[]`, `[]`, `[{"name":"A","precedence":1,"terminal":truex}]`), Limits{}, CodeMalformed, -1},
		{"leading zero", meta(`01`, `"verifoxx"`, `"1.0.0"`), Limits{}, CodeMalformed, -1},
		{"fraction", meta(`1.0`, `"verifoxx"`, `"1.0.0"`), Limits{}, CodeMalformed, -1},
		{"exponent", meta(`1e0`, `"verifoxx"`, `"1.0.0"`), Limits{}, CodeMalformed, -1},
		{"empty root", "", Limits{}, CodeTruncated, -1},
		{"bare opening brace", `{`, Limits{}, CodeTruncated, -1},
		{"truncated key", `{"schema_ver`, Limits{}, CodeTruncated, -1},
		{"truncated string value", `{"schema_version":1,"name":"verifoxx`, Limits{}, CodeTruncated, -1},
		{"truncated escape", `{"schema_version":1,"name":"a\u00`, Limits{}, CodeTruncated, -1},
		{"truncated minus", `{"schema_version":-`, Limits{}, CodeTruncated, -1},
		{"truncated literal", `{"schema_version":1,"name":"verifoxx","version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[{"name":"A","precedence":1,"terminal":tru`, Limits{}, CodeTruncated, -1},
		{"malformed literal prefix", `{"schema_version":1,"name":"verifoxx","version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[{"name":"A","precedence":1,"terminal":tru}],"requirements":[]}`, Limits{}, CodeMalformed, -1},
		{"truncated catalog", `{"schema_version":1,"name":"verifoxx","version":"1.0.0","evidence_kinds":[{"name":"a"},{"name":"b"}`, Limits{}, CodeTruncated, -1},
		{"truncated after high surrogate", `{"schema_version":1,"name":"a\ud83d`, Limits{}, CodeTruncated, -1},
		{"trailing data", basePolicy + `x`, Limits{}, CodeTrailing, -1},
		{"invalid schema version", meta(`2`, `"verifoxx"`, `"1.0.0"`), Limits{}, CodeInvalidVersion, -1},
		{"negative schema version", meta(`-1`, `"verifoxx"`, `"1.0.0"`), Limits{}, CodeInvalidVersion, -1},
		{"string schema version", meta(`"1"`, `"verifoxx"`, `"1.0.0"`), Limits{}, CodeInvalidType, -1},
		{"bool schema version", meta(`true`, `"verifoxx"`, `"1.0.0"`), Limits{}, CodeInvalidType, -1},
		{"number version", meta(`1`, `"verifoxx"`, `7`), Limits{}, CodeInvalidType, -1},
		{"null version", meta(`1`, `"verifoxx"`, `null`), Limits{}, CodeInvalidType, -1},
		{"number name", meta(`1`, `5`, `"1.0.0"`), Limits{}, CodeInvalidType, -1},
		{"precedence string", policy(`[]`, `[]`, `[{"name":"A","precedence":"1","terminal":true}]`), Limits{}, CodeInvalidType, -1},
		{"terminal number", policy(`[]`, `[]`, `[{"name":"A","precedence":1,"terminal":1}]`), Limits{}, CodeInvalidType, -1},
		{"terminal null", policy(`[]`, `[]`, `[{"name":"A","precedence":1,"terminal":null}]`), Limits{}, CodeInvalidType, -1},
		{"kinds not array", `{"schema_version":1,"name":"verifoxx","version":"1.0.0","evidence_kinds":{},"evidence_states":[],"outcomes":[],"requirements":[]}`, Limits{}, CodeInvalidType, -1},
		{"requirements not array", reqs(`5`), Limits{}, CodeInvalidType, -1},
		{"incomplete requirement", reqs(`[{"id":"R1"}]`), Limits{}, CodeMissingKey, -1},
		{"negative precedence", policy(`[]`, `[]`, `[{"name":"A","precedence":-1,"terminal":true}]`), Limits{}, CodeLimit, -1},
		{"overflow precedence", policy(`[]`, `[]`, `[{"name":"A","precedence":256,"terminal":true}]`), Limits{}, CodeLimit, -1},
		{"control character in string", `{"schema_version":1,"name":"a` + "\x01" + `b","version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[]}`, Limits{}, CodeMalformed, -1},
		{"invalid escape", `{"schema_version":1,"name":"a\xq","version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[]}`, Limits{}, CodeMalformed, -1},
		{"invalid hex digit", `{"schema_version":1,"name":"a\u00G0b","version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[]}`, Limits{}, CodeMalformed, -1},
		{"lone high surrogate", `{"schema_version":1,"name":"a\ud800b","version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[]}`, Limits{}, CodeMalformed, -1},
		{"lone low surrogate", `{"schema_version":1,"name":"a\udc00b","version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[]}`, Limits{}, CodeMalformed, -1},
		{"high surrogate not followed by low", `{"schema_version":1,"name":"a\ud83d\u0041","version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[]}`, Limits{}, CodeMalformed, -1},
		{"invalid utf8 in string", `{"schema_version":1,"name":"a` + "\xff" + `b","version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[]}`, Limits{}, CodeInvalidUTF8, -1},
		{"truncated multibyte rune", `{"schema_version":1,"name":"a` + "\xe4\xb8" + `","version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[]}`, Limits{}, CodeInvalidUTF8, -1},
		{"source limit", basePolicy, Limits{MaxSourceBytes: 10}, CodeLimit, 10},
		{"catalog limit", policy(`[{"name":"a"},{"name":"b"}]`, `[]`, `[]`), Limits{MaxCatalogItems: 1}, CodeLimit, -1},
		{"string limit", `{"name":"verifoxx","schema_version":1,"version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[]}`, Limits{MaxStringBytes: 5}, CodeLimit, -1},
		{"string limit during growth", `{"name":"` + strings.Repeat("a", 1<<20) + `","schema_version":1,"version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[]}`, Limits{MaxStringBytes: 8}, CodeLimit, -1},
		{"symbol limit", meta(`1`, `"verifoxx"`, `"1.0.0"`), Limits{MaxSymbolBytes: 4}, CodeLimit, -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reject(t, tc.src, tc.limits, tc.code, tc.offset)
		})
	}
}

func TestDecodeRejectsWithExactOffsets(t *testing.T) {
	src := `{"schema_version":1,"name":"verifoxx","version":"1.0.0","evidence_kinds":[],"evidence_states":[],"outcomes":[],"requirements":[],"extra":1}`
	reject(t, src, Limits{}, CodeUnknownKey, offsetOf(t, src, `"extra"`))

	src = meta(`2`, `"verifoxx"`, `"1.0.0"`)
	reject(t, src, Limits{}, CodeInvalidVersion, offsetOf(t, src, ":2")+1)

	src = meta(`1`, `"verifoxx"`, `7`)
	reject(t, src, Limits{}, CodeInvalidType, offsetOf(t, src, `7`))

	src = `{"schema_ver`
	reject(t, src, Limits{}, CodeTruncated, len(src))
}

func TestDecodeRollbackOnErrorAndReuse(t *testing.T) {
	syms := schema.NewSymbolInterner(32)
	b := ast.NewBuilder(ast.Hints{
		Values: 16, SymbolValues: 16, SymbolBytes: 512,
		EvidenceKinds: 8, EvidenceStates: 8, Outcomes: 8,
		SourceBytes: len(fullPolicy), Requirements: 1,
	})
	if err := Decode(b, []byte(fullPolicy), schema.NewBuilder().Finish(), syms, Limits{}); err != nil {
		t.Fatal(err)
	}
	bad := []byte(`{"schema_version":1,"name":"verifoxx","version":"1.0.0","evidence_kinds":[{"name":"a"`)
	var je *Error
	if err := Decode(b, bad, schema.NewBuilder().Finish(), syms, Limits{}); !errors.As(err, &je) {
		t.Fatalf("Decode(bad) error = %v, want *Error", err)
	}
	d := b.Document()
	if d.Len() != 0 {
		t.Fatalf("rollback left %d nodes", d.Len())
	}
	if _, ok := d.PolicyMetadata(); ok {
		t.Fatal("rollback left metadata set")
	}
	if len(d.InputBytes) != 0 || len(d.SymbolBytes) != 0 || len(d.EvidenceKindNames) != 0 ||
		len(d.EvidenceStateNames) != 0 || len(d.OutcomeNames) != 0 || len(d.RequirementIDs) != 0 {
		t.Fatalf("rollback left stale content: %+v", d)
	}
	if err := Decode(b, []byte(fullPolicy), schema.NewBuilder().Finish(), syms, Limits{}); err != nil {
		t.Fatalf("reuse after rollback failed: %v", err)
	}
	if metadata, ok := b.Document().PolicyMetadata(); !ok || metadata.ContentHash != sha256.Sum256([]byte(fullPolicy)) {
		t.Fatalf("reuse metadata = (%+v, %v)", metadata, ok)
	}
}

func TestDecodeLeavesInternerUnchanged(t *testing.T) {
	syms := schema.NewSymbolInterner(16)
	for _, name := range []string{"subject.trust", "context.usage"} {
		if _, err := syms.Intern([]byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	before := syms.Len()
	beforeBytes := syms.ByteLen()
	mustDecodeWith(t, []byte(fullPolicy), Limits{}, syms)
	if syms.Len() != before || syms.ByteLen() != beforeBytes {
		t.Fatalf("successful decode mutated interner: len %d->%d, bytes %d->%d", before, syms.Len(), beforeBytes, syms.ByteLen())
	}
	if _, ok := syms.Lookup([]byte("verifoxx")); ok {
		t.Fatal("decode interned policy symbol verifoxx")
	}
	if _, ok := syms.Lookup([]byte("Approve")); ok {
		t.Fatal("decode interned outcome symbol Approve")
	}
	var je *Error
	err := Decode(ast.NewBuilder(ast.Hints{}), []byte(`{"schema_version":1,"name":"verifoxx`), schema.NewBuilder().Finish(), syms, Limits{})
	if !errors.As(err, &je) {
		t.Fatalf("failed decode error = %v, want *Error", err)
	}
	if syms.Len() != before || syms.ByteLen() != beforeBytes {
		t.Fatalf("failed decode mutated interner: len %d->%d, bytes %d->%d", before, syms.Len(), beforeBytes, syms.ByteLen())
	}
}

func TestDecodeRejectsDuplicateCatalogNames(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"duplicate evidence kind", policy(`[{"name":"approval_record"},{"name":"approval_record"}]`, `[]`, `[]`)},
		{"escaped duplicate evidence kind", policy(`[{"name":"approval_record"},{"name":"approval\u005frecord"}]`, `[]`, `[]`)},
		{"duplicate evidence state", policy(`[]`, `[{"name":"current"},{"name":"current"}]`, `[]`)},
		{"duplicate outcome", policy(`[]`, `[]`, `[{"name":"Approve","precedence":1,"terminal":true},{"name":"Approve","precedence":2,"terminal":true}]`)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reject(t, tc.src, Limits{}, CodeDuplicateID, -1)
		})
	}
}

func TestErrorString(t *testing.T) {
	err := &Error{Code: CodeUnknownKey, Offset: 12, Message: "unknown root key"}
	if got := err.Error(); !strings.Contains(got, "unknown_key") || !strings.Contains(got, "12") {
		t.Fatalf("Error() = %q", got)
	}
	if got := CodeUnknownKey.String(); got != "unknown_key" {
		t.Fatalf("CodeUnknownKey.String() = %q", got)
	}
	if got := ErrorCode(0).String(); got != "invalid" {
		t.Fatalf("zero code String() = %q, want invalid", got)
	}
}
