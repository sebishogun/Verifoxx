package natural

import (
	"testing"
	"unicode/utf8"
)

func FuzzProposalValidation(f *testing.F) {
	f.Add([]byte("R1 must remain local"), uint8(0))
	f.Add([]byte("世界"), uint8(3))
	f.Fuzz(func(t *testing.T, source []byte, mutation uint8) {
		if len(source) == 0 || !utf8.Valid(source) {
			return
		}
		document, err := NewDocument(source, []uint32{0}, DefaultLimits())
		if err != nil {
			return
		}
		var builder Builder
		builder.Reset(document.Digest, ProviderInfo{ID: "fuzz", Version: "1"}, DefaultLimits())
		citation, err := builder.AddCitation(0, Span{Start: 0, End: uint32(len(source))}, source)
		if err != nil {
			return
		}
		requirement, err := builder.AddItem(ItemKindRequirement, 0, []byte("R1"), []CitationID{citation})
		if err != nil {
			return
		}
		if _, err := builder.AddItem(ItemKindRestriction, requirement, []byte("restricted"), []CitationID{citation}); err != nil {
			return
		}
		proposal := builder.Finish()
		switch mutation % 6 {
		case 0:
			proposal.DocumentDigest[0] ^= 1
		case 1:
			proposal.CitationSourceEnds[0]++
		case 2:
			proposal.CitationQuoteLengths[0]++
		case 3:
			proposal.ItemCitationStarts[1] = 0
		case 4:
			proposal.ItemParents[1] = 2
		case 5:
			proposal.ItemKinds[1] = ItemKind(255)
		}
		limits := DefaultLimits()
		limits.MaxDiagnostics = 8
		var validator Validator
		diagnostics := validator.Validate(nil, document, &proposal, limits)
		if len(diagnostics) > int(limits.MaxDiagnostics) {
			t.Fatalf("diagnostic count = %d, max %d", len(diagnostics), limits.MaxDiagnostics)
		}
	})
}
