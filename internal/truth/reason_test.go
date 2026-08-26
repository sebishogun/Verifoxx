package truth

import (
	"testing"

	"github.com/sebishogun/nornrune/internal/schema"
)

var testReasons = [...]schema.ReasonID{
	ReasonMissing,
	ReasonStale,
	ReasonUnclear,
	ReasonUnverifiable,
	ReasonWrongScope,
	ReasonWrongSubject,
	ReasonWrongTiming,
	ReasonInvalid,
	ReasonConflict,
}

func TestReasonConstants(t *testing.T) {
	if len(testReasons) != ReasonCount {
		t.Fatalf("enumerated %d reasons, want ReasonCount=%d", len(testReasons), ReasonCount)
	}
	for i, reason := range testReasons {
		if want := schema.ReasonID(i + 1); reason != want {
			t.Fatalf("reason %d = %d, want %d", i, reason, want)
		}
	}
}

func TestReasonBit(t *testing.T) {
	var bits [ReasonCount]ReasonMask
	for i, reason := range testReasons {
		bits[i] = ReasonBit(reason)
	}
	for i, bit := range bits {
		if want := ReasonMask(1) << i; bit != want {
			t.Fatalf("ReasonBit(%d) = %b, want %b", testReasons[i], bit, want)
		}
	}
}

func TestReasonBitInvalid(t *testing.T) {
	for _, reason := range []schema.ReasonID{0, ReasonConflict + 1, 1 << 20} {
		if bit := ReasonBit(reason); bit != 0 {
			t.Fatalf("ReasonBit(%d) = %b, want 0", reason, bit)
		}
	}
}

func TestAllReasonsMask(t *testing.T) {
	mask := ReasonMask(0)
	for _, reason := range testReasons {
		mask = mask.With(reason)
	}
	if mask != AllReasonsMask {
		t.Fatalf("With over all reasons = %b, want AllReasonsMask %b", mask, AllReasonsMask)
	}
	if !AllReasonsMask.Valid() {
		t.Fatal("AllReasonsMask must be valid")
	}
	if !ReasonMask(0).Valid() {
		t.Fatal("zero mask must be valid")
	}
	if ReasonMask(1 << ReasonCount).Valid() {
		t.Fatal("mask with bit ReasonCount must be invalid")
	}
}

func TestReasonMaskHas(t *testing.T) {
	mask := ReasonMask(0).With(ReasonMissing).With(ReasonConflict)
	if !mask.Has(ReasonMissing) || !mask.Has(ReasonConflict) {
		t.Fatal("inserted reasons must be present")
	}
	if mask.Has(ReasonStale) || mask.Has(ReasonInvalid) {
		t.Fatal("absent reasons must be absent")
	}
	for _, reason := range []schema.ReasonID{0, ReasonConflict + 1} {
		if mask.Has(reason) {
			t.Fatalf("Has(%d) must be false for an invalid ID", reason)
		}
	}
	if AllReasonsMask.Has(0) {
		t.Fatal("Has(0) must be false even on a full mask")
	}
	if got := ReasonMask(0).With(0); got != 0 {
		t.Fatalf("With(0) = %b, want 0", got)
	}
}
