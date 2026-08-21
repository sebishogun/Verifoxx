package ast

// SourceSpan is a half-open byte range [Start, End) in Document.InputBytes.
// It contains no pointer and is safe to store in flat columns.
type SourceSpan struct {
	Start uint32
	End   uint32
}

func (s SourceSpan) valid(inputLen int) bool {
	return s.Start <= s.End && uint64(s.End) <= uint64(inputLen)
}
