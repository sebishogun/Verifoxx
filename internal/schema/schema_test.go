package schema

import (
	"errors"
	"reflect"
	"testing"
)

// requirementNamespace and requestNamespace exist to demonstrate that each
// strong ID type flows through API boundaries that accept only its own
// namespace. Passing a RequestID to requirementNamespace is a compile error.
func requirementNamespace(r RequirementID) RequirementID { return r }

func requestNamespace(r RequestID) RequestID { return r }

func TestIDTypesAreDistinctNamedTypes(t *testing.T) {
	samples := []any{
		RequirementID(1), RequestID(1), EvidenceID(1), NodeID(1), FieldID(1),
		ValueID(1), SymbolID(1), OutcomeID(1), RemediationID(1), EvidenceKindID(1),
		EvidenceStateID(1), ClauseID(1), InstructionID(1), SlotID(1), ReasonID(1),
		TemplateID(1), ExplanationID(1),
	}
	seen := make(map[string]int, len(samples))
	for _, s := range samples {
		typ := reflect.TypeOf(s)
		name := typ.Name()
		if name == "" || name == "uint32" {
			t.Fatalf("sample %v has no named type", s)
		}
		if typ.Kind() != reflect.Uint32 {
			t.Fatalf("%s underlying kind = %v, want uint32", name, typ.Kind())
		}
		if _, dup := seen[name]; dup {
			t.Fatalf("named type %q is not distinct", name)
		}
		seen[name] = len(seen)
	}
	if len(seen) != 17 {
		t.Fatalf("expected 17 distinct named handle types, got %d: %v", len(seen), seen)
	}
}

func TestNamedTypesUsedInOwnNamespaces(t *testing.T) {
	var r RequirementID = 1
	var q RequestID = 1
	if requirementNamespace(r) != 1 {
		t.Fatal("RequirementID did not round-trip through its namespace")
	}
	if requestNamespace(q) != 1 {
		t.Fatal("RequestID did not round-trip through its namespace")
	}
}

func TestZeroValueIsInvalidID(t *testing.T) {
	b := NewBuilder()
	if _, err := b.AddField(SymbolID(0), ValueKindSymbol, FieldGroupSubject); !errors.Is(err, ErrInvalidSymbol) {
		t.Fatalf("AddField with zero symbol err = %v, want ErrInvalidSymbol", err)
	}
	if b.Len() != 0 {
		t.Fatalf("builder Len = %d after rejected add, want 0", b.Len())
	}
}

func TestValueKindsBounded(t *testing.T) {
	valid := []ValueKind{
		ValueKindSymbol, ValueKindInteger, ValueKindBoolean, ValueKindTimestamp, ValueKindPresence,
	}
	seen := make(map[ValueKind]bool, len(valid))
	for _, k := range valid {
		if !k.Valid() {
			t.Fatalf("ValueKind(%d) must be valid", k)
		}
		if seen[k] {
			t.Fatalf("duplicate value kind %d", k)
		}
		seen[k] = true
	}
	if ValueKindInvalid.Valid() {
		t.Fatal("ValueKindInvalid must not be valid")
	}
	if ValueKind(6).Valid() || ValueKind(7).Valid() || ValueKind(255).Valid() {
		t.Fatal("out-of-range ValueKind must not be valid")
	}
	if len(seen) != 5 {
		t.Fatalf("expected 5 distinct value kinds, got %d", len(seen))
	}
}

func TestValueKindString(t *testing.T) {
	tests := []struct {
		kind ValueKind
		want string
	}{
		{ValueKindInvalid, "invalid"},
		{ValueKindSymbol, "symbol"},
		{ValueKindInteger, "integer"},
		{ValueKindBoolean, "boolean"},
		{ValueKindTimestamp, "timestamp"},
		{ValueKindPresence, "presence"},
		{ValueKind(6), "invalid"},
		{ValueKind(255), "invalid"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("ValueKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestFieldGroupsBounded(t *testing.T) {
	groups := []FieldGroup{
		FieldGroupSubject, FieldGroupAction, FieldGroupResource, FieldGroupOutput, FieldGroupContext,
	}
	if FieldGroupInvalid != 0 {
		t.Fatalf("FieldGroupInvalid = %d, want 0", FieldGroupInvalid)
	}
	seen := make(map[FieldGroup]bool, len(groups))
	for _, g := range groups {
		if !g.Valid() {
			t.Fatalf("FieldGroup(%d) must be valid", g)
		}
		if seen[g] {
			t.Fatalf("duplicate field group %d", g)
		}
		seen[g] = true
	}
	if FieldGroupInvalid.Valid() {
		t.Fatal("FieldGroupInvalid must not be valid")
	}
	if len(seen) != 5 {
		t.Fatalf("expected 5 distinct field groups, got %d", len(seen))
	}
}

func TestAddFieldAssignsSequentialStableIDs(t *testing.T) {
	b := NewBuilder()
	id1, err := b.AddField(SymbolID(10), ValueKindSymbol, FieldGroupSubject)
	if err != nil || id1 != 1 {
		t.Fatalf("first AddField = (%d, %v), want (1, nil)", id1, err)
	}
	id2, err := b.AddField(SymbolID(20), ValueKindSymbol, FieldGroupAction)
	if err != nil || id2 != 2 {
		t.Fatalf("second AddField = (%d, %v), want (2, nil)", id2, err)
	}
	id3, err := b.AddField(SymbolID(30), ValueKindInteger, FieldGroupResource)
	if err != nil || id3 != 3 {
		t.Fatalf("third AddField = (%d, %v), want (3, nil)", id3, err)
	}
	id4, err := b.AddField(SymbolID(40), ValueKindPresence, FieldGroupContext)
	if err != nil || id4 != 4 {
		t.Fatalf("fourth AddField = (%d, %v), want (4, nil)", id4, err)
	}
	if id1 != 1 || id2 != 2 || id3 != 3 {
		t.Fatalf("earlier FieldIDs changed: %d %d %d", id1, id2, id3)
	}
	if b.Len() != 4 {
		t.Fatalf("builder Len = %d, want 4", b.Len())
	}
}

func TestAddFieldRejectsExactDuplicate(t *testing.T) {
	b := NewBuilder()
	id, err := b.AddField(SymbolID(5), ValueKindSymbol, FieldGroupSubject)
	if err != nil || id != 1 {
		t.Fatalf("initial AddField = (%d, %v), want (1, nil)", id, err)
	}
	if _, err := b.AddField(SymbolID(5), ValueKindSymbol, FieldGroupSubject); !errors.Is(err, ErrDuplicateField) {
		t.Fatalf("exact duplicate err = %v, want ErrDuplicateField", err)
	}
	if b.Len() != 1 {
		t.Fatalf("builder mutated on exact duplicate: Len = %d, want 1", b.Len())
	}
}

func TestAddFieldRejectsIncompatibleRedefinition(t *testing.T) {
	b := NewBuilder()
	id1, err := b.AddField(SymbolID(5), ValueKindSymbol, FieldGroupSubject)
	if err != nil || id1 != 1 {
		t.Fatalf("initial AddField = (%d, %v), want (1, nil)", id1, err)
	}
	if _, err := b.AddField(SymbolID(5), ValueKindInteger, FieldGroupContext); !errors.Is(err, ErrDuplicateField) {
		t.Fatalf("duplicate add err = %v, want ErrDuplicateField", err)
	}
	if b.Len() != 1 {
		t.Fatalf("builder mutated on rejected duplicate: Len = %d, want 1", b.Len())
	}
	if got, ok := b.Lookup(SymbolID(5)); !ok || got != id1 {
		t.Fatalf("original field lost after rejected duplicate: (%d, %v), want (%d, true)", got, ok, id1)
	}
}

func TestFieldTableDoesNotExportMutableColumns(t *testing.T) {
	typ := reflect.TypeOf(FieldTable{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.IsExported() && field.Type.Kind() == reflect.Slice {
			t.Errorf("FieldTable exports mutable slice column %s", field.Name)
		}
	}
}

func TestAddFieldRejectsInvalidValueKind(t *testing.T) {
	b := NewBuilder()
	for _, k := range []ValueKind{ValueKindInvalid, 6, 7, 255} {
		if _, err := b.AddField(SymbolID(1), k, FieldGroupSubject); !errors.Is(err, ErrInvalidValueKind) {
			t.Fatalf("AddField with kind %d err = %v, want ErrInvalidValueKind", k, err)
		}
	}
	if b.Len() != 0 {
		t.Fatalf("builder mutated on rejected kinds: Len = %d, want 0", b.Len())
	}
}

func TestAddFieldRejectsInvalidFieldGroup(t *testing.T) {
	b := NewBuilder()
	if _, err := b.AddField(SymbolID(1), ValueKindSymbol, FieldGroupInvalid); !errors.Is(err, ErrInvalidFieldGroup) {
		t.Fatalf("AddField with invalid group err = %v, want ErrInvalidFieldGroup", err)
	}
	if b.Len() != 0 {
		t.Fatalf("builder mutated on rejected group: Len = %d, want 0", b.Len())
	}
}

func TestSchemaLookupAndMetadata(t *testing.T) {
	b := NewBuilder()
	subject := SymbolID(1)
	if _, err := b.AddField(subject, ValueKindSymbol, FieldGroupSubject); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddField(SymbolID(2), ValueKindInteger, FieldGroupResource); err != nil {
		t.Fatal(err)
	}
	s := b.Finish()

	if got, ok := s.Lookup(subject); !ok || got != 1 {
		t.Fatalf("Lookup(subject) = (%d, %v), want (1, true)", got, ok)
	}
	if got, ok := s.Lookup(SymbolID(2)); !ok || got != 2 {
		t.Fatalf("Lookup(2) = (%d, %v), want (2, true)", got, ok)
	}
	if got, ok := s.Lookup(SymbolID(99)); ok || got != 0 {
		t.Fatalf("Lookup(99) = (%d, %v), want (0, false)", got, ok)
	}

	if k, ok := s.Kind(1); !ok || k != ValueKindSymbol {
		t.Fatalf("Kind(1) = (%d, %v), want (symbol, true)", k, ok)
	}
	if g, ok := s.Group(2); !ok || g != FieldGroupResource {
		t.Fatalf("Group(2) = (%d, %v), want (resource, true)", g, ok)
	}
	if n, ok := s.Name(1); !ok || n != subject {
		t.Fatalf("Name(1) = (%d, %v), want (%d, true)", n, ok, subject)
	}

	if _, ok := s.Kind(0); ok {
		t.Fatal("Kind(0) must not be valid")
	}
	if _, ok := s.Group(0); ok {
		t.Fatal("Group(0) must not be valid")
	}
	if _, ok := s.Kind(3); ok {
		t.Fatal("Kind(3) out of range must not be valid")
	}
	if _, ok := s.Name(99); ok {
		t.Fatal("Name(99) out of range must not be valid")
	}
	if s.Len() != 2 {
		t.Fatalf("schema Len = %d, want 2", s.Len())
	}
}

func TestLookupIsAllocationFree(t *testing.T) {
	b := NewBuilder()
	for i := SymbolID(1); i <= 20; i++ {
		if _, err := b.AddField(i, ValueKindSymbol, FieldGroupSubject); err != nil {
			t.Fatal(err)
		}
	}
	s := b.Finish()

	missed := false
	allocs := testing.AllocsPerRun(1000, func() {
		for i := SymbolID(1); i <= 20; i++ {
			if _, ok := s.Lookup(i); !ok {
				missed = true
			}
		}
	})
	if missed {
		t.Fatal("Lookup missed a registered field")
	}
	if allocs != 0 {
		t.Fatalf("Lookup allocates %.2f allocs/run, want 0", allocs)
	}
}

func TestBuilderResetRetainsCapacity(t *testing.T) {
	b := NewBuilder()
	for i := SymbolID(1); i <= 50; i++ {
		if _, err := b.AddField(i, ValueKindSymbol, FieldGroupSubject); err != nil {
			t.Fatal(err)
		}
	}
	b.Reset()
	if b.Len() != 0 {
		t.Fatalf("Reset left Len = %d, want 0", b.Len())
	}
	restarted, err := b.AddField(SymbolID(100), ValueKindSymbol, FieldGroupSubject)
	if err != nil {
		t.Fatal(err)
	}
	if restarted != 1 {
		t.Fatalf("first FieldID after Reset = %d, want 1", restarted)
	}

	var addErr error
	allocs := testing.AllocsPerRun(100, func() {
		b.Reset()
		for i := SymbolID(1); i <= 50; i++ {
			if _, err := b.AddField(i, ValueKindSymbol, FieldGroupSubject); err != nil {
				addErr = err
			}
		}
	})
	if addErr != nil {
		t.Fatal(addErr)
	}
	if allocs != 0 {
		t.Fatalf("AddField after reset allocates %.2f allocs/run, want 0 (capacity not retained)", allocs)
	}
	if b.Len() != 50 {
		t.Fatalf("builder Len after reuse = %d, want 50", b.Len())
	}
}

func TestFinishIsolatedFromBuilderMutations(t *testing.T) {
	b := NewBuilder()
	if _, err := b.AddField(SymbolID(1), ValueKindSymbol, FieldGroupSubject); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddField(SymbolID(2), ValueKindInteger, FieldGroupResource); err != nil {
		t.Fatal(err)
	}
	s := b.Finish()

	if _, err := b.AddField(SymbolID(3), ValueKindBoolean, FieldGroupContext); err != nil {
		t.Fatal(err)
	}
	b.Reset()
	if _, err := b.AddField(SymbolID(9), ValueKindSymbol, FieldGroupAction); err != nil {
		t.Fatal(err)
	}

	if s.Len() != 2 {
		t.Fatalf("finished schema Len = %d, want 2", s.Len())
	}
	if got, ok := s.Lookup(SymbolID(1)); !ok || got != 1 {
		t.Fatalf("finished schema lost field 1: (%d, %v)", got, ok)
	}
	if got, ok := s.Lookup(SymbolID(2)); !ok || got != 2 {
		t.Fatalf("finished schema lost field 2: (%d, %v)", got, ok)
	}
	if got, ok := s.Lookup(SymbolID(3)); ok {
		t.Fatalf("finished schema saw field 3 added after Finish: (%d, %v)", got, ok)
	}
}

func TestZeroValueSchemaSafe(t *testing.T) {
	var s Schema
	if s.Len() != 0 {
		t.Fatalf("zero schema Len = %d, want 0", s.Len())
	}
	if _, ok := s.Lookup(SymbolID(1)); ok {
		t.Fatal("zero schema Lookup must fail")
	}
	if _, ok := s.Kind(1); ok {
		t.Fatal("zero schema Kind must fail")
	}
	if _, ok := s.Group(1); ok {
		t.Fatal("zero schema Group must fail")
	}
}
