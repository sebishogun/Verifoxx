package schema

import (
	"fmt"
	"testing"
)

func TestHashSymbolKnownVectors(t *testing.T) {
	cases := []struct {
		input string
		want  uint64
	}{
		{"", 0xcbf29ce484222325},
		{"a", 0xaf63dc4c8601ec8c},
		{"foobar", 0x85944171f73967e8},
	}
	for _, c := range cases {
		if got := HashSymbol([]byte(c.input)); got != c.want {
			t.Fatalf("HashSymbol(%q) = %#x, want %#x", c.input, got, c.want)
		}
	}
}

func TestInternAssignsSequentialIDs(t *testing.T) {
	in := NewSymbolInterner(0)
	a, err := in.Intern([]byte("alpha"))
	if err != nil || a != 1 {
		t.Fatalf("first intern = (%d, %v), want (1, nil)", a, err)
	}
	b, err := in.Intern([]byte("beta"))
	if err != nil || b != 2 {
		t.Fatalf("second intern = (%d, %v), want (2, nil)", b, err)
	}
	if in.Len() != 2 {
		t.Fatalf("Len = %d, want 2", in.Len())
	}
}

func TestDuplicateInternReturnsSameIDWithoutAppending(t *testing.T) {
	in := NewSymbolInterner(0)
	a, err := in.Intern([]byte("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	bytesBefore := in.ByteLen()
	a2, err := in.Intern([]byte("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	if a2 != a {
		t.Fatalf("duplicate intern id = %d, want %d", a2, a)
	}
	if in.Len() != 1 {
		t.Fatalf("Len after duplicate = %d, want 1", in.Len())
	}
	if in.ByteLen() != bytesBefore {
		t.Fatalf("duplicate intern appended bytes: %d -> %d", bytesBefore, in.ByteLen())
	}
}

func TestEmptyInputInternedOnce(t *testing.T) {
	in := NewSymbolInterner(0)
	a, err := in.Intern(nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := in.Intern([]byte{})
	if err != nil {
		t.Fatal(err)
	}
	if a != 1 || b != a {
		t.Fatalf("empty intern ids = (%d, %d), want (1, 1)", a, b)
	}
	if in.Len() != 1 {
		t.Fatalf("Len = %d, want 1", in.Len())
	}
	if got, ok := in.Bytes(a); !ok || len(got) != 0 {
		t.Fatalf("Bytes(empty) = (%q, %v), want empty, true", got, ok)
	}
}

func TestGrowthPreservesIDsAndData(t *testing.T) {
	in := NewSymbolInterner(4)
	const n = 200
	ids := make([]SymbolID, n)
	for i := 0; i < n; i++ {
		s := []byte(fmt.Sprintf("sym-%03d", i))
		id, err := in.Intern(s)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	if in.Len() != n {
		t.Fatalf("Len = %d, want %d", in.Len(), n)
	}
	for i := 0; i < n; i++ {
		if int(ids[i]) != i+1 {
			t.Fatalf("symbol %d id = %d, want %d", i, ids[i], i+1)
		}
		want := fmt.Sprintf("sym-%03d", i)
		got, ok := in.Bytes(ids[i])
		if !ok || string(got) != want {
			t.Fatalf("Bytes(%d) = (%q, %v), want (%q, true)", ids[i], got, ok, want)
		}
		if id, ok := in.Lookup([]byte(want)); !ok || id != ids[i] {
			t.Fatalf("Lookup(%q) = (%d, %v), want (%d, true)", want, id, ok, ids[i])
		}
	}
}

func TestMaskedSlotCollisionResolvesDistinctly(t *testing.T) {
	in := NewSymbolInterner(16) // table size 32, no growth for two symbols
	mask := uint64(len(in.ids) - 1)
	dict := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta", "eta", "theta", "iota", "kappa", "lambda", "mu"}
	var first, second string
found:
	for i := 0; i < len(dict); i++ {
		for j := i + 1; j < len(dict); j++ {
			if HashSymbol([]byte(dict[i]))&mask == HashSymbol([]byte(dict[j]))&mask {
				first, second = dict[i], dict[j]
				break found
			}
		}
	}
	if first == "" {
		t.Fatal("candidate set has no masked collision at this table size")
	}
	idA, err := in.Intern([]byte(first))
	if err != nil {
		t.Fatal(err)
	}
	idB, err := in.Intern([]byte(second))
	if err != nil {
		t.Fatal(err)
	}
	if idA == idB {
		t.Fatalf("colliding but distinct bytes %q and %q share id %d", first, second, idA)
	}
	if got, ok := in.Bytes(idA); !ok || string(got) != first {
		t.Fatalf("Bytes(%d) = (%q, %v), want (%q, true)", idA, got, ok, first)
	}
	if got, ok := in.Bytes(idB); !ok || string(got) != second {
		t.Fatalf("Bytes(%d) = (%q, %v), want (%q, true)", idB, got, ok, second)
	}
	if id, ok := in.Lookup([]byte(first)); !ok || id != idA {
		t.Fatalf("Lookup(%q) = (%d, %v), want (%d, true)", first, id, ok, idA)
	}
	if id, ok := in.Lookup([]byte(second)); !ok || id != idB {
		t.Fatalf("Lookup(%q) = (%d, %v), want (%d, true)", second, id, ok, idB)
	}
}

func TestLookupAndBytesRejectInvalidIDs(t *testing.T) {
	in := NewSymbolInterner(0)
	if _, ok := in.Lookup([]byte("anything")); ok {
		t.Fatal("Lookup on empty interner must fail")
	}
	if _, err := in.Intern([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, ok := in.Bytes(0); ok {
		t.Fatal("Bytes(0) must be invalid")
	}
	if _, ok := in.Bytes(99); ok {
		t.Fatal("Bytes(99) out of range must be invalid")
	}
}

func TestCallerBufferMutationAfterIntern(t *testing.T) {
	in := NewSymbolInterner(0)
	buf := []byte("payload")
	id, err := in.Intern(buf)
	if err != nil {
		t.Fatal(err)
	}
	buf[0] = 'X'
	got, ok := in.Bytes(id)
	if !ok || string(got) != "payload" {
		t.Fatalf("stored bytes changed after caller buffer mutation: %q", got)
	}
	if id2, ok := in.Lookup([]byte("payload")); !ok || id2 != id {
		t.Fatalf("Lookup after mutation = (%d, %v), want (%d, true)", id2, ok, id)
	}
}

func TestZeroValueByteLenSafe(t *testing.T) {
	var in Interner
	if got := in.ByteLen(); got != 0 {
		t.Fatalf("zero-value ByteLen = %d, want 0", got)
	}
}

func TestConstructorPresizesStartsAndLengths(t *testing.T) {
	in := NewSymbolInterner(64)
	if cap(in.starts) < 64 {
		t.Fatalf("cap(starts) = %d, want >= 64", cap(in.starts))
	}
	if cap(in.lengths) < 64 {
		t.Fatalf("cap(lengths) = %d, want >= 64", cap(in.lengths))
	}
}

func TestResetRestoresDeterministicIDs(t *testing.T) {
	in := NewSymbolInterner(0)
	first, err := in.Intern([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := in.Intern([]byte("y")); err != nil {
		t.Fatal(err)
	}
	in.Reset()
	if in.Len() != 0 {
		t.Fatalf("Len after Reset = %d, want 0", in.Len())
	}
	if _, ok := in.Lookup([]byte("x")); ok {
		t.Fatal("stale symbol found after Reset")
	}
	x2, err := in.Intern([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if x2 != first {
		t.Fatalf("id for x after reset = %d, want %d", x2, first)
	}
	if _, ok := in.Lookup([]byte("y")); ok {
		t.Fatal("y present after only x was re-interned")
	}
}

func TestZeroValueInternerUsable(t *testing.T) {
	var in Interner
	a, err := in.Intern([]byte("first"))
	if err != nil || a != 1 {
		t.Fatalf("zero-value intern = (%d, %v), want (1, nil)", a, err)
	}
	b, err := in.Intern([]byte("second"))
	if err != nil || b != 2 {
		t.Fatalf("zero-value second intern = (%d, %v), want (2, nil)", b, err)
	}
	if got, ok := in.Bytes(a); !ok || string(got) != "first" {
		t.Fatalf("Bytes = (%q, %v), want (first, true)", got, ok)
	}
	in.Reset()
	if in.Len() != 0 {
		t.Fatalf("Len after zero-value Reset = %d, want 0", in.Len())
	}
}

func TestResetRetainsCapacity(t *testing.T) {
	inputs := make([][]byte, 64)
	for i := range inputs {
		inputs[i] = []byte(fmt.Sprintf("sym-%02d", i))
	}
	in := NewSymbolInterner(64)
	for _, s := range inputs {
		if _, err := in.Intern(s); err != nil {
			t.Fatal(err)
		}
	}
	in.Reset()

	var internErr error
	allocs := testing.AllocsPerRun(100, func() {
		in.Reset()
		for _, s := range inputs {
			if _, err := in.Intern(s); err != nil {
				internErr = err
			}
		}
	})
	if internErr != nil {
		t.Fatal(internErr)
	}
	if allocs != 0 {
		t.Fatalf("Intern after Reset allocates %.2f allocs/run, want 0 (capacity not retained)", allocs)
	}
	if in.Len() != 64 {
		t.Fatalf("Len after reuse = %d, want 64", in.Len())
	}
}
