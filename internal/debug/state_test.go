package debug

import (
	"math"
	"testing"

	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

func TestSlotWordOffsetsPreserveFullWidth(t *testing.T) {
	const rows = uint32(math.MaxUint32)
	instruction := schema.InstructionID(math.MaxUint32)
	words := (uint64(rows) + 63) >> 6
	wantTruth := uint64(instruction-1) * 2 * words
	wantReason := uint64(instruction-1) * truth.ReasonCount * words

	gotTruth, gotReason := slotWordOffsets(instruction, rows)
	if gotTruth != wantTruth || gotReason != wantReason {
		t.Fatalf("slotWordOffsets() = (%d,%d), want (%d,%d)", gotTruth, gotReason, wantTruth, wantReason)
	}
	if gotTruth <= math.MaxUint32 || gotReason <= math.MaxUint32 {
		t.Fatalf("large offsets unexpectedly fit uint32: (%d,%d)", gotTruth, gotReason)
	}
}
