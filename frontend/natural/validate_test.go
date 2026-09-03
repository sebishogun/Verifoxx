package natural

import (
	"reflect"
	"testing"
)

func TestValidatorAcceptsExactBoundedProposal(t *testing.T) {
	document, proposal := validProposal(t)
	var validator Validator
	if diagnostics := validator.Validate(nil, document, proposal, DefaultLimits()); len(diagnostics) != 0 {
		t.Fatalf("Validate() diagnostics = %#v, want none", diagnostics)
	}
}

func TestValidatorRejectsMalformedColumns(t *testing.T) {
	document, proposal := validProposal(t)
	proposal.ItemParents = proposal.ItemParents[:len(proposal.ItemParents)-1]
	assertDiagnosticCode(t, document, proposal, CodeInvalidProposal)
}

func TestValidatorRejectsDocumentDigestMismatch(t *testing.T) {
	document, proposal := validProposal(t)
	proposal.DocumentDigest[0] ^= 0xff
	assertDiagnosticCode(t, document, proposal, CodeInvalidDocument)
}

func TestValidatorRejectsFabricatedCitation(t *testing.T) {
	document, proposal := validProposal(t)
	proposal.CitationQuoteBytes[0] = 'X'
	assertDiagnosticCode(t, document, proposal, CodeCitation)
}

func TestValidatorRejectsCitationRangesThatSplitUTF8Runes(t *testing.T) {
	source := []byte("é")
	document, err := NewDocument(source, []uint32{0}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	proposal := &Proposal{
		Provider:             ProviderInfo{ID: "fixture", Version: "1"},
		ItemKinds:            []ItemKind{ItemKindRequirement, ItemKindRestriction},
		ItemParents:          []ItemID{0, 1},
		ItemTextStarts:       []uint32{0, 2},
		ItemTextLengths:      []uint32{2, 11},
		ItemCitationStarts:   []uint32{0, 1},
		ItemCitationCounts:   []uint16{1, 1},
		ItemBytes:            []byte("R1restriction"),
		ItemCitationIDs:      []CitationID{1, 2},
		CitationPages:        []uint32{0, 0},
		CitationSourceStarts: []uint32{0, 1},
		CitationSourceEnds:   []uint32{1, 2},
		CitationQuoteStarts:  []uint32{0, 1},
		CitationQuoteLengths: []uint32{1, 1},
		CitationQuoteBytes:   append([]byte(nil), source...),
		DocumentDigest:       document.Digest,
	}
	assertDiagnosticCode(t, document, proposal, CodeCitation)
}

func TestValidatorRejectsItemRangesThatSplitUTF8Runes(t *testing.T) {
	document, proposal := validProposal(t)
	proposal.ItemBytes = []byte("éapproval")
	proposal.ItemTextStarts = []uint32{0, 1, 2}
	proposal.ItemTextLengths = []uint32{1, 1, 8}
	assertDiagnosticCode(t, document, proposal, CodeInvalidProposal)
}

func TestValidatorRejectsOverlappingCitations(t *testing.T) {
	source := []byte("abcdef")
	document, err := NewDocument(source, []uint32{0}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	var builder Builder
	builder.Reset(document.Digest, ProviderInfo{ID: "fixture", Version: "1"}, DefaultLimits())
	first, err := builder.AddCitation(0, Span{Start: 0, End: 4}, source[0:4])
	if err != nil {
		t.Fatalf("first AddCitation() error = %v", err)
	}
	second, err := builder.AddCitation(0, Span{Start: 2, End: 6}, source[2:6])
	if err != nil {
		t.Fatalf("second AddCitation() error = %v", err)
	}
	requirement, err := builder.AddItem(ItemKindRequirement, 0, []byte("requirement"), []CitationID{first})
	if err != nil {
		t.Fatalf("AddItem(requirement) error = %v", err)
	}
	if _, err := builder.AddItem(ItemKindRestriction, requirement, []byte("restriction"), []CitationID{second}); err != nil {
		t.Fatalf("AddItem(restriction) error = %v", err)
	}
	proposal := builder.Finish()
	assertDiagnosticCode(t, document, &proposal, CodeCitation)
}

func TestValidatorBoundsProviderMetadataBeforeReview(t *testing.T) {
	document, proposal := validProposal(t)
	limits := DefaultLimits()
	limits.MaxClaimBytes = uint32(len(proposal.ItemBytes) + len(proposal.Provider.ID) + len(proposal.Provider.Version) - 1)
	var validator Validator
	diagnostics := validator.Validate(nil, document, proposal, limits)
	if len(diagnostics) != 1 || diagnostics[0].Code != CodeLimit {
		t.Fatalf("Validate() diagnostics = %#v, want provider metadata limit", diagnostics)
	}
}

func TestValidatorRejectsNonOwnedCitationRanges(t *testing.T) {
	document, proposal := validProposal(t)
	proposal.ItemCitationStarts[1] = 0
	assertDiagnosticCode(t, document, proposal, CodeInvalidProposal)
}

func TestValidatorRejectsForwardParent(t *testing.T) {
	document, proposal := validProposal(t)
	proposal.ItemParents[1] = 3
	assertDiagnosticCode(t, document, proposal, CodeInvalidProposal)
}

func TestValidatorRequiresRestrictionForEveryRequirement(t *testing.T) {
	document, err := NewDocument([]byte("R1"), []uint32{0}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	var builder Builder
	builder.Reset(document.Digest, ProviderInfo{ID: "fixture", Version: "1"}, DefaultLimits())
	citation, err := builder.AddCitation(0, Span{Start: 0, End: 2}, []byte("R1"))
	if err != nil {
		t.Fatalf("AddCitation() error = %v", err)
	}
	if _, err := builder.AddItem(ItemKindRequirement, 0, []byte("R1"), []CitationID{citation}); err != nil {
		t.Fatalf("AddItem() error = %v", err)
	}
	proposal := builder.Finish()
	assertDiagnosticCode(t, document, &proposal, CodeOmittedRestriction)
}

func TestValidatorBlocksAmbiguity(t *testing.T) {
	document, proposal := validProposal(t)
	appendProposalItem(t, proposal, ItemKindAmbiguity, 1, []byte("scope is unclear"), 3)
	assertDiagnosticCode(t, document, proposal, CodeAmbiguity)
}

func TestValidatorRejectsNormalizedDuplicateSibling(t *testing.T) {
	document, proposal := validProposal(t)
	appendProposalItem(t, proposal, ItemKindRestriction, 1, []byte("  MUST   REMAIN LOCAL "), 2)
	assertDiagnosticCode(t, document, proposal, CodeDuplicate)
}

func TestValidatorRejectsRestrictionExceptionConflict(t *testing.T) {
	document, proposal := validProposal(t)
	appendProposalItem(t, proposal, ItemKindException, 1, []byte("must remain local"), 2)
	assertDiagnosticCode(t, document, proposal, CodeConflict)
}

func TestValidatorRejectsOrphanEvidence(t *testing.T) {
	document, proposal := validProposal(t)
	proposal.ItemParents[2] = 0
	assertDiagnosticCode(t, document, proposal, CodeInvalidProposal)
}

func TestValidatorCapsAndSortsDiagnostics(t *testing.T) {
	document, proposal := validProposal(t)
	proposal.CitationQuoteBytes[0] = 'X'
	appendProposalItem(t, proposal, ItemKindAmbiguity, 1, []byte("scope is unclear"), 3)
	limits := DefaultLimits()
	limits.MaxDiagnostics = 1
	var validator Validator
	diagnostics := validator.Validate(nil, document, proposal, limits)
	if got, want := len(diagnostics), 1; got != want {
		t.Fatalf("diagnostic count = %d, want %d", got, want)
	}
	if diagnostics[0].Code != CodeCitation {
		t.Fatalf("first diagnostic = %#v, want earliest citation diagnostic", diagnostics[0])
	}
}

func BenchmarkValidatorWarm(b *testing.B) {
	document, proposal := validProposal(b)
	var validator Validator
	diagnostics := make([]Diagnostic, 0, 16)
	diagnostics = validator.Validate(diagnostics, document, proposal, DefaultLimits())
	if len(diagnostics) != 0 {
		b.Fatalf("warm Validate() diagnostics = %#v", diagnostics)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		diagnostics = validator.Validate(diagnostics[:0], document, proposal, DefaultLimits())
	}
}

func validProposal(tb testing.TB) (*Document, *Proposal) {
	tb.Helper()
	source := []byte("R1 must remain local with approval.")
	document, err := NewDocument(source, []uint32{0}, DefaultLimits())
	if err != nil {
		tb.Fatalf("NewDocument() error = %v", err)
	}
	var builder Builder
	builder.Reset(document.Digest, ProviderInfo{ID: "fixture", Version: "1"}, DefaultLimits())
	requirementCitation, err := builder.AddCitation(0, Span{Start: 0, End: 2}, source[0:2])
	if err != nil {
		tb.Fatalf("AddCitation(requirement) error = %v", err)
	}
	restrictionCitation, err := builder.AddCitation(0, Span{Start: 3, End: 20}, source[3:20])
	if err != nil {
		tb.Fatalf("AddCitation(restriction) error = %v", err)
	}
	evidenceCitation, err := builder.AddCitation(0, Span{Start: 26, End: 34}, source[26:34])
	if err != nil {
		tb.Fatalf("AddCitation(evidence) error = %v", err)
	}
	requirement, err := builder.AddItem(ItemKindRequirement, 0, []byte("R1"), []CitationID{requirementCitation})
	if err != nil {
		tb.Fatalf("AddItem(requirement) error = %v", err)
	}
	if _, err := builder.AddItem(ItemKindRestriction, requirement, []byte("must remain local"), []CitationID{restrictionCitation}); err != nil {
		tb.Fatalf("AddItem(restriction) error = %v", err)
	}
	if _, err := builder.AddItem(ItemKindEvidence, requirement, []byte("approval"), []CitationID{evidenceCitation}); err != nil {
		tb.Fatalf("AddItem(evidence) error = %v", err)
	}
	proposal := builder.Finish()
	return document, &proposal
}

func appendProposalItem(tb testing.TB, proposal *Proposal, kind ItemKind, parent ItemID, text []byte, citation CitationID) {
	tb.Helper()
	proposal.ItemKinds = append(proposal.ItemKinds, kind)
	proposal.ItemParents = append(proposal.ItemParents, parent)
	proposal.ItemTextStarts = append(proposal.ItemTextStarts, uint32(len(proposal.ItemBytes)))
	proposal.ItemTextLengths = append(proposal.ItemTextLengths, uint32(len(text)))
	proposal.ItemCitationStarts = append(proposal.ItemCitationStarts, uint32(len(proposal.ItemCitationIDs)))
	proposal.ItemCitationCounts = append(proposal.ItemCitationCounts, 1)
	proposal.ItemBytes = append(proposal.ItemBytes, text...)
	proposal.ItemCitationIDs = append(proposal.ItemCitationIDs, citation)
}

func assertDiagnosticCode(tb testing.TB, document *Document, proposal *Proposal, want DiagnosticCode) {
	tb.Helper()
	var validator Validator
	diagnostics := validator.Validate(nil, document, proposal, DefaultLimits())
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == want {
			return
		}
	}
	tb.Fatalf("Validate() diagnostics = %#v, want code %d", diagnostics, want)
}

func TestDiagnosticLayoutIsPointerless(t *testing.T) {
	want := Diagnostic{Span: Span{Start: 1, End: 2}, Item: 3, Citation: 4, Code: CodeCitation}
	if got := want; !reflect.DeepEqual(got, want) {
		t.Fatalf("Diagnostic copy = %#v, want %#v", got, want)
	}
}

func TestNormalizedHashUsesFNV1aOffsetBasis(t *testing.T) {
	const (
		offset = uint64(14695981039346656037)
		prime  = uint64(1099511628211)
	)
	want := offset
	for _, value := range []byte{1, 0, 0, 0, byte(ItemKindRestriction), 'a', ' ', 'b'} {
		want = (want ^ uint64(value)) * prime
	}
	if got := normalizedHash(1, ItemKindRestriction, []byte(" A  B "), true); got != want {
		t.Fatalf("normalizedHash() = %x, want %x", got, want)
	}
}
