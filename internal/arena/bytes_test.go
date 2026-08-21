package arena

import (
	"errors"
	"math"
	"testing"
)

func TestAppendAndBytes(t *testing.T) {
	a := New(0)
	r, err := a.Append([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Offset != 0 || r.Length != 5 {
		t.Fatalf("first ref = %+v, want {0 5}", r)
	}
	if got := string(a.Bytes(r)); got != "hello" {
		t.Fatalf("Bytes(first) = %q, want hello", got)
	}
	r2, err := a.Append([]byte("world"))
	if err != nil {
		t.Fatal(err)
	}
	if r2.Offset != 5 || r2.Length != 5 {
		t.Fatalf("second ref = %+v, want {5 5}", r2)
	}
	if got := string(a.Bytes(r2)); got != "world" {
		t.Fatalf("Bytes(second) = %q, want world", got)
	}
	if a.Len() != 10 {
		t.Fatalf("Len = %d, want 10", a.Len())
	}
}

func TestAppendEmpty(t *testing.T) {
	a := New(0)
	r, err := a.Append(nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Length != 0 {
		t.Fatalf("empty append ref = %+v, want length 0", r)
	}
	if a.Len() != 0 {
		t.Fatalf("Len after empty append = %d, want 0", a.Len())
	}
	if got := a.Bytes(r); len(got) != 0 {
		t.Fatalf("Bytes(empty) = %q, want empty", got)
	}
}

func TestRefsStableAcrossGrowth(t *testing.T) {
	a := New(2)
	first, err := a.Append([]byte("alpha"))
	if err != nil {
		t.Fatal(err)
	}
	var last Ref
	for i := 0; i < 1000; i++ {
		r, err := a.Append([]byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		last = r
	}
	if got := string(a.Bytes(first)); got != "alpha" {
		t.Fatalf("first ref corrupted after growth: %q", got)
	}
	if got := string(a.Bytes(last)); got != "x" {
		t.Fatalf("last ref wrong after growth: %q", got)
	}
	if a.Len() != 1005 {
		t.Fatalf("Len = %d, want 1005", a.Len())
	}
}

func TestBytesRejectsInvalidRefs(t *testing.T) {
	a := New(0)
	if _, err := a.Append([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if a.Bytes(Ref{0, 4}) != nil {
		t.Fatal("range past end must be nil")
	}
	if a.Bytes(Ref{1, 3}) != nil {
		t.Fatal("range past end must be nil")
	}
	if a.Bytes(Ref{3, 1}) != nil {
		t.Fatal("offset at end must be nil")
	}
	if a.Bytes(Ref{math.MaxUint32, 1}) != nil {
		t.Fatal("offset+length overflow must be nil")
	}
	if a.Bytes(Ref{0, math.MaxUint32}) != nil {
		t.Fatal("huge length must be nil")
	}
	if a.Bytes(Ref{0, 0}) == nil {
		t.Fatal("empty range at start must be valid")
	}
}

func TestZeroValueAppendNonEmpty(t *testing.T) {
	var a ByteArena
	r, err := a.Append([]byte("hello"))
	if err != nil {
		t.Fatalf("zero-value non-empty append failed: %v", err)
	}
	if r.Offset != 0 || r.Length != 5 {
		t.Fatalf("ref = %+v, want {0 5}", r)
	}
	if got := string(a.Bytes(r)); got != "hello" {
		t.Fatalf("Bytes = %q, want hello", got)
	}
	if a.Len() != 5 {
		t.Fatalf("Len = %d, want 5", a.Len())
	}
}

func TestZeroValueAppendEmpty(t *testing.T) {
	var a ByteArena
	r, err := a.Append(nil)
	if err != nil {
		t.Fatalf("zero-value empty append failed: %v", err)
	}
	if r.Length != 0 {
		t.Fatalf("empty ref = %+v, want length 0", r)
	}
	if a.Len() != 0 {
		t.Fatalf("Len = %d, want 0", a.Len())
	}
	if got := a.Bytes(r); got == nil || len(got) != 0 {
		t.Fatalf("Bytes(empty) = %#v, want non-nil empty view", got)
	}
}

func TestZeroValueGrow(t *testing.T) {
	var a ByteArena
	a.Grow(64)
	if a.Cap() < 64 {
		t.Fatalf("Cap after zero-value Grow = %d, want >= 64", a.Cap())
	}
}

func TestZeroValueEmptyAppendNoAlloc(t *testing.T) {
	var a ByteArena
	var appendErr error
	allocs := testing.AllocsPerRun(1000, func() {
		if _, err := a.Append(nil); err != nil {
			appendErr = err
		}
	})
	if appendErr != nil {
		t.Fatal(appendErr)
	}
	if allocs != 0 {
		t.Fatalf("empty append on zero value allocates %.2f allocs/run, want 0", allocs)
	}
}

func TestAppendOverflowRejectsWithoutMutation(t *testing.T) {
	a := &ByteArena{maxBytes: 10}
	if _, err := a.Append([]byte("0123456789")); err != nil {
		t.Fatalf("exact-capacity append failed: %v", err)
	}
	if _, err := a.Append([]byte("x")); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("overflow err = %v, want ErrTooLarge", err)
	}
	if a.Len() != 10 {
		t.Fatalf("overflow mutated arena: Len = %d, want 10", a.Len())
	}
	if got := string(a.Bytes(Ref{0, 10})); got != "0123456789" {
		t.Fatalf("existing bytes corrupted: %q", got)
	}
}

func TestGrowCapacityHint(t *testing.T) {
	a := New(64)
	if a.Cap() < 64 {
		t.Fatalf("Cap = %d, want >= 64", a.Cap())
	}
	if _, err := a.Append([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	a.Grow(256)
	if a.Cap() < 256 {
		t.Fatalf("Cap after Grow = %d, want >= 256", a.Cap())
	}
	if got := string(a.Bytes(Ref{0, 3})); got != "abc" {
		t.Fatalf("bytes lost by Grow: %q", got)
	}
}

func TestResetRetainsCapacity(t *testing.T) {
	a := New(64)
	for i := 0; i < 40; i++ {
		if _, err := a.Append([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	a.Reset()
	if a.Len() != 0 {
		t.Fatalf("Len after Reset = %d, want 0", a.Len())
	}

	var appendErr error
	allocs := testing.AllocsPerRun(100, func() {
		a.Reset()
		for i := 0; i < 40; i++ {
			if _, err := a.Append([]byte("x")); err != nil {
				appendErr = err
			}
		}
	})
	if appendErr != nil {
		t.Fatal(appendErr)
	}
	if allocs != 0 {
		t.Fatalf("Append after Reset allocates %.2f allocs/run, want 0 (capacity not retained)", allocs)
	}
	if a.Len() != 40 {
		t.Fatalf("Len after reuse = %d, want 40", a.Len())
	}
}
