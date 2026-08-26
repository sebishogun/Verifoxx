package eval

import (
	"github.com/sebishogun/nornrune/internal/schema"
	"github.com/sebishogun/nornrune/internal/truth"
)

// ReasonPlanes is a non-owning reason-major view for one evaluator scratch
// slot. It contains one row bitplane for each engine reason.
type ReasonPlanes struct {
	Words []uint64
}

// Plane returns the row bitplane for reason. Invalid IDs or storage shapes are
// evaluator defects and panic.
func (p ReasonPlanes) Plane(reason schema.ReasonID, rows uint32) []uint64 {
	words := truth.WordCount(rows)
	if reason < truth.ReasonMissing || reason > truth.ReasonConflict ||
		uint64(len(p.Words)) != uint64(truth.ReasonCount)*uint64(words) {
		panic("eval: invalid reason plane")
	}
	return p.plane(reason, words)
}

func (p ReasonPlanes) plane(reason schema.ReasonID, words int) []uint64 {
	start := int(uint64(reason-1) * uint64(words))
	end := start + words
	return p.Words[start:end:end]
}

func resetLeafOutputs(dst truth.Planes, reasons ReasonPlanes, rows uint32) int {
	words := truth.WordCount(rows)
	if len(dst.Positive) != words || len(dst.Negative) != words ||
		uint64(len(reasons.Words)) != uint64(truth.ReasonCount)*uint64(words) {
		panic("eval: invalid leaf output shape")
	}
	clear(dst.Positive)
	clear(dst.Negative)
	clear(reasons.Words)
	return words
}

func leafWordMask(word, words int, rows uint32) uint64 {
	if word+1 != words || rows&63 == 0 {
		return ^uint64(0)
	}
	return uint64(1)<<(rows&63) - 1
}
