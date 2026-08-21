package result

import (
	"math"
	"slices"
	"testing"

	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

// outcomeTable returns the four-row fixture shared by the lookup and
// precedence tests: precedence [1,4,2,3], terminal [true,true,false,true].
func outcomeTable() *OutcomeTable {
	return &OutcomeTable{
		Names:      []schema.SymbolID{10, 20, 30, 40},
		Precedence: []uint8{1, 4, 2, 3},
		Terminal:   []bool{true, true, false, true},
	}
}

func TestOutcomeLookupRows(t *testing.T) {
	table := outcomeTable()
	want := []Outcome{
		{Name: 10, Precedence: 1, Terminal: true},
		{Name: 20, Precedence: 4, Terminal: true},
		{Name: 30, Precedence: 2, Terminal: false},
		{Name: 40, Precedence: 3, Terminal: true},
	}
	for i, record := range want {
		got, ok := table.Lookup(schema.OutcomeID(i + 1))
		if !ok {
			t.Fatalf("Lookup(%d) = ok=false, want record %+v", i+1, record)
		}
		if got != record {
			t.Fatalf("Lookup(%d) = %+v, want %+v", i+1, got, record)
		}
	}
}

func TestOutcomeLookupInvalidIDs(t *testing.T) {
	table := outcomeTable()
	for _, id := range []schema.OutcomeID{0, 5, math.MaxUint32} {
		if got, ok := table.Lookup(id); ok || got != (Outcome{}) {
			t.Fatalf("Lookup(%d) = %+v, ok=%v; want zero record, ok=false", id, got, ok)
		}
	}
}

func TestOutcomeLookupShortColumns(t *testing.T) {
	base := outcomeTable()
	tables := []*OutcomeTable{
		{Names: base.Names[:3], Precedence: base.Precedence, Terminal: base.Terminal},
		{Names: base.Names, Precedence: base.Precedence[:3], Terminal: base.Terminal},
		{Names: base.Names, Precedence: base.Precedence, Terminal: base.Terminal[:3]},
	}
	for i, table := range tables {
		if got, ok := table.Lookup(4); ok {
			t.Fatalf("table %d: Lookup(4) = %+v, ok=true; want false for short column", i, got)
		}
		if _, ok := table.Lookup(3); !ok {
			t.Fatalf("table %d: Lookup(3) must still succeed on a short table", i)
		}
	}
}

func TestOutcomePreferZeroCombinations(t *testing.T) {
	table := outcomeTable()
	cases := []struct {
		current, candidate schema.OutcomeID
		want               schema.OutcomeID
	}{
		{0, 0, 0},
		{0, 3, 3},
		{2, 0, 2},
	}
	for _, c := range cases {
		got, ok := table.Prefer(c.current, c.candidate)
		if !ok || got != c.want {
			t.Fatalf("Prefer(%d,%d) = %d, ok=%v; want %d, true", c.current, c.candidate, got, ok, c.want)
		}
	}
}

func TestOutcomePreferHigherPrecedence(t *testing.T) {
	table := outcomeTable()
	cases := []struct {
		current, candidate schema.OutcomeID
		want               schema.OutcomeID
	}{
		{2, 4, 2},
		{4, 2, 2},
		{1, 3, 3},
		{3, 1, 3},
		{3, 4, 4},
		{4, 3, 4},
		{1, 2, 2},
		{2, 1, 2},
	}
	for _, c := range cases {
		got, ok := table.Prefer(c.current, c.candidate)
		if !ok || got != c.want {
			t.Fatalf("Prefer(%d,%d) = %d, ok=%v; want %d, true", c.current, c.candidate, got, ok, c.want)
		}
	}
}

func TestOutcomePreferEqualIDs(t *testing.T) {
	table := outcomeTable()
	for _, id := range []schema.OutcomeID{1, 2, 3, 4} {
		got, ok := table.Prefer(id, id)
		if !ok || got != id {
			t.Fatalf("Prefer(%d,%d) = %d, ok=%v; want %d, true", id, id, got, ok, id)
		}
	}
}

func TestOutcomePreferTerminalIgnored(t *testing.T) {
	table := outcomeTable()
	cases := []struct {
		current, candidate schema.OutcomeID
		want               schema.OutcomeID
	}{
		{1, 3, 3},
		{3, 1, 3},
		{4, 3, 4},
		{3, 4, 4},
	}
	for _, c := range cases {
		got, ok := table.Prefer(c.current, c.candidate)
		if !ok || got != c.want {
			t.Fatalf("Prefer(%d,%d) = %d, ok=%v; want %d, true", c.current, c.candidate, got, ok, c.want)
		}
	}
}

func TestOutcomePreferTieLowerID(t *testing.T) {
	tie := &OutcomeTable{
		Names:      []schema.SymbolID{50, 51},
		Precedence: []uint8{5, 5},
		Terminal:   []bool{false, false},
	}
	cases := []struct {
		current, candidate schema.OutcomeID
		want               schema.OutcomeID
	}{
		{1, 2, 1},
		{2, 1, 1},
	}
	for _, c := range cases {
		got, ok := tie.Prefer(c.current, c.candidate)
		if !ok || got != c.want {
			t.Fatalf("Prefer(%d,%d) = %d, ok=%v; want %d, true", c.current, c.candidate, got, ok, c.want)
		}
	}
}

func TestOutcomePreferInvalidIDs(t *testing.T) {
	table := outcomeTable()
	cases := [][2]schema.OutcomeID{
		{0, 5},
		{5, 0},
		{99, 3},
		{3, 99},
		{99, 98},
		{0, math.MaxUint32},
		{math.MaxUint32, 0},
	}
	for _, pair := range cases {
		got, ok := table.Prefer(pair[0], pair[1])
		if ok || got != 0 {
			t.Fatalf("Prefer(%d,%d) = %d, ok=%v; want 0, false", pair[0], pair[1], got, ok)
		}
	}
}

func TestOutcomeValid(t *testing.T) {
	base := outcomeTable()
	if !base.valid() {
		t.Fatal("four-row table must be valid")
	}
	if (&OutcomeTable{}).valid() {
		t.Fatal("empty table must be invalid")
	}
	mismatched := []*OutcomeTable{
		{Names: base.Names[:3], Precedence: base.Precedence, Terminal: base.Terminal},
		{Names: append(base.Names, 99), Precedence: base.Precedence, Terminal: base.Terminal},
		{Names: base.Names, Precedence: base.Precedence[:3], Terminal: base.Terminal},
		{Names: base.Names, Precedence: append(base.Precedence, 9), Terminal: base.Terminal},
		{Names: base.Names, Precedence: base.Precedence, Terminal: base.Terminal[:3]},
		{Names: base.Names, Precedence: base.Precedence, Terminal: append(base.Terminal, false)},
	}
	for i, table := range mismatched {
		if table.valid() {
			t.Fatalf("mismatched table %d must be invalid", i)
		}
	}
	zeroName := &OutcomeTable{Names: []schema.SymbolID{0, 20, 30, 40}, Precedence: base.Precedence, Terminal: base.Terminal}
	if zeroName.valid() {
		t.Fatal("zero name must be invalid")
	}
	allowed := &OutcomeTable{Names: []schema.SymbolID{1, 2}, Precedence: []uint8{0, 3}, Terminal: []bool{true, false}}
	if !allowed.valid() {
		t.Fatal("precedence 0 and either terminal value must be allowed")
	}
}

// remediationFixture returns the two-row remediation table: row 1 sets
// context.usage (field 7) to standard (value 42); row 2 requests one allowed
// usage-adjustment evidence kind (9).
func remediationFixture() *RemediationTable {
	return &RemediationTable{
		Kinds:         []RemediationKind{RemediationSetField, RemediationAddEvidence},
		Fields:        []schema.FieldID{7, 0},
		Values:        []schema.ValueID{42, 0},
		EvidenceKinds: []schema.EvidenceKindID{0, 9},
	}
}

func TestRemediationLookupRows(t *testing.T) {
	table := remediationFixture()
	want := []Remediation{
		{Kind: RemediationSetField, Field: 7, Value: 42},
		{Kind: RemediationAddEvidence, EvidenceKind: 9},
	}
	for i, record := range want {
		got, ok := table.Lookup(schema.RemediationID(i + 1))
		if !ok {
			t.Fatalf("Lookup(%d) = ok=false, want record %+v", i+1, record)
		}
		if got != record {
			t.Fatalf("Lookup(%d) = %+v, want %+v", i+1, got, record)
		}
	}
}

func TestRemediationLookupInvalidIDs(t *testing.T) {
	table := remediationFixture()
	for _, id := range []schema.RemediationID{0, 3, math.MaxUint32} {
		if got, ok := table.Lookup(id); ok || got != (Remediation{}) {
			t.Fatalf("Lookup(%d) = %+v, ok=%v; want zero record, ok=false", id, got, ok)
		}
	}
}

func TestRemediationLookupShortColumns(t *testing.T) {
	base := remediationFixture()
	tables := []*RemediationTable{
		{Kinds: base.Kinds[:1], Fields: base.Fields, Values: base.Values, EvidenceKinds: base.EvidenceKinds},
		{Kinds: base.Kinds, Fields: base.Fields[:1], Values: base.Values, EvidenceKinds: base.EvidenceKinds},
		{Kinds: base.Kinds, Fields: base.Fields, Values: base.Values[:1], EvidenceKinds: base.EvidenceKinds},
		{Kinds: base.Kinds, Fields: base.Fields, Values: base.Values, EvidenceKinds: base.EvidenceKinds[:1]},
	}
	for i, table := range tables {
		if got, ok := table.Lookup(2); ok {
			t.Fatalf("table %d: Lookup(2) = %+v, ok=true; want false for short column", i, got)
		}
		if _, ok := table.Lookup(1); !ok {
			t.Fatalf("table %d: Lookup(1) must still succeed on a short table", i)
		}
	}
}

func TestRemediationKindValid(t *testing.T) {
	for _, kind := range []RemediationKind{RemediationSetField, RemediationAddEvidence} {
		if !kind.Valid() {
			t.Fatalf("kind %d must be valid", kind)
		}
	}
	for _, kind := range []RemediationKind{RemediationInvalid, 255} {
		if kind.Valid() {
			t.Fatalf("kind %d must be invalid", kind)
		}
	}
}

func TestRemediationValid(t *testing.T) {
	base := remediationFixture()
	if !base.valid() {
		t.Fatal("two-row table must be valid")
	}
	if !(&RemediationTable{}).valid() {
		t.Fatal("aligned empty table must be valid")
	}
	mismatched := []*RemediationTable{
		{Kinds: base.Kinds[:1], Fields: base.Fields, Values: base.Values, EvidenceKinds: base.EvidenceKinds},
		{Kinds: base.Kinds, Fields: base.Fields[:1], Values: base.Values, EvidenceKinds: base.EvidenceKinds},
		{Kinds: base.Kinds, Fields: base.Fields, Values: base.Values[:1], EvidenceKinds: base.EvidenceKinds},
		{Kinds: base.Kinds, Fields: base.Fields, Values: base.Values, EvidenceKinds: base.EvidenceKinds[:1]},
	}
	for i, table := range mismatched {
		if table.valid() {
			t.Fatalf("mismatched table %d must be invalid", i)
		}
	}
	badKind := &RemediationTable{
		Kinds:         []RemediationKind{RemediationInvalid},
		Fields:        []schema.FieldID{7},
		Values:        []schema.ValueID{42},
		EvidenceKinds: []schema.EvidenceKindID{0},
	}
	if badKind.valid() {
		t.Fatal("invalid kind must make the table invalid")
	}
	malformed := []*RemediationTable{
		{Kinds: []RemediationKind{RemediationSetField}, Fields: []schema.FieldID{0}, Values: []schema.ValueID{42}, EvidenceKinds: []schema.EvidenceKindID{0}},
		{Kinds: []RemediationKind{RemediationSetField}, Fields: []schema.FieldID{7}, Values: []schema.ValueID{0}, EvidenceKinds: []schema.EvidenceKindID{0}},
		{Kinds: []RemediationKind{RemediationSetField}, Fields: []schema.FieldID{7}, Values: []schema.ValueID{42}, EvidenceKinds: []schema.EvidenceKindID{9}},
		{Kinds: []RemediationKind{RemediationAddEvidence}, Fields: []schema.FieldID{7}, Values: []schema.ValueID{0}, EvidenceKinds: []schema.EvidenceKindID{9}},
		{Kinds: []RemediationKind{RemediationAddEvidence}, Fields: []schema.FieldID{0}, Values: []schema.ValueID{42}, EvidenceKinds: []schema.EvidenceKindID{9}},
		{Kinds: []RemediationKind{RemediationAddEvidence}, Fields: []schema.FieldID{0}, Values: []schema.ValueID{0}, EvidenceKinds: []schema.EvidenceKindID{0}},
	}
	for i, table := range malformed {
		if table.valid() {
			t.Fatalf("malformed table %d must be invalid", i)
		}
	}
}

// newResolverFixture returns a valid outcome table, remediation table, and
// one nine-row rule set for constructor tests: four outcomes with precedence
// [1,4,2,3], two remediations, and a rule set whose Missing row selects
// outcome 3 with remediation edges [1,2] and whose other rows select
// outcome 4 with empty ranges.
func newResolverFixture() (OutcomeTable, RemediationTable, ResolutionTable) {
	return OutcomeTable{
			Names:      []schema.SymbolID{10, 20, 30, 40},
			Precedence: []uint8{1, 4, 2, 3},
			Terminal:   []bool{true, true, false, true},
		},
		RemediationTable{
			Kinds:         []RemediationKind{RemediationSetField, RemediationAddEvidence},
			Fields:        []schema.FieldID{7, 0},
			Values:        []schema.ValueID{42, 0},
			EvidenceKinds: []schema.EvidenceKindID{0, 9},
		},
		ResolutionTable{
			OutcomeIDs:        []schema.OutcomeID{3, 4, 4, 4, 4, 4, 4, 4, 4},
			RemediationStarts: []uint32{0, 2, 2, 2, 2, 2, 2, 2, 2},
			RemediationCounts: []uint16{2, 0, 0, 0, 0, 0, 0, 0, 0},
			RemediationIDs:    []schema.RemediationID{1, 2},
		}
}

// assertBorrows fails if got and backing do not share the same backing
// array. Both slices must be nonempty for the first-element address to exist.
func assertBorrows[T any](t *testing.T, name string, got, backing []T) {
	t.Helper()
	if len(got) == 0 || len(backing) == 0 {
		t.Fatalf("%s must be nonempty to compare first-element addresses", name)
	}
	if &got[0] != &backing[0] {
		t.Fatalf("%s must borrow the input backing array", name)
	}
}

func TestNewResolverValid(t *testing.T) {
	outcomes, remediations, rules := newResolverFixture()
	got, err := NewResolver(outcomes, remediations, rules)
	if err != nil {
		t.Fatalf("NewResolver = %v, want nil", err)
	}

	assertBorrows(t, "outcomes.Names", got.outcomes.Names, outcomes.Names)
	assertBorrows(t, "outcomes.Precedence", got.outcomes.Precedence, outcomes.Precedence)
	assertBorrows(t, "outcomes.Terminal", got.outcomes.Terminal, outcomes.Terminal)
	assertBorrows(t, "remediations.Kinds", got.remediations.Kinds, remediations.Kinds)
	assertBorrows(t, "remediations.Fields", got.remediations.Fields, remediations.Fields)
	assertBorrows(t, "remediations.Values", got.remediations.Values, remediations.Values)
	assertBorrows(t, "remediations.EvidenceKinds", got.remediations.EvidenceKinds, remediations.EvidenceKinds)
	assertBorrows(t, "rules.OutcomeIDs", got.rules.OutcomeIDs, rules.OutcomeIDs)
	assertBorrows(t, "rules.RemediationStarts", got.rules.RemediationStarts, rules.RemediationStarts)
	assertBorrows(t, "rules.RemediationCounts", got.rules.RemediationCounts, rules.RemediationCounts)
	assertBorrows(t, "rules.RemediationIDs", got.rules.RemediationIDs, rules.RemediationIDs)

	wantOutcomes := OutcomeTable{
		Names:      []schema.SymbolID{10, 20, 30, 40},
		Precedence: []uint8{1, 4, 2, 3},
		Terminal:   []bool{true, true, false, true},
	}
	wantRemediations := RemediationTable{
		Kinds:         []RemediationKind{RemediationSetField, RemediationAddEvidence},
		Fields:        []schema.FieldID{7, 0},
		Values:        []schema.ValueID{42, 0},
		EvidenceKinds: []schema.EvidenceKindID{0, 9},
	}
	wantRules := ResolutionTable{
		OutcomeIDs:        []schema.OutcomeID{3, 4, 4, 4, 4, 4, 4, 4, 4},
		RemediationStarts: []uint32{0, 2, 2, 2, 2, 2, 2, 2, 2},
		RemediationCounts: []uint16{2, 0, 0, 0, 0, 0, 0, 0, 0},
		RemediationIDs:    []schema.RemediationID{1, 2},
	}
	if !slices.Equal(outcomes.Names, wantOutcomes.Names) {
		t.Fatalf("outcomes.Names = %v, want %v", outcomes.Names, wantOutcomes.Names)
	}
	if !slices.Equal(outcomes.Precedence, wantOutcomes.Precedence) {
		t.Fatalf("outcomes.Precedence = %v, want %v", outcomes.Precedence, wantOutcomes.Precedence)
	}
	if !slices.Equal(outcomes.Terminal, wantOutcomes.Terminal) {
		t.Fatalf("outcomes.Terminal = %v, want %v", outcomes.Terminal, wantOutcomes.Terminal)
	}
	if !slices.Equal(remediations.Kinds, wantRemediations.Kinds) {
		t.Fatalf("remediations.Kinds = %v, want %v", remediations.Kinds, wantRemediations.Kinds)
	}
	if !slices.Equal(remediations.Fields, wantRemediations.Fields) {
		t.Fatalf("remediations.Fields = %v, want %v", remediations.Fields, wantRemediations.Fields)
	}
	if !slices.Equal(remediations.Values, wantRemediations.Values) {
		t.Fatalf("remediations.Values = %v, want %v", remediations.Values, wantRemediations.Values)
	}
	if !slices.Equal(remediations.EvidenceKinds, wantRemediations.EvidenceKinds) {
		t.Fatalf("remediations.EvidenceKinds = %v, want %v", remediations.EvidenceKinds, wantRemediations.EvidenceKinds)
	}
	if !slices.Equal(rules.OutcomeIDs, wantRules.OutcomeIDs) {
		t.Fatalf("rules.OutcomeIDs = %v, want %v", rules.OutcomeIDs, wantRules.OutcomeIDs)
	}
	if !slices.Equal(rules.RemediationStarts, wantRules.RemediationStarts) {
		t.Fatalf("rules.RemediationStarts = %v, want %v", rules.RemediationStarts, wantRules.RemediationStarts)
	}
	if !slices.Equal(rules.RemediationCounts, wantRules.RemediationCounts) {
		t.Fatalf("rules.RemediationCounts = %v, want %v", rules.RemediationCounts, wantRules.RemediationCounts)
	}
	if !slices.Equal(rules.RemediationIDs, wantRules.RemediationIDs) {
		t.Fatalf("rules.RemediationIDs = %v, want %v", rules.RemediationIDs, wantRules.RemediationIDs)
	}
}

func TestNewResolverValidEmptyRemediations(t *testing.T) {
	outcomes, _, _ := newResolverFixture()
	rules := ResolutionTable{
		OutcomeIDs:        []schema.OutcomeID{4, 4, 4, 4, 4, 4, 4, 4, 4},
		RemediationStarts: make([]uint32, 9),
		RemediationCounts: make([]uint16, 9),
	}
	got, err := NewResolver(outcomes, RemediationTable{}, rules)
	if err != nil {
		t.Fatalf("NewResolver = %v, want nil", err)
	}
	if len(got.rules.RemediationIDs) != 0 {
		t.Fatalf("rule set edge slice = %v, want empty", got.rules.RemediationIDs)
	}
}

func TestNewResolverAllowsDanglingValidRemediationEdge(t *testing.T) {
	outcomes, remediations, rules := newResolverFixture()
	rules.RemediationIDs = append(rules.RemediationIDs, 1)
	got, err := NewResolver(outcomes, remediations, rules)
	if err != nil {
		t.Fatalf("NewResolver = %v, want nil", err)
	}
	if len(got.rules.RemediationIDs) != 3 {
		t.Fatalf("edge slice length = %d, want 3", len(got.rules.RemediationIDs))
	}
}

func TestNewResolverMalformed(t *testing.T) {
	cases := []struct {
		name string
		bad  func(*OutcomeTable, *RemediationTable, *ResolutionTable)
		want error
	}{
		{"outcome column mismatch", func(o *OutcomeTable, _ *RemediationTable, _ *ResolutionTable) {
			o.Names = o.Names[:3]
		}, ErrInvalidOutcomeTable},
		{"zero outcome name", func(o *OutcomeTable, _ *RemediationTable, _ *ResolutionTable) {
			o.Names[2] = 0
		}, ErrInvalidOutcomeTable},
		{"remediation column mismatch", func(_ *OutcomeTable, r *RemediationTable, _ *ResolutionTable) {
			r.Kinds = r.Kinds[:1]
		}, ErrInvalidRemediationTable},
		{"invalid remediation kind", func(_ *OutcomeTable, r *RemediationTable, _ *ResolutionTable) {
			r.Kinds[1] = RemediationInvalid
		}, ErrInvalidRemediationTable},
		{"invalid remediation payload", func(_ *OutcomeTable, r *RemediationTable, _ *ResolutionTable) {
			r.Fields[0] = 0
		}, ErrInvalidRemediationTable},
		{"empty resolution rows", func(_ *OutcomeTable, _ *RemediationTable, x *ResolutionTable) {
			x.OutcomeIDs = nil
			x.RemediationStarts = nil
			x.RemediationCounts = nil
		}, ErrInvalidResolutionTable},
		{"outcome row column mismatch", func(_ *OutcomeTable, _ *RemediationTable, x *ResolutionTable) {
			x.OutcomeIDs = x.OutcomeIDs[:8]
		}, ErrInvalidResolutionTable},
		{"start row column mismatch", func(_ *OutcomeTable, _ *RemediationTable, x *ResolutionTable) {
			x.RemediationStarts = x.RemediationStarts[:8]
		}, ErrInvalidResolutionTable},
		{"count row column mismatch", func(_ *OutcomeTable, _ *RemediationTable, x *ResolutionTable) {
			x.RemediationCounts = x.RemediationCounts[:8]
		}, ErrInvalidResolutionTable},
		{"row count not divisible by nine (8 rows)", func(_ *OutcomeTable, _ *RemediationTable, x *ResolutionTable) {
			x.OutcomeIDs = x.OutcomeIDs[:8]
			x.RemediationStarts = x.RemediationStarts[:8]
			x.RemediationCounts = x.RemediationCounts[:8]
		}, ErrInvalidResolutionTable},
		{"row count not divisible by nine (10 rows)", func(_ *OutcomeTable, _ *RemediationTable, x *ResolutionTable) {
			x.OutcomeIDs = append(x.OutcomeIDs, 4)
			x.RemediationStarts = append(x.RemediationStarts, 2)
			x.RemediationCounts = append(x.RemediationCounts, 0)
		}, ErrInvalidResolutionTable},
		{"zero outcome reference", func(_ *OutcomeTable, _ *RemediationTable, x *ResolutionTable) {
			x.OutcomeIDs[0] = 0
		}, ErrInvalidOutcomeReference},
		{"out-of-range outcome reference", func(_ *OutcomeTable, _ *RemediationTable, x *ResolutionTable) {
			x.OutcomeIDs[0] = 5
		}, ErrInvalidOutcomeReference},
		{"csr range beyond edge length", func(_ *OutcomeTable, _ *RemediationTable, x *ResolutionTable) {
			x.RemediationStarts[1] = 3
		}, ErrInvalidResolutionTable},
		{"csr near-MaxUint32 start", func(_ *OutcomeTable, _ *RemediationTable, x *ResolutionTable) {
			x.RemediationStarts[1] = math.MaxUint32
			x.RemediationCounts[1] = 1
		}, ErrInvalidResolutionTable},
		{"zero remediation edge", func(_ *OutcomeTable, _ *RemediationTable, x *ResolutionTable) {
			x.RemediationIDs[1] = 0
		}, ErrInvalidRemediationReference},
		{"out-of-range remediation edge", func(_ *OutcomeTable, _ *RemediationTable, x *ResolutionTable) {
			x.RemediationIDs[1] = 3
		}, ErrInvalidRemediationReference},
		{"dangling invalid edge", func(_ *OutcomeTable, _ *RemediationTable, x *ResolutionTable) {
			x.RemediationIDs = append(x.RemediationIDs, 3)
		}, ErrInvalidRemediationReference},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			outcomes, remediations, rules := newResolverFixture()
			c.bad(&outcomes, &remediations, &rules)
			if _, err := NewResolver(outcomes, remediations, rules); err != c.want {
				t.Fatalf("NewResolver err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestOutcomePreferKnownAgreement(t *testing.T) {
	table := outcomeTable()
	direct := []struct {
		current, candidate schema.OutcomeID
		want               schema.OutcomeID
	}{
		{0, 0, 0},
		{0, 4, 4},
		{2, 0, 2},
		{2, 4, 2},
		{4, 2, 2},
		{3, 3, 3},
	}
	for _, c := range direct {
		if got := table.preferKnown(c.current, c.candidate); got != c.want {
			t.Fatalf("preferKnown(%d,%d) = %d, want %d", c.current, c.candidate, got, c.want)
		}
	}
	tie := &OutcomeTable{
		Names:      []schema.SymbolID{50, 51},
		Precedence: []uint8{5, 5},
		Terminal:   []bool{false, false},
	}
	for _, pair := range [][2]schema.OutcomeID{{1, 2}, {2, 1}} {
		if got := tie.preferKnown(pair[0], pair[1]); got != 1 {
			t.Fatalf("tie preferKnown(%d,%d) = %d, want 1", pair[0], pair[1], got)
		}
	}
	agreement := [][2]schema.OutcomeID{
		{0, 0}, {0, 1}, {2, 0}, {1, 2}, {2, 1}, {3, 3}, {1, 4}, {4, 1}, {2, 3}, {3, 2},
	}
	for _, pair := range agreement {
		want, ok := table.Prefer(pair[0], pair[1])
		if !ok {
			t.Fatalf("Prefer(%d,%d) unexpectedly ok=false", pair[0], pair[1])
		}
		if got := table.preferKnown(pair[0], pair[1]); got != want {
			t.Fatalf("preferKnown(%d,%d) = %d, want Prefer result %d", pair[0], pair[1], got, want)
		}
	}
}

// resolveFixture returns a resolver built from newResolverFixture: outcomes
// 1..4 with precedence [1,4,2,3] and terminal [true,true,false,true], and one
// rule set whose Missing row selects outcome 3 with remediation edges [1,2]
// and whose other rows select outcome 4 with empty ranges.
func resolveFixture() *Resolver {
	outcomes, remediations, rules := newResolverFixture()
	r, err := NewResolver(outcomes, remediations, rules)
	if err != nil {
		panic(err)
	}
	return &r
}

// assertPanics runs fn and fails the test unless it panics with exactly the
// string want.
func assertPanics(t *testing.T, want string, fn func()) {
	t.Helper()
	defer func() {
		if got := recover(); got != want {
			t.Fatalf("panic = %v, want %q", got, want)
		}
	}()
	fn()
}

func TestResolveOneHotReasons(t *testing.T) {
	r := resolveFixture()
	for id := schema.ReasonID(1); id <= truth.ReasonConflict; id++ {
		got, ok := r.Resolve(1, truth.ReasonBit(id))
		if !ok {
			t.Fatalf("Resolve(1, bit(%d)) = ok=false, want true", id)
		}
		if got.Reason != id {
			t.Fatalf("Resolve(1, bit(%d)).Reason = %d, want %d", id, got.Reason, id)
		}
		switch id {
		case truth.ReasonMissing:
			if got.Outcome != 3 || got.Terminal {
				t.Fatalf("Resolve(1, bit(Missing)) = outcome %d terminal %v, want nonterminal outcome 3", got.Outcome, got.Terminal)
			}
			if !slices.Equal(got.Remediations, []schema.RemediationID{1, 2}) {
				t.Fatalf("Resolve(1, bit(Missing)).Remediations = %v, want [1 2]", got.Remediations)
			}
		default:
			if got.Outcome != 4 || !got.Terminal {
				t.Fatalf("Resolve(1, bit(%d)) = outcome %d terminal %v, want terminal outcome 4", id, got.Outcome, got.Terminal)
			}
			if len(got.Remediations) != 0 {
				t.Fatalf("Resolve(1, bit(%d)).Remediations = %v, want empty", id, got.Remediations)
			}
		}
	}
}

func TestResolveMissingRemediationShapes(t *testing.T) {
	r := resolveFixture()
	got, ok := r.Resolve(1, truth.ReasonBit(truth.ReasonMissing))
	if !ok {
		t.Fatal("Resolve(1, bit(Missing)) = ok=false, want true")
	}
	if got.Outcome != 3 || got.Terminal {
		t.Fatalf("Resolve(1, bit(Missing)) = outcome %d terminal %v, want nonterminal outcome 3", got.Outcome, got.Terminal)
	}
	if !slices.Equal(got.Remediations, []schema.RemediationID{1, 2}) {
		t.Fatalf("Remediations = %v, want [1 2]", got.Remediations)
	}
	assertBorrows(t, "Remediations", got.Remediations, r.rules.RemediationIDs)

	setField, ok := r.remediations.Lookup(1)
	if !ok || setField != (Remediation{Kind: RemediationSetField, Field: 7, Value: 42}) {
		t.Fatalf("remediation 1 = %+v, ok=%v; want set-usage-to-standard shape", setField, ok)
	}
	addEvidence, ok := r.remediations.Lookup(2)
	if !ok || addEvidence != (Remediation{Kind: RemediationAddEvidence, EvidenceKind: 9}) {
		t.Fatalf("remediation 2 = %+v, ok=%v; want add-evidence usage-approval shape", addEvidence, ok)
	}
}

func TestResolveStaleTerminalNoRemediation(t *testing.T) {
	r := resolveFixture()
	got, ok := r.Resolve(1, truth.ReasonBit(truth.ReasonStale))
	if !ok {
		t.Fatal("Resolve(1, bit(Stale)) = ok=false, want true")
	}
	if got.Outcome != 4 || !got.Terminal {
		t.Fatalf("Resolve(1, bit(Stale)) = outcome %d terminal %v, want terminal outcome 4", got.Outcome, got.Terminal)
	}
	if got.Reason != truth.ReasonStale {
		t.Fatalf("Reason = %d, want %d", got.Reason, truth.ReasonStale)
	}
	if len(got.Remediations) != 0 {
		t.Fatalf("Remediations = %v, want empty", got.Remediations)
	}
}

func TestResolveMissingStalePrecedence(t *testing.T) {
	r := resolveFixture()
	got, ok := r.Resolve(1, truth.ReasonBit(truth.ReasonMissing)|truth.ReasonBit(truth.ReasonStale))
	if !ok {
		t.Fatal("Resolve = ok=false, want true")
	}
	if got.Outcome != 4 || got.Reason != truth.ReasonStale {
		t.Fatalf("Resolve({1,2}) = outcome %d reason %d, want outcome 4 reason Stale", got.Outcome, got.Reason)
	}
}

func TestResolvePolicyPrecedence(t *testing.T) {
	outcomes, remediations, rules := newResolverFixture()
	outcomes.Precedence = []uint8{1, 4, 5, 3}
	r, err := NewResolver(outcomes, remediations, rules)
	if err != nil {
		t.Fatalf("NewResolver = %v, want nil", err)
	}
	got, ok := r.Resolve(1, truth.ReasonBit(truth.ReasonMissing)|truth.ReasonBit(truth.ReasonStale))
	if !ok {
		t.Fatal("Resolve = ok=false, want true")
	}
	if got.Outcome != 3 || got.Reason != truth.ReasonMissing {
		t.Fatalf("Resolve({1,2}) = outcome %d reason %d, want outcome 3 reason Missing", got.Outcome, got.Reason)
	}
}

func TestResolveEqualPrecedenceLowerID(t *testing.T) {
	outcomes, remediations, _ := newResolverFixture()
	outcomes.Precedence = []uint8{1, 4, 3, 3}
	mask := truth.ReasonBit(truth.ReasonMissing) | truth.ReasonBit(truth.ReasonStale)
	assignments := []struct {
		name   string
		first  schema.OutcomeID // row 1 (Missing)
		second schema.OutcomeID // row 2 (Stale)
		reason schema.ReasonID  // row driving outcome 3
	}{
		{"lower id first", 3, 4, truth.ReasonMissing},
		{"lower id second", 4, 3, truth.ReasonStale},
	}
	for _, a := range assignments {
		t.Run(a.name, func(t *testing.T) {
			rules := ResolutionTable{
				OutcomeIDs:        []schema.OutcomeID{a.first, a.second, 4, 4, 4, 4, 4, 4, 4},
				RemediationStarts: make([]uint32, 9),
				RemediationCounts: make([]uint16, 9),
			}
			r, err := NewResolver(outcomes, remediations, rules)
			if err != nil {
				t.Fatalf("NewResolver = %v, want nil", err)
			}
			got, ok := r.Resolve(1, mask)
			if !ok {
				t.Fatal("Resolve = ok=false, want true")
			}
			if got.Outcome != 3 {
				t.Fatalf("Outcome = %d, want 3", got.Outcome)
			}
			if got.Reason != a.reason {
				t.Fatalf("Reason = %d, want %d", got.Reason, a.reason)
			}
		})
	}
}

func TestResolveSameOutcomeLowerReason(t *testing.T) {
	outcomes, remediations, _ := newResolverFixture()
	rules := ResolutionTable{
		OutcomeIDs:        []schema.OutcomeID{4, 4, 4, 4, 4, 4, 4, 4, 4},
		RemediationStarts: make([]uint32, 9),
		RemediationCounts: make([]uint16, 9),
	}
	r, err := NewResolver(outcomes, remediations, rules)
	if err != nil {
		t.Fatalf("NewResolver = %v, want nil", err)
	}
	got, ok := r.Resolve(1, truth.ReasonBit(truth.ReasonMissing)|truth.ReasonBit(truth.ReasonStale))
	if !ok {
		t.Fatal("Resolve = ok=false, want true")
	}
	if got.Outcome != 4 || got.Reason != truth.ReasonMissing {
		t.Fatalf("Resolve({1,2}) = outcome %d reason %d, want outcome 4 reason Missing", got.Outcome, got.Reason)
	}
}

func TestResolveTerminalDoesNotStopScan(t *testing.T) {
	outcomes, remediations, rules := newResolverFixture()
	outcomes.Terminal = []bool{true, true, true, true}
	r, err := NewResolver(outcomes, remediations, rules)
	if err != nil {
		t.Fatalf("NewResolver = %v, want nil", err)
	}
	got, ok := r.Resolve(1, truth.ReasonBit(truth.ReasonMissing)|truth.ReasonBit(truth.ReasonStale))
	if !ok {
		t.Fatal("Resolve = ok=false, want true")
	}
	if got.Outcome != 4 || got.Reason != truth.ReasonStale {
		t.Fatalf("Resolve({1,2}) = outcome %d reason %d, want outcome 4 reason Stale despite terminal outcome 3", got.Outcome, got.Reason)
	}
}

func TestResolveEmptyMask(t *testing.T) {
	r := resolveFixture()
	got, ok := r.Resolve(1, 0)
	if ok {
		t.Fatalf("Resolve(1, 0) = ok=true, want false")
	}
	if got.Outcome != 0 || got.Reason != 0 || got.Terminal || got.Remediations != nil {
		t.Fatalf("Resolve(1, 0) = %+v, want all-zero fields", got)
	}
}

func TestResolveInvalidMaskPanics(t *testing.T) {
	r := resolveFixture()
	for _, mask := range []truth.ReasonMask{1 << 9, 1 << 15, truth.ReasonMask(math.MaxUint16)} {
		assertPanics(t, "result: invalid reason mask", func() { r.Resolve(1, mask) })
	}
}

func TestResolveInvalidRuleSetPanics(t *testing.T) {
	r := resolveFixture()
	for _, id := range []RuleSetID{0, 2, math.MaxUint32} {
		assertPanics(t, "result: invalid rule set", func() { r.Resolve(id, truth.ReasonBit(truth.ReasonMissing)) })
	}
	assertPanics(t, "result: invalid rule set", func() { r.Resolve(2, 0) })
	assertPanics(t, "result: invalid reason mask", func() { r.Resolve(0, 1<<9) })
}

func TestResolveSecondRuleSetBlock(t *testing.T) {
	outcomes, remediations, _ := newResolverFixture()
	rows := make([]schema.OutcomeID, 2*truth.ReasonCount)
	starts := make([]uint32, 2*truth.ReasonCount)
	counts := make([]uint16, 2*truth.ReasonCount)
	for i := range rows {
		rows[i] = 4
		starts[i] = 2
	}
	rows[truth.ReasonCount] = 3
	starts[truth.ReasonCount] = 0
	counts[truth.ReasonCount] = 2
	rules := ResolutionTable{
		OutcomeIDs:        rows,
		RemediationStarts: starts,
		RemediationCounts: counts,
		RemediationIDs:    []schema.RemediationID{1, 2},
	}
	r, err := NewResolver(outcomes, remediations, rules)
	if err != nil {
		t.Fatalf("NewResolver = %v, want nil", err)
	}
	first, ok := r.Resolve(1, truth.ReasonBit(truth.ReasonMissing))
	if !ok || first.Outcome != 4 {
		t.Fatalf("Resolve(1, bit(Missing)) = %+v ok=%v, want outcome 4 from first block", first, ok)
	}
	second, ok := r.Resolve(2, truth.ReasonBit(truth.ReasonMissing))
	if !ok {
		t.Fatal("Resolve(2, bit(Missing)) = ok=false, want true")
	}
	if second.Outcome != 3 || second.Reason != truth.ReasonMissing {
		t.Fatalf("Resolve(2, bit(Missing)) = outcome %d reason %d, want outcome 3 reason Missing from second block", second.Outcome, second.Reason)
	}
	if !slices.Equal(second.Remediations, []schema.RemediationID{1, 2}) {
		t.Fatalf("Resolve(2, bit(Missing)).Remediations = %v, want [1 2]", second.Remediations)
	}
}
