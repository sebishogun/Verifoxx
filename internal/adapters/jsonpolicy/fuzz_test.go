package jsonpolicy

import (
	"errors"
	"testing"
)

// FuzzDecode drives the package-level Decode entry point with bounded limits
// so no input can force unbounded work. Every input must either decode to a
// document with complete metadata or fail with a *Error that leaves the
// builder empty. The shared schema and interner are lookup-only.
func FuzzDecode(f *testing.F) {
	f.Add(readPolicyFixture(f, "valid-full.json"))
	f.Add(readPolicyFixture(f, "malformed-truncated.json"))
	f.Add([]byte{})
	f.Add([]byte(basePolicy))
	fields := testSchema(f)
	symbols := testInterner(f)
	limits := Limits{
		MaxSourceBytes:  1 << 20,
		MaxStringBytes:  1 << 16,
		MaxDepth:        32,
		MaxNodes:        4096,
		MaxValues:       4096,
		MaxArrayItems:   1024,
		MaxCatalogItems: 1024,
		MaxSymbolBytes:  1 << 16,
		MaxRequirements: 512,
		MaxClauses:      2048,
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		b := policyBuilder(min(len(data), limits.MaxSourceBytes))
		err := Decode(b, data, fields, symbols, limits)
		if err == nil {
			if _, ok := b.Document().PolicyMetadata(); !ok {
				t.Fatal("successful decode left no metadata")
			}
			return
		}
		var je *Error
		if !errors.As(err, &je) {
			t.Fatalf("error = %T %v, want *Error", err, err)
		}
		d := b.Document()
		if len(d.InputBytes) != 0 || len(d.NodeKinds) != 0 || len(d.ValueKinds) != 0 ||
			len(d.EvidenceKindNames) != 0 || len(d.EvidenceStateNames) != 0 ||
			len(d.OutcomeNames) != 0 || len(d.RemediationKinds) != 0 ||
			len(d.ClauseAssertionRoots) != 0 || len(d.RequirementIDs) != 0 {
			t.Fatal("failed decode left AST content")
		}
		if _, ok := d.PolicyMetadata(); ok {
			t.Fatal("failed decode left metadata set")
		}
	})
}
