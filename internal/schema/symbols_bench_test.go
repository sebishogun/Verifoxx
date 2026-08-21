package schema

import (
	"fmt"
	"strconv"
	"testing"
)

// BenchmarkSymbolInternRepeated measures the steady-state dedupe path: every
// input is already interned, so Intern performs zero allocations.
func BenchmarkSymbolInternRepeated(b *testing.B) {
	inputs := make([][]byte, 128)
	for i := range inputs {
		inputs[i] = []byte(fmt.Sprintf("field-%03d", i))
	}
	in := NewSymbolInterner(len(inputs))
	for _, s := range inputs {
		if _, err := in.Intern(s); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	var sink SymbolID
	for i := 0; i < b.N; i++ {
		id, err := in.Intern(inputs[i&(len(inputs)-1)])
		if err != nil {
			b.Fatal(err)
		}
		sink += id
	}
	if sink == 0 {
		b.Fatal("sink")
	}
}

// BenchmarkSymbolInternUnique measures first-sight interning in cold batches.
// Replacing the interner every 1024 symbols includes constructor and growth
// costs without retaining an unbounded table as benchmark calibration grows.
func BenchmarkSymbolInternUnique(b *testing.B) {
	const batchSize = 1024
	inputs := make([][]byte, batchSize)
	buf := make([]byte, 0, 32)
	for i := range inputs {
		buf = buf[:0]
		buf = append(buf, "unique-symbol-"...)
		buf = strconv.AppendInt(buf, int64(i), 10)
		inputs[i] = append([]byte(nil), buf...)
	}
	var in *Interner
	b.ReportAllocs()
	b.ResetTimer()
	var sink SymbolID
	for i := 0; i < b.N; i++ {
		j := i & (batchSize - 1)
		if j == 0 {
			in = NewSymbolInterner(batchSize)
		}
		id, err := in.Intern(inputs[j])
		if err != nil {
			b.Fatal(err)
		}
		sink += id
	}
	if sink == 0 {
		b.Fatal("sink")
	}
}

// BenchmarkSymbolLookup measures allocation-free lookup on a populated table.
func BenchmarkSymbolLookup(b *testing.B) {
	inputs := make([][]byte, 256)
	for i := range inputs {
		inputs[i] = []byte(fmt.Sprintf("field-%03d", i))
	}
	in := NewSymbolInterner(len(inputs))
	for _, s := range inputs {
		if _, err := in.Intern(s); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	var sink SymbolID
	for i := 0; i < b.N; i++ {
		id, ok := in.Lookup(inputs[i&(len(inputs)-1)])
		if !ok {
			b.Fatal("lookup missed")
		}
		sink += id
	}
	if sink == 0 {
		b.Fatal("sink")
	}
}
