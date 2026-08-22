package eval

import (
	"slices"
	"strconv"
	"testing"

	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

func requirePanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("call did not panic")
		}
	}()
	fn()
}

func TestReasonPlanesSelectsDenseReasonRanges(t *testing.T) {
	for _, rows := range []uint32{0, 1, 63, 64, 65} {
		t.Run(strconv.FormatUint(uint64(rows), 10), func(t *testing.T) {
			words := truth.WordCount(rows)
			storage := make([]uint64, truth.ReasonCount*words)
			planes := ReasonPlanes{Words: storage}
			for reason := schema.ReasonID(1); reason <= truth.ReasonConflict; reason++ {
				plane := planes.Plane(reason, rows)
				if len(plane) != words || cap(plane) != words {
					t.Fatalf("reason %d shape = len %d cap %d, want %d", reason, len(plane), cap(plane), words)
				}
				if words != 0 {
					plane[0] = uint64(reason)
				}
			}
			for reason := schema.ReasonID(1); words != 0 && reason <= truth.ReasonConflict; reason++ {
				if got := storage[int(reason-1)*words]; got != uint64(reason) {
					t.Fatalf("reason %d starts with %d", reason, got)
				}
			}
		})
	}
}

func TestReasonPlanesRejectsInvalidShapes(t *testing.T) {
	rows := uint32(65)
	words := truth.WordCount(rows)
	valid := ReasonPlanes{Words: make([]uint64, truth.ReasonCount*words)}
	requirePanic(t, func() { valid.Plane(0, rows) })
	requirePanic(t, func() { valid.Plane(truth.ReasonConflict+1, rows) })
	requirePanic(t, func() {
		ReasonPlanes{Words: make([]uint64, truth.ReasonCount*words-1)}.Plane(truth.ReasonMissing, rows)
	})
}

func TestResetLeafOutputsClearsPoisonedStorage(t *testing.T) {
	for _, rows := range []uint32{0, 1, 63, 64, 65} {
		words := truth.WordCount(rows)
		positive := slices.Repeat([]uint64{^uint64(0)}, words)
		negative := slices.Repeat([]uint64{^uint64(0)}, words)
		reasonWords := slices.Repeat([]uint64{^uint64(0)}, truth.ReasonCount*words)
		gotWords := resetLeafOutputs(
			truth.Planes{Positive: positive, Negative: negative},
			ReasonPlanes{Words: reasonWords},
			rows,
		)
		if gotWords != words {
			t.Fatalf("reset words = %d, want %d", gotWords, words)
		}
		for _, storage := range [][]uint64{positive, negative, reasonWords} {
			if slices.ContainsFunc(storage, func(word uint64) bool { return word != 0 }) {
				t.Fatalf("reset left dirty words: %#x", storage)
			}
		}
	}
}

func TestResetLeafOutputsValidatesBeforeMutation(t *testing.T) {
	positive := []uint64{1}
	negative := []uint64{2}
	reasonWords := slices.Repeat([]uint64{3}, truth.ReasonCount)
	requirePanic(t, func() {
		resetLeafOutputs(
			truth.Planes{Positive: positive, Negative: negative[:0]},
			ReasonPlanes{Words: reasonWords},
			1,
		)
	})
	if positive[0] != 1 || negative[0] != 2 || !slices.Equal(reasonWords, slices.Repeat([]uint64{3}, truth.ReasonCount)) {
		t.Fatal("shape failure mutated output")
	}
}
