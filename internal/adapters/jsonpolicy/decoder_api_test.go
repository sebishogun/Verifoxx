package jsonpolicy

import (
	"crypto/sha256"
	"errors"
	"reflect"
	"testing"
)

func TestDecoderZeroValueDecodesFixture(t *testing.T) {
	source := readPolicyFixture(t, "valid-full.json")
	var dec Decoder
	b := policyBuilder(len(source))
	if err := dec.Decode(b, source, testSchema(t), testInterner(t), Limits{}); err != nil {
		t.Fatalf("zero-value Decoder.Decode error: %v", err)
	}
	metadata, ok := b.Document().PolicyMetadata()
	if !ok || metadata.ContentHash != sha256.Sum256(source) {
		t.Fatalf("metadata = (%+v, %v)", metadata, ok)
	}
}

func TestDecoderReuseIsDeterministicAndInternerReadOnly(t *testing.T) {
	source := readPolicyFixture(t, "valid-full.json")
	fields := testSchema(t)
	symbols := testInterner(t)
	beforeLen, beforeBytes := symbols.Len(), symbols.ByteLen()
	var dec Decoder
	b := policyBuilder(len(source))
	if err := dec.Decode(b, source, fields, symbols, Limits{}); err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(b, source, fields, symbols, Limits{}); err != nil {
		t.Fatalf("second decode on same builder: %v", err)
	}
	b2 := policyBuilder(len(source))
	if err := dec.Decode(b2, source, fields, symbols, Limits{}); err != nil {
		t.Fatalf("decode into fresh builder: %v", err)
	}
	if !reflect.DeepEqual(*b.Document(), *b2.Document()) {
		t.Fatal("reused Decoder produced different AST documents")
	}
	if symbols.Len() != beforeLen || symbols.ByteLen() != beforeBytes {
		t.Fatalf("shared interner changed: (%d, %d) -> (%d, %d)", beforeLen, beforeBytes, symbols.Len(), symbols.ByteLen())
	}
}

func TestDecoderFailureLeavesBuilderEmptyThenRecovers(t *testing.T) {
	valid := readPolicyFixture(t, "valid-full.json")
	bad := readPolicyFixture(t, "malformed-truncated.json")
	var dec Decoder
	b := policyBuilder(len(valid))
	if err := dec.Decode(b, valid, testSchema(t), testInterner(t), Limits{}); err != nil {
		t.Fatal(err)
	}
	var je *Error
	if err := dec.Decode(b, bad, testSchema(t), testInterner(t), Limits{}); !errors.As(err, &je) {
		t.Fatalf("malformed error = %v, want *Error", err)
	}
	if b.Len() != 0 || len(b.Document().RequirementIDs) != 0 {
		t.Fatal("failed decode left builder non-empty")
	}
	if err := dec.Decode(b, valid, testSchema(t), testInterner(t), Limits{}); err != nil {
		t.Fatalf("recovery decode: %v", err)
	}
	if metadata, ok := b.Document().PolicyMetadata(); !ok || metadata.ContentHash != sha256.Sum256(valid) {
		t.Fatalf("recovery metadata = (%+v, %v)", metadata, ok)
	}
}

func TestDecoderClearsReferencesAfterCalls(t *testing.T) {
	valid := readPolicyFixture(t, "valid-full.json")
	bad := readPolicyFixture(t, "malformed-truncated.json")
	fields := testSchema(t)
	symbols := testInterner(t)
	var dec Decoder
	b := policyBuilder(len(valid))
	if err := dec.Decode(b, valid, fields, symbols, Limits{}); err != nil {
		t.Fatal(err)
	}
	if dec.decoder.src != nil || dec.decoder.fields != nil || dec.decoder.symbols != nil {
		t.Fatal("source/schema/interner retained after success")
	}
	if err := dec.Decode(b, bad, fields, symbols, Limits{}); err == nil {
		t.Fatal("malformed decode succeeded")
	}
	if dec.decoder.src != nil || dec.decoder.fields != nil || dec.decoder.symbols != nil {
		t.Fatal("source/schema/interner retained after error")
	}
}

func TestDecoderOversizedSourceBoundedAndRecovers(t *testing.T) {
	valid := readPolicyFixture(t, "valid-full.json")
	fields := testSchema(t)
	symbols := testInterner(t)
	var dec Decoder
	b := policyBuilder(len(valid))
	if err := dec.Decode(b, valid, fields, symbols, Limits{}); err != nil {
		t.Fatal(err)
	}
	over := append([]byte(valid), 'x')
	var je *Error
	if err := dec.Decode(b, over, fields, symbols, Limits{MaxSourceBytes: len(valid)}); !errors.As(err, &je) || je.Code != CodeLimit {
		t.Fatalf("oversized error = %v, want CodeLimit", err)
	}
	d := b.Document()
	if len(d.InputBytes) != 0 || len(d.NodeKinds) != 0 || len(d.ValueKinds) != 0 ||
		len(d.EvidenceKindNames) != 0 || len(d.EvidenceStateNames) != 0 ||
		len(d.OutcomeNames) != 0 || len(d.RemediationKinds) != 0 ||
		len(d.ClauseAssertionRoots) != 0 || len(d.RequirementIDs) != 0 {
		t.Fatal("oversized failure left builder content")
	}
	if _, ok := d.PolicyMetadata(); ok {
		t.Fatal("oversized failure left metadata set")
	}
	if dec.decoder.src != nil || dec.decoder.fields != nil || dec.decoder.symbols != nil {
		t.Fatal("references retained after oversized failure")
	}
	if len(dec.decoder.keyScratch) != 0 || len(dec.decoder.valueScratch) != 0 ||
		len(dec.decoder.nodeScratch) != 0 || len(dec.decoder.valueIDScratch) != 0 ||
		len(dec.decoder.clauseScratch) != 0 || len(dec.decoder.remedyScratch) != 0 {
		t.Fatal("scratch lengths non-zero after oversized failure")
	}
	if err := dec.Decode(b, valid, fields, symbols, Limits{}); err != nil {
		t.Fatalf("recovery decode: %v", err)
	}
	if metadata, ok := b.Document().PolicyMetadata(); !ok || metadata.ContentHash != sha256.Sum256(valid) {
		t.Fatalf("recovery metadata = (%+v, %v)", metadata, ok)
	}
}

func TestDecoderScratchRetainsCapacityWithZeroLengths(t *testing.T) {
	valid := readPolicyFixture(t, "valid-full.json")
	bad := readPolicyFixture(t, "malformed-truncated.json")
	var dec Decoder
	b := policyBuilder(len(valid))
	if err := dec.Decode(b, valid, testSchema(t), testInterner(t), Limits{}); err != nil {
		t.Fatal(err)
	}
	caps := [...]int{
		cap(dec.decoder.keyScratch), cap(dec.decoder.valueScratch),
		cap(dec.decoder.nodeScratch), cap(dec.decoder.valueIDScratch),
		cap(dec.decoder.clauseScratch), cap(dec.decoder.remedyScratch),
	}
	for _, c := range caps {
		if c == 0 {
			t.Fatal("decode left a zero-capacity scratch buffer")
		}
	}
	check := func(phase string) {
		t.Helper()
		if len(dec.decoder.keyScratch) != 0 || len(dec.decoder.valueScratch) != 0 ||
			len(dec.decoder.nodeScratch) != 0 || len(dec.decoder.valueIDScratch) != 0 ||
			len(dec.decoder.clauseScratch) != 0 || len(dec.decoder.remedyScratch) != 0 {
			t.Fatalf("%s: non-zero scratch lengths", phase)
		}
		if cap(dec.decoder.keyScratch) < caps[0] || cap(dec.decoder.valueScratch) < caps[1] ||
			cap(dec.decoder.nodeScratch) < caps[2] || cap(dec.decoder.valueIDScratch) < caps[3] ||
			cap(dec.decoder.clauseScratch) < caps[4] || cap(dec.decoder.remedyScratch) < caps[5] {
			t.Fatalf("%s: scratch capacity lost", phase)
		}
	}
	if err := dec.Decode(b, valid, testSchema(t), testInterner(t), Limits{}); err != nil {
		t.Fatal(err)
	}
	check("after success")
	if err := dec.Decode(b, bad, testSchema(t), testInterner(t), Limits{}); err == nil {
		t.Fatal("malformed decode succeeded")
	}
	check("after error")
}
