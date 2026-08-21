package compile

import (
	"testing"
)

func benchmarkAssignSlots(b *testing.B, mode slotMode) {
	const rows = 8192
	p := branchingSlotProgram(rows)
	var lowerer Lowerer
	if err := lowerer.assignSlots(&p, mode); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := lowerer.assignSlots(&p, mode); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(slotPeakBytes(&p, rows)), "peak-B")
}

func BenchmarkAssignSlotsReuse(b *testing.B) {
	benchmarkAssignSlots(b, slotReuse)
}

func BenchmarkAssignSlotsRetainAll(b *testing.B) {
	benchmarkAssignSlots(b, slotRetainAll)
}
