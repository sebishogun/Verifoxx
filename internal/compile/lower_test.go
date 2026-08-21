package compile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/sebishogun/verifoxx/internal/adapters/jsonpolicy"
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// lowerFixture builds the same field schema and source interner required by
// testdata/policies/valid-full.json, decodes the fixture through jsonpolicy,
// validates it with the package Validator, and requires zero diagnostics. It
// returns the Document, the frozen Schema, and the exact Interner that
// assigned the schema's field-name SymbolIDs.
func lowerFixture(t *testing.T) (*ast.Document, *schema.Schema, *schema.Interner) {
	t.Helper()
	syms := schema.NewSymbolInterner(16)
	b := schema.NewBuilder()
	add := func(name string, kind schema.ValueKind, group schema.FieldGroup) {
		id, err := syms.Intern([]byte(name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.AddField(id, kind, group); err != nil {
			t.Fatal(err)
		}
	}
	add("subject.trust", schema.ValueKindSymbol, schema.FieldGroupSubject)
	add("context.environment", schema.ValueKindPresence, schema.FieldGroupContext)
	add("context.usage", schema.ValueKindSymbol, schema.FieldGroupContext)

	src, err := os.ReadFile("../../testdata/policies/valid-full.json")
	if err != nil {
		t.Fatal(err)
	}
	fields := b.Finish()
	ab := ast.NewBuilder(ast.Hints{
		Nodes: 8, CompareNodes: 4, CompareListValues: 4, GroupNodes: 2,
		ChildEdges: 4, NotNodes: 1, EvidenceNodes: 2,
		Values: 16, SymbolValues: 16, SymbolBytes: 512,
		EvidenceKinds: 4, EvidenceStates: 8, Outcomes: 8,
		Remediations: 4, Clauses: 2, ClauseEvidenceEdges: 2,
		ClauseRemediationEdges: 4, Requirements: 2, RequirementClauseEdges: 2,
		SourceBytes: len(src),
	})
	if err := jsonpolicy.Decode(ab, src, fields, syms, jsonpolicy.Limits{}); err != nil {
		t.Fatalf("decode valid-full.json: %v", err)
	}
	doc := ab.Document()
	var v Validator
	if diags := v.Validate(nil, doc, fields); len(diags) != 0 {
		t.Fatalf("valid-full.json produced %d diagnostics: %+v", len(diags), diags)
	}
	return doc, fields, syms
}

// lowerConstantsFixture lowers the canonical fixture's constant columns and
// returns the document, schema, interner, and generated Program.
func lowerConstantsFixture(t *testing.T) (*ast.Document, *schema.Schema, *schema.Interner, program.Program) {
	t.Helper()
	doc, fields, syms := lowerFixture(t)
	var lowerer Lowerer
	var got program.Program
	if err := lowerer.lowerConstants(&got, doc, fields, syms); err != nil {
		t.Fatalf("lowerConstants: %v", err)
	}
	return doc, fields, syms, got
}

// maskedMiss searches a deterministic candidate sequence for bytes that hash
// into an occupied frozen slot without being interned, so Program.LookupSymbol
// must reject them by exact byte comparison. With at most 50% occupancy an
// occupied slot is found within a handful of candidates.
func maskedMiss(t *testing.T, p *program.Program) []byte {
	t.Helper()
	mask := uint64(len(p.SymbolHashes) - 1)
	for i := 0; i < 1000; i++ {
		cand := []byte("miss-")
		cand = append(cand, byte('0'+i/100), byte('0'+(i/10)%10), byte('0'+i%10))
		if p.SymbolIDs[schema.HashSymbol(cand)&mask] != 0 {
			return cand
		}
	}
	t.Fatalf("no masked-slot miss candidate found over 1000 candidates")
	return nil
}

// maskedSymbolPair returns two distinct byte strings that share a masked
// frozen-slot hash, forcing a probe chain. mask+2 candidates into mask+1
// slots guarantee a colliding pair by the pigeonhole principle, so the search
// always terminates.
func maskedSymbolPair(t *testing.T, mask uint64) (string, string) {
	t.Helper()
	n := int(mask) + 2
	for i := 0; i < n; i++ {
		a := fmt.Sprintf("sym-%03d", i)
		for j := i + 1; j < n; j++ {
			b := fmt.Sprintf("sym-%03d", j)
			if schema.HashSymbol([]byte(a))&mask == schema.HashSymbol([]byte(b))&mask {
				return a, b
			}
		}
	}
	t.Fatalf("pool of %d has no masked collision over %d slots", n, mask+1)
	return "", ""
}

// maskedIntPair returns two distinct integers that share a masked value-table
// slot, forcing exact payload comparison. Candidates exclude the fixed
// kind-distinctness payload 1; mask+2 candidates into mask+1 slots guarantee
// a colliding pair by the pigeonhole principle.
func maskedIntPair(t *testing.T, mask uint64) (int64, int64) {
	t.Helper()
	hi := int(mask) + 3
	for i := 2; i <= hi; i++ {
		for j := i + 1; j <= hi; j++ {
			if valueHash(schema.ValueKindInteger, nil, int64(i), 0, 0)&mask ==
				valueHash(schema.ValueKindInteger, nil, int64(j), 0, 0)&mask {
				return int64(i), int64(j)
			}
		}
	}
	t.Fatalf("pool of %d has no masked collision over %d slots", hi-1, mask+1)
	return 0, 0
}

// buildValueFixture builds a validator-clean synthetic document with
// duplicated typed literals of every kind, a colliding frozen-symbol pair,
// and a colliding canonical-value pair. The frozen table holds 6 distinct
// symbols (slotSize(6) = 16 slots) and the value table is sized for the 17
// AST values (slotSize(17) = 64 slots); the collision searches use those
// masks, so both probe chains are exercised deterministically.
func buildValueFixture(t *testing.T) (*ast.Document, *schema.Schema, *schema.Interner) {
	t.Helper()
	pairA, pairB := maskedSymbolPair(t, uint64(slotSize(6)-1))
	intA, intB := maskedIntPair(t, uint64(slotSize(17)-1))

	syms := schema.NewSymbolInterner(4)
	fields := schema.NewBuilder().Finish()
	ab := ast.NewBuilder(ast.Hints{
		Values: 17, SymbolValues: 8, SymbolBytes: 128,
		IntegerValues: 8, BooleanValues: 8, TimestampValues: 8,
		SourceBytes: 2,
	})
	if err := ab.SetSource([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	name, err := ab.AddSymbolValue([]byte("meta-name"))
	if err != nil {
		t.Fatal(err)
	}
	version, err := ab.AddSymbolValue([]byte("meta-version"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ab.SetMetadata(name, version); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddIntegerValue(1); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddIntegerValue(1); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddIntegerValue(intA); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddIntegerValue(intB); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddBooleanValue(true); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddBooleanValue(true); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddBooleanValue(false); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddTimestampValue(1); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddTimestampValue(1); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddTimestampValue(2); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddSymbolValue([]byte("lit-a")); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddSymbolValue([]byte("lit-a")); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddSymbolValue([]byte("lit-b")); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddSymbolValue([]byte(pairA)); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddSymbolValue([]byte(pairB)); err != nil {
		t.Fatal(err)
	}
	if diags := Validate(nil, ab.Document(), fields); len(diags) != 0 {
		t.Fatalf("value fixture produced %d diagnostics: %+v", len(diags), diags)
	}
	return ab.Document(), fields, syms
}

// canonicalInteger returns the canonical Program ValueID carrying payload.
func canonicalInteger(t *testing.T, p *program.Program, payload int64) schema.ValueID {
	t.Helper()
	for j := range p.ValueKinds {
		if p.ValueKinds[j] != schema.ValueKindInteger {
			continue
		}
		ref := p.ValueRefs[j]
		if ref >= 1 && uint64(ref-1) < uint64(len(p.IntegerValues)) && p.IntegerValues[ref-1] == payload {
			return schema.ValueID(j + 1)
		}
	}
	t.Fatalf("no canonical integer value with payload %d", payload)
	return 0
}

// canonicalBoolean returns the canonical Program ValueID carrying payload.
func canonicalBoolean(t *testing.T, p *program.Program, payload bool) schema.ValueID {
	t.Helper()
	encoded := uint64(0)
	if payload {
		encoded = 1
	}
	for j := range p.ValueKinds {
		if p.ValueKinds[j] != schema.ValueKindBoolean {
			continue
		}
		ref := p.ValueRefs[j]
		if ref >= 1 && uint64(ref-1) < uint64(len(p.BooleanValues)) && p.BooleanValues[ref-1] == encoded {
			return schema.ValueID(j + 1)
		}
	}
	t.Fatalf("no canonical boolean value with payload %v", payload)
	return 0
}

// canonicalTimestamp returns the canonical Program ValueID carrying payload.
func canonicalTimestamp(t *testing.T, p *program.Program, payload int64) schema.ValueID {
	t.Helper()
	for j := range p.ValueKinds {
		if p.ValueKinds[j] != schema.ValueKindTimestamp {
			continue
		}
		ref := p.ValueRefs[j]
		if ref >= 1 && uint64(ref-1) < uint64(len(p.TimestampValues)) && p.TimestampValues[ref-1] == payload {
			return schema.ValueID(j + 1)
		}
	}
	t.Fatalf("no canonical timestamp value with payload %d", payload)
	return 0
}

func TestLowerConstantsCanonicalFixture(t *testing.T) {
	doc, fields, syms, got := lowerConstantsFixture(t)

	// Field IDs preserve row order; names, kinds, and groups are copied with
	// exact bytes resolved through the source interner.
	n := fields.Len()
	if len(got.FieldNames) != n || len(got.FieldKinds) != n || len(got.FieldGroups) != n {
		t.Fatalf("field columns = %d/%d/%d, want %d each",
			len(got.FieldNames), len(got.FieldKinds), len(got.FieldGroups), n)
	}
	for i := 0; i < n; i++ {
		id := schema.FieldID(i + 1)
		name, ok := fields.Name(id)
		if !ok {
			t.Fatal("schema field name missing")
		}
		want, ok := syms.Bytes(name)
		if !ok {
			t.Fatal("field symbol bytes missing from interner")
		}
		gotName, ok := got.Symbol(got.FieldNames[i])
		if !ok || !bytes.Equal(gotName, want) {
			t.Fatalf("FieldNames[%d] = %q, want %q", i, gotName, want)
		}
		kind, _ := fields.Kind(id)
		group, _ := fields.Group(id)
		if got.FieldKinds[i] != kind || got.FieldGroups[i] != group {
			t.Fatalf("field %d metadata = (%v, %v), want (%v, %v)",
				id, got.FieldKinds[i], got.FieldGroups[i], kind, group)
		}
		if id, ok := got.LookupSymbol(want); !ok || id != got.FieldNames[i] {
			t.Fatalf("field name %q does not resolve to its frozen SymbolID", want)
		}
	}

	// Policy name and version translate into canonical Program SymbolIDs.
	meta, ok := doc.PolicyMetadata()
	if !ok {
		t.Fatal("fixture metadata missing")
	}
	if got.PolicyName == 0 || got.PolicyVersion == 0 {
		t.Fatal("policy identity not translated")
	}
	for _, tc := range []struct {
		valueID schema.ValueID
		program schema.SymbolID
	}{{meta.Name, got.PolicyName}, {meta.Version, got.PolicyVersion}} {
		b, ok := doc.SymbolValue(tc.valueID)
		if !ok {
			t.Fatal("metadata symbol value missing")
		}
		want, ok := got.LookupSymbol(b)
		if !ok {
			t.Fatalf("policy metadata %q not frozen", b)
		}
		if want != tc.program {
			t.Fatalf("policy metadata %q = SymbolID %d, want %d", b, tc.program, want)
		}
	}

	// Evidence-kind and evidence-state names translate into canonical Program
	// SymbolIDs and their source-span peers are preserved for Task 6.
	for i, nameID := range doc.EvidenceKindNames {
		b, ok := doc.SymbolValue(nameID)
		if !ok {
			t.Fatal("evidence-kind name value missing")
		}
		want, ok := got.LookupSymbol(b)
		if !ok {
			t.Fatalf("evidence-kind name %q not frozen", b)
		}
		if got.EvidenceKindNames[i] != want {
			t.Fatalf("EvidenceKindNames[%d] = %d, want %d", i, got.EvidenceKindNames[i], want)
		}
	}
	if !reflect.DeepEqual(got.EvidenceKindSourceStarts, doc.EvidenceKindSourceStarts) ||
		!reflect.DeepEqual(got.EvidenceKindSourceEnds, doc.EvidenceKindSourceEnds) {
		t.Fatal("evidence-kind source spans not preserved")
	}
	for i, nameID := range doc.EvidenceStateNames {
		b, ok := doc.SymbolValue(nameID)
		if !ok {
			t.Fatal("evidence-state name value missing")
		}
		want, ok := got.LookupSymbol(b)
		if !ok {
			t.Fatalf("evidence-state name %q not frozen", b)
		}
		if got.EvidenceStateNames[i] != want {
			t.Fatalf("EvidenceStateNames[%d] = %d, want %d", i, got.EvidenceStateNames[i], want)
		}
	}
	if !reflect.DeepEqual(got.EvidenceStateSourceStarts, doc.EvidenceStateSourceStarts) ||
		!reflect.DeepEqual(got.EvidenceStateSourceEnds, doc.EvidenceStateSourceEnds) {
		t.Fatal("evidence-state source spans not preserved")
	}

	// Outcome names resolve through canonical Program SymbolIDs; the outcome
	// rows themselves belong to the Task 6 result tables.
	for i := 1; i <= len(doc.OutcomeNames); i++ {
		nameID, _, _, ok := doc.Outcome(schema.OutcomeID(i))
		if !ok {
			t.Fatal("outcome row missing")
		}
		b, ok := doc.SymbolValue(nameID)
		if !ok {
			t.Fatal("outcome name value missing")
		}
		if _, ok := got.LookupSymbol(b); !ok {
			t.Fatalf("outcome name %q not in the frozen symbol space", b)
		}
	}

	// Equal symbol bytes used by several AST ValueIDs map to one Program
	// SymbolID: "standard" is decoded as an In literal and as the set_field
	// remediation value.
	standard := []byte("standard")
	var standardSym schema.SymbolID
	saw := false
	for i := 1; i <= len(doc.ValueKinds); i++ {
		if kind, ok := doc.ValueKind(schema.ValueID(i)); !ok || kind != schema.ValueKindSymbol {
			continue
		}
		b, ok := doc.SymbolValue(schema.ValueID(i))
		if !ok || !bytes.Equal(b, standard) {
			continue
		}
		id, ok := got.LookupSymbol(b)
		if !ok {
			t.Fatalf("standard literal not frozen")
		}
		if saw && id != standardSym {
			t.Fatalf("standard bytes map to SymbolID %d then %d", standardSym, id)
		}
		standardSym, saw = id, true
	}
	if !saw {
		t.Fatal("fixture must contain the standard literal twice")
	}
	canonicalStandard := 0
	for i := range got.ValueKinds {
		if got.ValueKinds[i] != schema.ValueKindSymbol {
			continue
		}
		b, ok := got.Symbol(schema.SymbolID(got.ValueRefs[i]))
		if !ok {
			t.Fatal("canonical symbol ref out of range")
		}
		if bytes.Equal(b, standard) {
			canonicalStandard++
		}
	}
	if canonicalStandard != 1 {
		t.Fatalf("canonical values carrying standard = %d, want 1", canonicalStandard)
	}

	// The fixture stores only symbol values; the 18 AST values deduplicate to
	// the number of distinct byte sequences.
	distinct := map[string]struct{}{}
	for i := 1; i <= len(doc.ValueKinds); i++ {
		b, ok := doc.SymbolValue(schema.ValueID(i))
		if !ok {
			t.Fatal("fixture value is not a symbol")
		}
		distinct[string(b)] = struct{}{}
	}
	if len(got.ValueKinds) != len(distinct) {
		t.Fatalf("canonical values = %d, want %d distinct", len(got.ValueKinds), len(distinct))
	}

	// Program ValueRefs are one-based for every kind.
	for i := range got.ValueKinds {
		id := schema.ValueID(i + 1)
		kind, ok := got.ValueKind(id)
		if !ok {
			t.Fatal("canonical value kind missing")
		}
		ref := got.ValueRefs[i]
		if ref == 0 {
			t.Fatalf("value %d has a zero ref", id)
		}
		switch kind {
		case schema.ValueKindSymbol:
			if ref > got.ProgramSymbolCount {
				t.Fatalf("symbol ref %d exceeds ProgramSymbolCount %d", ref, got.ProgramSymbolCount)
			}
			b, ok := got.Symbol(schema.SymbolID(ref))
			if !ok {
				t.Fatalf("symbol ref %d out of range", ref)
			}
			if id, ok := got.LookupSymbol(b); !ok || id != schema.SymbolID(ref) {
				t.Fatalf("symbol ref %d does not round-trip", ref)
			}
		case schema.ValueKindInteger:
			if uint64(ref) > uint64(len(got.IntegerValues)) {
				t.Fatalf("integer ref %d exceeds column length %d", ref, len(got.IntegerValues))
			}
		case schema.ValueKindBoolean:
			if uint64(ref) > uint64(len(got.BooleanValues)) || got.BooleanValues[ref-1] > 1 {
				t.Fatalf("boolean ref %d invalid in column of %d", ref, len(got.BooleanValues))
			}
		case schema.ValueKindTimestamp:
			if uint64(ref) > uint64(len(got.TimestampValues)) {
				t.Fatalf("timestamp ref %d exceeds column length %d", ref, len(got.TimestampValues))
			}
		default:
			t.Fatalf("value %d has invalid kind %v", id, kind)
		}
	}

	// Frozen symbol slots are a nonempty power of two with <= 50% occupancy.
	slots := len(got.SymbolHashes)
	if slots == 0 || slots != len(got.SymbolIDs) || slots&(slots-1) != 0 {
		t.Fatalf("frozen slots = %d, want a nonempty power of two", slots)
	}
	if 2*uint64(got.ProgramSymbolCount) > uint64(slots) {
		t.Fatalf("frozen occupancy %d/%d exceeds 50%%", got.ProgramSymbolCount, slots)
	}
	if uint64(got.ProgramSymbolCount) != uint64(len(got.SymbolStarts)) ||
		uint64(got.ProgramSymbolCount) != uint64(len(got.SymbolLengths)) {
		t.Fatalf("ProgramSymbolCount %d does not match the %d slab rows", got.ProgramSymbolCount, len(got.SymbolStarts))
	}
	for id := schema.SymbolID(1); id <= schema.SymbolID(got.ProgramSymbolCount); id++ {
		if _, ok := got.Symbol(id); !ok {
			t.Fatalf("Symbol(%d) missing from the frozen slab", id)
		}
	}
}

func TestLowerConstantsValueInterning(t *testing.T) {
	doc, fields, syms := buildValueFixture(t)
	var lowerer Lowerer
	var got program.Program
	if err := lowerer.lowerConstants(&got, doc, fields, syms); err != nil {
		t.Fatalf("lowerConstants: %v", err)
	}

	if len(doc.ValueKinds) != 17 {
		t.Fatalf("fixture AST values = %d, want 17", len(doc.ValueKinds))
	}
	if len(got.ValueKinds) != 13 || len(got.ValueRefs) != 13 {
		t.Fatalf("canonical values = %d/%d, want 13 each",
			len(got.ValueKinds), len(got.ValueRefs))
	}

	// Duplicate typed literal payloads map to one Program ValueID.
	if len(got.IntegerValues) != 3 {
		t.Fatalf("IntegerValues = %d, want 3", len(got.IntegerValues))
	}
	if len(got.BooleanValues) != 2 {
		t.Fatalf("BooleanValues = %d, want 2", len(got.BooleanValues))
	}
	if len(got.TimestampValues) != 2 {
		t.Fatalf("TimestampValues = %d, want 2", len(got.TimestampValues))
	}

	// Kinds remain distinct: integer 1, Boolean true, and timestamp 1 never
	// alias even though their payload words are equal.
	intOne := canonicalInteger(t, &got, 1)
	boolTrue := canonicalBoolean(t, &got, true)
	tsOne := canonicalTimestamp(t, &got, 1)
	ids := map[schema.ValueID]string{intOne: "integer 1", boolTrue: "boolean true", tsOne: "timestamp 1"}
	if len(ids) != 3 {
		t.Fatalf("equal typed payloads aliased: %v", ids)
	}
	if kind, _ := got.ValueKind(intOne); kind != schema.ValueKindInteger {
		t.Fatalf("integer 1 value has kind %v", kind)
	}
	if kind, _ := got.ValueKind(boolTrue); kind != schema.ValueKindBoolean {
		t.Fatalf("boolean true value has kind %v", kind)
	}
	if kind, _ := got.ValueKind(tsOne); kind != schema.ValueKindTimestamp {
		t.Fatalf("timestamp 1 value has kind %v", kind)
	}

	// The Program Boolean column is a packed uint64 column with one-based
	// references; zero-based AST refs never leak into Program refs.
	if got.ValueRefs[boolTrue-1] != 1 {
		t.Fatalf("boolean true ref = %d, want 1", got.ValueRefs[boolTrue-1])
	}
	if got.BooleanValues[0] != 1 {
		t.Fatalf("BooleanValues[0] = %d, want 1", got.BooleanValues[0])
	}
	if ref := got.ValueRefs[canonicalBoolean(t, &got, false)-1]; ref != 2 {
		t.Fatalf("boolean false ref = %d, want 2", ref)
	}
	if got.BooleanValues[1] != 0 {
		t.Fatalf("BooleanValues[1] = %d, want 0", got.BooleanValues[1])
	}
	for i := range got.BooleanValues {
		if got.BooleanValues[i] > 1 {
			t.Fatalf("BooleanValues[%d] = %d, not a packed 0/1", i, got.BooleanValues[i])
		}
	}

	// Payload columns hold each distinct literal once in one-based order.
	if got.IntegerValues[0] != 1 || got.TimestampValues[0] != 1 || got.TimestampValues[1] != 2 {
		t.Fatalf("typed payload columns misplaced: ints %v, timestamps %v",
			got.IntegerValues, got.TimestampValues)
	}

	// One-based refs for every kind.
	for i := range got.ValueKinds {
		id := schema.ValueID(i + 1)
		kind, ok := got.ValueKind(id)
		if !ok {
			t.Fatal("canonical value kind missing")
		}
		ref := got.ValueRefs[i]
		if ref == 0 {
			t.Fatalf("value %d has a zero ref", id)
		}
		switch kind {
		case schema.ValueKindSymbol:
			if ref > got.ProgramSymbolCount {
				t.Fatalf("symbol ref %d exceeds ProgramSymbolCount %d", ref, got.ProgramSymbolCount)
			}
		case schema.ValueKindInteger:
			if uint64(ref) > uint64(len(got.IntegerValues)) {
				t.Fatalf("integer ref %d exceeds column length %d", ref, len(got.IntegerValues))
			}
		case schema.ValueKindBoolean:
			if uint64(ref) > uint64(len(got.BooleanValues)) {
				t.Fatalf("boolean ref %d exceeds column length %d", ref, len(got.BooleanValues))
			}
		case schema.ValueKindTimestamp:
			if uint64(ref) > uint64(len(got.TimestampValues)) {
				t.Fatalf("timestamp ref %d exceeds column length %d", ref, len(got.TimestampValues))
			}
		default:
			t.Fatalf("value %d has invalid kind %v", id, kind)
		}
	}

	// The colliding symbol pair shares a frozen masked slot by construction;
	// exact byte comparison resolves both to distinct SymbolIDs. The pair is
	// the two "sym-*" strings the fixture interned.
	pairMask := uint64(len(got.SymbolHashes) - 1)
	if got.ProgramSymbolCount != 6 || len(got.SymbolHashes) != 16 {
		t.Fatalf("frozen space = %d symbols in %d slots, want 6 in 16",
			got.ProgramSymbolCount, len(got.SymbolHashes))
	}
	var pairA, pairB []byte
	for id := schema.SymbolID(1); id <= schema.SymbolID(got.ProgramSymbolCount); id++ {
		b, ok := got.Symbol(id)
		if !ok {
			t.Fatal("frozen symbol missing")
		}
		if bytes.HasPrefix(b, []byte("sym-")) {
			if pairA == nil {
				pairA = b
			} else {
				pairB = b
			}
		}
	}
	if pairA == nil || pairB == nil {
		t.Fatal("fixture must intern the colliding symbol pair")
	}
	if schema.HashSymbol(pairA)&pairMask != schema.HashSymbol(pairB)&pairMask {
		t.Fatalf("fixture: %q and %q must share frozen slot %d", pairA, pairB, pairMask)
	}
	symA, ok := got.LookupSymbol(pairA)
	symB, ok2 := got.LookupSymbol(pairB)
	if !ok || !ok2 {
		t.Fatal("colliding pair must resolve")
	}
	if symA == symB {
		t.Fatalf("colliding bytes %q and %q aliased to SymbolID %d", pairA, pairB, symA)
	}

	// The colliding integer pair shares a masked value-table slot by
	// construction; exact payload comparison keeps both canonical values.
	valMask := uint64(len(lowerer.valHashes) - 1)
	intA, intB := maskedIntPair(t, valMask)
	vidA := canonicalInteger(t, &got, intA)
	vidB := canonicalInteger(t, &got, intB)
	if vidA == vidB {
		t.Fatalf("colliding integers %d and %d aliased to value %d", intA, intB, vidA)
	}
	if valueHash(schema.ValueKindInteger, nil, intA, 0, 0)&valMask !=
		valueHash(schema.ValueKindInteger, nil, intB, 0, 0)&valMask {
		t.Fatalf("fixture: integers %d and %d must share value slot %d", intA, intB, valMask)
	}
	slotA, slotB := -1, -1
	for i, id := range lowerer.valIDs {
		if id == vidA {
			slotA = i
		}
		if id == vidB {
			slotB = i
		}
	}
	if slotA < 0 || slotB < 0 {
		t.Fatal("colliding values missing from the intern table")
	}
	if lowerer.valHashes[slotA]&valMask != lowerer.valHashes[slotB]&valMask {
		t.Fatalf("intern table slots %d and %d must share a masked hash", slotA, slotB)
	}
	if got.IntegerValues[got.ValueRefs[vidA-1]-1] == got.IntegerValues[got.ValueRefs[vidB-1]-1] {
		t.Fatalf("colliding values %d and %d share a payload", vidA, vidB)
	}
}

func TestLowerConstantsFrozenMissReservesExtensionIDs(t *testing.T) {
	doc, _, _, got := lowerConstantsFixture(t)
	count := got.ProgramSymbolCount
	if uint64(count) != uint64(len(got.SymbolStarts)) {
		t.Fatalf("ProgramSymbolCount %d != slab rows %d", count, len(got.SymbolStarts))
	}

	// Every interned symbol byte sequence resolves within the frozen space.
	for i := 1; i <= len(doc.ValueKinds); i++ {
		kind, ok := doc.ValueKind(schema.ValueID(i))
		if !ok || kind != schema.ValueKindSymbol {
			continue
		}
		b, ok := doc.SymbolValue(schema.ValueID(i))
		if !ok {
			t.Fatal("symbol value missing")
		}
		id, ok := got.LookupSymbol(b)
		if !ok {
			t.Fatalf("interned symbol %q missing from the frozen table", b)
		}
		if id > schema.SymbolID(count) {
			t.Fatalf("lookup returned %d above ProgramSymbolCount %d", id, count)
		}
	}

	// A plain miss returns false and leaves the frozen space unchanged.
	if id, ok := got.LookupSymbol([]byte("no-such-symbol")); ok {
		t.Fatalf("unknown symbol resolved to %d", id)
	}
	if got.ProgramSymbolCount != count {
		t.Fatal("frozen miss changed ProgramSymbolCount")
	}

	// A masked-slot miss (hash lands on an occupied slot) also returns false
	// and leaves the frozen space unchanged, so IDs above ProgramSymbolCount
	// stay reserved for Task 13 batch extensions.
	miss := maskedMiss(t, &got)
	if id, ok := got.LookupSymbol(miss); ok {
		t.Fatalf("masked miss %q resolved to %d", miss, id)
	}
	if got.ProgramSymbolCount != count {
		t.Fatal("masked frozen miss changed ProgramSymbolCount")
	}
}

func TestLowerConstantsReuse(t *testing.T) {
	doc, fields, syms := lowerFixture(t)
	var lowerer Lowerer
	var dst program.Program
	lower := func(p *program.Program) {
		t.Helper()
		if err := lowerer.lowerConstants(p, doc, fields, syms); err != nil {
			t.Fatalf("lowerConstants: %v", err)
		}
	}
	lower(&dst)

	// Warm reuse of the same destination and Lowerer allocates nothing.
	if allocs := testing.AllocsPerRun(3, func() { lower(&dst) }); allocs != 0 {
		t.Fatalf("warm reuse allocated %f B/op", allocs)
	}

	// A fresh destination on the same Lowerer emits identical output.
	var fresh program.Program
	lower(&fresh)
	if !reflect.DeepEqual(fresh, dst) {
		t.Fatal("fresh lowering differs from the reused destination")
	}

	// A different, smaller document clears logical scratch on the same
	// Lowerer without dropping capacity.
	sdoc, sfields, ssyms := buildValueFixture(t)
	var small program.Program
	if err := lowerer.lowerConstants(&small, sdoc, sfields, ssyms); err != nil {
		t.Fatalf("lowerConstants (small): %v", err)
	}

	// Returning to the fixture still allocates nothing, proving that the
	// capacity was retained, and still emits the identical output.
	if allocs := testing.AllocsPerRun(3, func() { lower(&dst) }); allocs != 0 {
		t.Fatalf("post-small warm reuse allocated %f B/op", allocs)
	}
	var fresh2 program.Program
	lower(&fresh2)
	if !reflect.DeepEqual(fresh2, dst) {
		t.Fatal("post-small lowering differs from the reused destination")
	}
}

// zeroRunPads returns eight distinct symbol strings whose canonical-value
// masked hashes are exactly 0x0c..0x13 at the fixture's value-table mask,
// filling the probe run that makes Timestamp 0's probe compare against the
// stored zero-payload entries. The search is deterministic and bounded.
func zeroRunPads(t *testing.T, mask uint64) []string {
	t.Helper()
	pads := make([]string, 8)
	for target := uint64(0x0c); target <= 0x13; target++ {
		found := false
		for i := 0; i < 8192; i++ {
			cand := fmt.Sprintf("pad-%02x-%04d", target, i)
			if valueHash(schema.ValueKindSymbol, []byte(cand), 0, 0, 0)&mask == target {
				pads[target-0x0c] = cand
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("no pad with value slot 0x%02x found", target)
		}
	}
	return pads
}

// buildZeroValueFixture builds a validator-clean document with Integer 0,
// Boolean false, and Timestamp 0 literals. The 13 AST values size the value
// intern table to 32 slots (mask 31); eight pad symbols occupy the probe run
// 0x0c..0x13, and the mixed-in hashes place Integer 0 at 0x14 and Boolean
// false at 0x0f, so Timestamp 0 (home 0x0c) probes through both stored
// zero-payload entries before finding an empty slot.
func buildZeroValueFixture(t *testing.T) (*ast.Document, *schema.Schema, *schema.Interner) {
	t.Helper()
	valMask := uint64(slotSize(13) - 1)
	if valMask != 31 {
		t.Fatalf("fixture value-table mask = %d, want 31", valMask)
	}
	pads := zeroRunPads(t, valMask)

	syms := schema.NewSymbolInterner(4)
	fields := schema.NewBuilder().Finish()
	ab := ast.NewBuilder(ast.Hints{
		Values: 13, SymbolValues: 10, SymbolBytes: 256,
		IntegerValues: 1, BooleanValues: 1, TimestampValues: 1,
		SourceBytes: 2,
	})
	if err := ab.SetSource([]byte("{}")); err != nil {
		t.Fatal(err)
	}
	name, err := ab.AddSymbolValue([]byte("meta-name"))
	if err != nil {
		t.Fatal(err)
	}
	version, err := ab.AddSymbolValue([]byte("meta-version"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ab.SetMetadata(name, version); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddIntegerValue(0); err != nil {
		t.Fatal(err)
	}
	if _, err := ab.AddBooleanValue(false); err != nil {
		t.Fatal(err)
	}
	for _, pad := range pads {
		if _, err := ab.AddSymbolValue([]byte(pad)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ab.AddTimestampValue(0); err != nil {
		t.Fatal(err)
	}
	if diags := Validate(nil, ab.Document(), fields); len(diags) != 0 {
		t.Fatalf("zero fixture produced %d diagnostics: %+v", len(diags), diags)
	}
	return ab.Document(), fields, syms
}

// TestLowerConstantsZeroPayloadKindsDistinct proves that Integer 0, Boolean
// false, and Timestamp 0 never alias even when their probe slots collide.
// Exact value equality must reject a differing candidate kind before any
// payload comparison, so the zero payload word of one kind can never match
// the zero payload word stored under another kind.
func TestLowerConstantsZeroPayloadKindsDistinct(t *testing.T) {
	doc, fields, syms := buildZeroValueFixture(t)
	var lowerer Lowerer
	var got program.Program
	if err := lowerer.lowerConstants(&got, doc, fields, syms); err != nil {
		t.Fatalf("lowerConstants: %v", err)
	}
	if len(doc.ValueKinds) != 13 {
		t.Fatalf("fixture AST values = %d, want 13", len(doc.ValueKinds))
	}

	// The three zero payloads receive distinct ValueIDs with distinct kinds
	// and their own one-based payload references.
	intZero := canonicalInteger(t, &got, 0)
	boolFalse := canonicalBoolean(t, &got, false)
	tsZero := canonicalTimestamp(t, &got, 0)
	ids := map[schema.ValueID]string{intZero: "integer 0", boolFalse: "boolean false", tsZero: "timestamp 0"}
	if len(ids) != 3 {
		t.Fatalf("zero payloads aliased: %v", ids)
	}
	if kind, _ := got.ValueKind(intZero); kind != schema.ValueKindInteger {
		t.Fatalf("integer 0 value has kind %v", kind)
	}
	if kind, _ := got.ValueKind(boolFalse); kind != schema.ValueKindBoolean {
		t.Fatalf("boolean false value has kind %v", kind)
	}
	if kind, _ := got.ValueKind(tsZero); kind != schema.ValueKindTimestamp {
		t.Fatalf("timestamp 0 value has kind %v", kind)
	}
	if len(got.IntegerValues) != 1 || got.IntegerValues[0] != 0 {
		t.Fatalf("IntegerValues = %v, want [0]", got.IntegerValues)
	}
	if len(got.BooleanValues) != 1 || got.BooleanValues[0] != 0 {
		t.Fatalf("BooleanValues = %v, want [0]", got.BooleanValues)
	}
	if len(got.TimestampValues) != 1 || got.TimestampValues[0] != 0 {
		t.Fatalf("TimestampValues = %v, want [0]", got.TimestampValues)
	}
	for _, tc := range []struct {
		id  schema.ValueID
		ref uint32
	}{
		{intZero, 1},
		{boolFalse, 1},
		{tsZero, 1},
	} {
		if ref := got.ValueRefs[tc.id-1]; ref != tc.ref {
			t.Fatalf("value %d ref = %d, want %d", tc.id, ref, tc.ref)
		}
	}

	// Explicit collision condition: the intern table is 32 slots (mask 31),
	// Timestamp 0's home slot is 0x0c, and the probe run 0x0c..0x13 is fully
	// occupied, with Integer 0 and Boolean false stored inside it. Timestamp
	// 0's probe therefore compares against both stored zero-payload entries
	// before finding an empty slot. The assertions read the actual intern
	// table placement, so the test cannot weaken silently.
	valMask := uint64(len(lowerer.valHashes) - 1)
	if valMask != 31 {
		t.Fatalf("value intern table mask = %d, want 31", valMask)
	}
	tsHome := valueHash(schema.ValueKindTimestamp, nil, 0, 0, 0) & valMask
	if tsHome != 0x0c {
		t.Fatalf("timestamp-0 home = 0x%x, want 0x0c", tsHome)
	}
	slotOf := func(id schema.ValueID) int {
		for i, vid := range lowerer.valIDs {
			if vid == id {
				return i
			}
		}
		t.Fatalf("value %d missing from intern table", id)
		return -1
	}
	intSlot := slotOf(intZero)
	boolSlot := slotOf(boolFalse)
	tsSlot := slotOf(tsZero)
	if intSlot <= int(tsHome) || intSlot >= tsSlot {
		t.Fatalf("integer-0 slot 0x%x not inside timestamp-0 probe run [0x%x, 0x%x)",
			intSlot, tsHome, tsSlot)
	}
	if boolSlot <= int(tsHome) || boolSlot >= tsSlot {
		t.Fatalf("boolean-false slot 0x%x not inside timestamp-0 probe run [0x%x, 0x%x)",
			boolSlot, tsHome, tsSlot)
	}
	for s := int(tsHome); s < tsSlot; s++ {
		if lowerer.valIDs[s] == 0 {
			t.Fatalf("probe path slot 0x%x is empty", s)
		}
	}
	if lowerer.valIDs[tsHome] == tsZero {
		t.Fatal("timestamp-0 probe must start at an occupied slot")
	}
}

func TestLowerConstantsErrors(t *testing.T) {
	doc, fields, syms := lowerFixture(t)
	var lowerer Lowerer
	var got program.Program

	cases := []struct {
		name string
		doc  *ast.Document
		fs   *schema.Schema
		sy   *schema.Interner
		want error
	}{
		{"nil document", nil, fields, syms, ErrInvalidDocument},
		{"nil schema", doc, nil, syms, ErrInvalidDocument},
		{"nil interner", doc, fields, nil, ErrInvalidDocument},
		{"mismatched value peers", doc, fields, syms, nil}, // replaced below
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := lowerer.lowerConstants(&got, tc.doc, tc.fs, tc.sy)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	// Truncated value peer columns are structurally invalid.
	bad := *doc
	bad.ValueRefs = bad.ValueRefs[:len(bad.ValueRefs)-1]
	if err := lowerer.lowerConstants(&got, &bad, fields, syms); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("truncated ValueRefs: err = %v, want ErrInvalidDocument", err)
	}

	// A schema field-name SymbolID missing from the supplied interner fails
	// with ErrInvalidSymbols; the interner identity is the caller contract.
	wrongSyms := schema.NewSymbolInterner(4)
	if err := lowerer.lowerConstants(&got, doc, fields, wrongSyms); !errors.Is(err, ErrInvalidSymbols) {
		t.Fatalf("foreign interner: err = %v, want ErrInvalidSymbols", err)
	}
}
