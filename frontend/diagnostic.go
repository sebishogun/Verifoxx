package frontend

import "slices"

// Diagnostic is a bounded, pointerless frontend result. Source excerpts remain
// caller-owned and parser objects never cross this boundary.
type Diagnostic struct {
	Span     Span           `json:"span"`
	Row      uint32         `json:"row"`
	Field    FieldID        `json:"field,omitempty"`
	Language Language       `json:"language"`
	Code     DiagnosticCode `json:"code"`
}

// SortDiagnostics orders diagnostics by exact span, code, and semantic row.
func SortDiagnostics(diagnostics []Diagnostic) {
	slices.SortStableFunc(diagnostics, func(left, right Diagnostic) int {
		if left.Span.Start != right.Span.Start {
			return compareUint32(left.Span.Start, right.Span.Start)
		}
		if left.Span.End != right.Span.End {
			return compareUint32(left.Span.End, right.Span.End)
		}
		if left.Code != right.Code {
			return compareUint8(uint8(left.Code), uint8(right.Code))
		}
		if left.Row != right.Row {
			return compareUint32(left.Row, right.Row)
		}
		if left.Language != right.Language {
			return compareUint8(uint8(left.Language), uint8(right.Language))
		}
		return compareUint32(uint32(left.Field), uint32(right.Field))
	})
}

func compareUint32(left, right uint32) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func compareUint8(left, right uint8) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}
