package result

import (
	"math"
	"testing"

	"github.com/sebishogun/verifoxx/internal/schema"
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
