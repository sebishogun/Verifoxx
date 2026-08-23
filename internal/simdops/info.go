package simdops

import "github.com/sebishogun/simd"

// Thresholds reports the pinned library guard sizes in wrapper input units.
type Thresholds struct {
	CompareU32  int
	CompareI64  int
	WordBitwise int
	BoolBitwise int
	CompressU32 int
	PackMask    int
}

// RuntimeInfo describes this process's selected SIMD backend.
type RuntimeInfo struct {
	Tier        string
	Description string
	PureGo      bool
	Thresholds  Thresholds
}

func maxKernelThreshold(names ...string) int {
	maximum := 0
	for _, name := range names {
		maximum = max(maximum, simd.KernelThreshold(name))
	}
	return maximum
}

// Runtime returns cold-path diagnostics for the selected backend and guards.
func Runtime() RuntimeInfo {
	compare := maxKernelThreshold(
		"EqualScalarMask",
		"NotEqualScalarMask",
		"LessScalarMask",
		"LessEqualScalarMask",
		"GreaterScalarMask",
		"GreaterEqualScalarMask",
	)
	bitwiseBytes := maxKernelThreshold("And", "Or", "Xor", "AndNot")
	// v1.21 has no bitwise-mask keys; its mask family shares one guard.
	mask := maxKernelThreshold("All", "Any", "Count")
	return RuntimeInfo{
		Tier:        simd.Tier(),
		Description: simd.Describe(),
		PureGo:      pureGo,
		Thresholds: Thresholds{
			CompareU32:  compare,
			CompareI64:  compare,
			WordBitwise: (bitwiseBytes + 7) / 8,
			BoolBitwise: mask,
			CompressU32: simd.KernelThreshold("Compress"),
			PackMask:    simd.KernelThreshold("MaskBits"),
		},
	}
}
