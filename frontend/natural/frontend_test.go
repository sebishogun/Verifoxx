package natural

import (
	"crypto/sha256"
	"errors"
	"reflect"
	"testing"
)

func TestStableEnumValues(t *testing.T) {
	itemKinds := []ItemKind{
		ItemKindRequirement,
		ItemKindApplicability,
		ItemKindAssertion,
		ItemKindEvidence,
		ItemKindException,
		ItemKindRestriction,
		ItemKindAssumption,
		ItemKindAmbiguity,
	}
	for row, kind := range itemKinds {
		if got, want := uint8(kind), uint8(row+1); got != want {
			t.Fatalf("ItemKind value at row %d = %d, want %d", row, got, want)
		}
		if !kind.Valid() {
			t.Fatalf("ItemKind %d is not valid", kind)
		}
	}
	if ItemKindInvalid.Valid() || ItemKind(9).Valid() {
		t.Fatal("out-of-range ItemKind is valid")
	}
	semanticKinds := []SemanticKind{
		SemanticKindRequirement,
		SemanticKindApplicability,
		SemanticKindClause,
		SemanticKindAssertion,
		SemanticKindEvidence,
		SemanticKindResolution,
		SemanticKindRemediation,
		SemanticKindExplanation,
		SemanticKindOutcome,
		SemanticKindAssumption,
	}
	for row, kind := range semanticKinds {
		if got, want := uint8(kind), uint8(row+1); got != want || !kind.Valid() {
			t.Fatalf("SemanticKind at row %d = %d (valid %t), want %d", row, got, kind.Valid(), want)
		}
	}
	if SemanticKindInvalid.Valid() || SemanticKind(11).Valid() {
		t.Fatal("out-of-range SemanticKind is valid")
	}

	codes := []DiagnosticCode{
		CodeInvalidDocument,
		CodeInvalidProposal,
		CodeLimit,
		CodeCitation,
		CodeDuplicate,
		CodeConflict,
		CodeAmbiguity,
		CodeOmittedRestriction,
		CodeInvalidDraft,
		CodeInvalidToken,
		CodeExpiredToken,
	}
	for row, code := range codes {
		if got, want := uint8(code), uint8(row+1); got != want {
			t.Fatalf("DiagnosticCode value at row %d = %d, want %d", row, got, want)
		}
		if !code.Valid() {
			t.Fatalf("DiagnosticCode %d is not valid", code)
		}
	}
}

func TestNewDocumentOwnsSourcePagesAndDigest(t *testing.T) {
	source := []byte("first page\nsecond page")
	pages := []uint32{0, 11}
	document, err := NewDocument(source, pages, DefaultLimits())
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}

	wantDigest := sha256.Sum256(source)
	if document.Digest != wantDigest {
		t.Fatalf("Digest = %x, want %x", document.Digest, wantDigest)
	}
	source[0] = 'X'
	pages[1] = 1
	if got, want := string(document.Source), "first page\nsecond page"; got != want {
		t.Fatalf("owned Source = %q, want %q", got, want)
	}
	if got, want := document.PageStarts[1], uint32(11); got != want {
		t.Fatalf("owned PageStarts[1] = %d, want %d", got, want)
	}
}

func TestNewDocumentRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		source []byte
		pages  []uint32
	}{
		{name: "empty source", pages: []uint32{0}},
		{name: "invalid UTF-8", source: []byte{0xff}, pages: []uint32{0}},
		{name: "no pages", source: []byte("source")},
		{name: "nonzero first page", source: []byte("source"), pages: []uint32{1}},
		{name: "duplicate page", source: []byte("source"), pages: []uint32{0, 0}},
		{name: "unsorted page", source: []byte("source"), pages: []uint32{0, 4, 2}},
		{name: "page past source", source: []byte("source"), pages: []uint32{0, 7}},
		{name: "page splits UTF-8 rune", source: []byte("éx"), pages: []uint32{0, 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewDocument(test.source, test.pages, DefaultLimits())
			if !errors.Is(err, ErrInvalidDocument) {
				t.Fatalf("NewDocument() error = %v, want ErrInvalidDocument", err)
			}
		})
	}
}

func TestNewDocumentAppliesLimitsBeforeOwnershipCopies(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxSourceBytes = 3
	if _, err := NewDocument([]byte("four"), []uint32{0}, limits); !errors.Is(err, ErrLimit) {
		t.Fatalf("source limit error = %v, want ErrLimit", err)
	}

	limits = DefaultLimits()
	limits.MaxPages = 1
	if _, err := NewDocument([]byte("two pages"), []uint32{0, 4}, limits); !errors.Is(err, ErrLimit) {
		t.Fatalf("page limit error = %v, want ErrLimit", err)
	}
}

func TestBuilderAppendsOwnedProposal(t *testing.T) {
	source := []byte("R1 must remain local.")
	document, err := NewDocument(source, []uint32{0}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}

	var builder Builder
	builder.Reset(document.Digest, ProviderInfo{ID: "fixture", Version: "1"}, DefaultLimits())
	quote := append([]byte(nil), source[:2]...)
	citation, err := builder.AddCitation(0, Span{Start: 0, End: 2}, quote)
	if err != nil {
		t.Fatalf("AddCitation() error = %v", err)
	}
	if citation != 1 {
		t.Fatalf("CitationID = %d, want 1", citation)
	}

	requirement, err := builder.AddItem(ItemKindRequirement, 0, []byte("R1"), []CitationID{citation})
	if err != nil {
		t.Fatalf("AddItem(requirement) error = %v", err)
	}
	restrictionText := []byte("must remain local")
	restriction, err := builder.AddItem(ItemKindRestriction, requirement, restrictionText, []CitationID{citation})
	if err != nil {
		t.Fatalf("AddItem(restriction) error = %v", err)
	}
	if requirement != 1 || restriction != 2 {
		t.Fatalf("ItemIDs = %d, %d, want 1, 2", requirement, restriction)
	}

	proposal := builder.Finish()
	quote[0] = 'X'
	restrictionText[0] = 'X'
	builder.Reset([sha256.Size]byte{}, ProviderInfo{ID: "other"}, DefaultLimits())

	if got, want := proposal.Provider, (ProviderInfo{ID: "fixture", Version: "1"}); got != want {
		t.Fatalf("Provider = %+v, want %+v", got, want)
	}
	if got, want := string(proposal.CitationQuoteBytes), "R1"; got != want {
		t.Fatalf("CitationQuoteBytes = %q, want %q", got, want)
	}
	if got, want := string(proposal.ItemBytes), "R1must remain local"; got != want {
		t.Fatalf("ItemBytes = %q, want %q", got, want)
	}
	if got, want := proposal.ItemCitationIDs, []CitationID{1, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ItemCitationIDs = %v, want %v", got, want)
	}
	if got, want := proposal.ItemParents, []ItemID{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ItemParents = %v, want %v", got, want)
	}
}

func TestBuilderRejectsInvalidUTF8Rows(t *testing.T) {
	var builder Builder
	builder.Reset([sha256.Size]byte{}, ProviderInfo{ID: "fixture", Version: "1"}, DefaultLimits())
	if _, err := builder.AddCitation(0, Span{Start: 0, End: 1}, []byte{0xc3}); !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("AddCitation() error = %v, want ErrInvalidProposal", err)
	}
	citation, err := builder.AddCitation(0, Span{Start: 0, End: 1}, []byte("R"))
	if err != nil {
		t.Fatalf("valid AddCitation() error = %v", err)
	}
	if _, err := builder.AddItem(ItemKindRequirement, 0, []byte{0xc3}, []CitationID{citation}); !errors.Is(err, ErrInvalidProposal) {
		t.Fatalf("AddItem() error = %v, want ErrInvalidProposal", err)
	}
}

func TestBuilderFailedAppendIsAtomic(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxItems = 1
	limits.MaxCitations = 1
	limits.MaxCitationEdges = 1
	limits.MaxClaimBytes = 2
	limits.MaxQuoteBytes = 2

	var builder Builder
	builder.Reset(sha256.Sum256([]byte("R1")), ProviderInfo{ID: "fixture"}, limits)
	citation, err := builder.AddCitation(0, Span{Start: 0, End: 2}, []byte("R1"))
	if err != nil {
		t.Fatalf("AddCitation() error = %v", err)
	}
	if _, err := builder.AddItem(ItemKindRequirement, 0, []byte("R1"), []CitationID{citation}); err != nil {
		t.Fatalf("AddItem() error = %v", err)
	}
	want := builder.Finish()

	if _, err := builder.AddCitation(0, Span{Start: 0, End: 1}, []byte("R")); !errors.Is(err, ErrLimit) {
		t.Fatalf("second AddCitation() error = %v, want ErrLimit", err)
	}
	if _, err := builder.AddItem(ItemKindRestriction, 1, []byte("X"), []CitationID{citation}); !errors.Is(err, ErrLimit) {
		t.Fatalf("second AddItem() error = %v, want ErrLimit", err)
	}
	if got := builder.Finish(); !reflect.DeepEqual(got, want) {
		t.Fatalf("proposal changed after failed append:\n got: %#v\nwant: %#v", got, want)
	}
}
