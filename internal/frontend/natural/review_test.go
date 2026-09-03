package natural

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	public "github.com/sebishogun/nornrune/frontend/natural"
)

func TestProposalDigestIsCanonicalAcrossBackingCapacities(t *testing.T) {
	document, proposal := reviewProposal(t)
	var reviewer Reviewer
	first, err := reviewer.ProposalDigest(document, proposal, public.DefaultLimits())
	if err != nil {
		t.Fatalf("first ProposalDigest() error = %v", err)
	}

	clone := *proposal
	clone.ItemKinds = cloneWithCapacity(proposal.ItemKinds, len(proposal.ItemKinds)+32)
	clone.ItemParents = cloneWithCapacity(proposal.ItemParents, len(proposal.ItemParents)+32)
	clone.ItemBytes = cloneWithCapacity(proposal.ItemBytes, len(proposal.ItemBytes)+128)
	clone.ItemCitationIDs = cloneWithCapacity(proposal.ItemCitationIDs, len(proposal.ItemCitationIDs)+32)
	clone.CitationQuoteBytes = cloneWithCapacity(proposal.CitationQuoteBytes, len(proposal.CitationQuoteBytes)+128)
	second, err := reviewer.ProposalDigest(document, &clone, public.DefaultLimits())
	if err != nil {
		t.Fatalf("second ProposalDigest() error = %v", err)
	}
	if first != second {
		t.Fatalf("digest differs by capacity: %x != %x", first, second)
	}

	clone.ItemBytes[0] ^= 1
	third, err := reviewer.ProposalDigest(document, &clone, public.DefaultLimits())
	if err != nil {
		t.Fatalf("mutated ProposalDigest() error = %v", err)
	}
	if third == first {
		t.Fatal("digest did not change after claim mutation")
	}
}

func TestAppendReviewIsDeterministicAndComplete(t *testing.T) {
	document, proposal := reviewProposal(t)
	var reviewer Reviewer
	first, diagnostics, err := reviewer.AppendReview(nil, document, proposal, public.DefaultLimits())
	if err != nil {
		t.Fatalf("first AppendReview() error = %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("first AppendReview() diagnostics = %#v", diagnostics)
	}
	second, diagnostics, err := reviewer.AppendReview(nil, document, proposal, public.DefaultLimits())
	if err != nil {
		t.Fatalf("second AppendReview() error = %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("second AppendReview() diagnostics = %#v", diagnostics)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("review output differs:\nfirst:  %s\nsecond: %s", first, second)
	}
	if !json.Valid(first) {
		t.Fatalf("review output is not valid JSON: %s", first)
	}
	for _, required := range [][]byte{
		[]byte(`"schema_version":1`),
		[]byte(`"provider":{"id":"fixture","version":"1"}`),
		[]byte(`"kind":"requirement"`),
		[]byte(`"kind":"restriction"`),
		[]byte(`"claim":"must remain \"local\""`),
		[]byte(`"page":0,"start":3,"end":22,"quote":"must remain \"local\""`),
	} {
		if !bytes.Contains(first, required) {
			t.Fatalf("review output %s does not contain %s", first, required)
		}
	}
}

func TestAppendReviewLabelsDocumentAndProposalDigests(t *testing.T) {
	document, proposal := reviewProposal(t)
	var reviewer Reviewer
	proposalDigest, err := reviewer.ProposalDigest(document, proposal, public.DefaultLimits())
	if err != nil {
		t.Fatalf("ProposalDigest() error = %v", err)
	}
	review, diagnostics, err := reviewer.AppendReview(nil, document, proposal, public.DefaultLimits())
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("AppendReview() = diagnostics %#v, error %v", diagnostics, err)
	}
	var artifact struct {
		DocumentSHA256 string `json:"document_sha256"`
		ProposalSHA256 string `json:"proposal_sha256"`
	}
	if err := json.Unmarshal(review, &artifact); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if want := hex.EncodeToString(document.Digest[:]); artifact.DocumentSHA256 != want {
		t.Fatalf("document_sha256 = %q, want %q", artifact.DocumentSHA256, want)
	}
	if want := hex.EncodeToString(proposalDigest[:]); artifact.ProposalSHA256 != want {
		t.Fatalf("proposal_sha256 = %q, want %q", artifact.ProposalSHA256, want)
	}
}

func TestAppendReviewPresentsBlockingConflictAndAmbiguity(t *testing.T) {
	document, proposal := reviewProposal(t)
	proposal.ItemKinds = append(proposal.ItemKinds, public.ItemKindException, public.ItemKindAmbiguity)
	proposal.ItemParents = append(proposal.ItemParents, 1, 1)
	proposal.ItemTextStarts = append(proposal.ItemTextStarts, uint32(len(proposal.ItemBytes)), uint32(len(proposal.ItemBytes)+len(`must remain "local"`)))
	proposal.ItemTextLengths = append(proposal.ItemTextLengths, uint32(len(`must remain "local"`)), uint32(len("unclear scope")))
	proposal.ItemBytes = append(proposal.ItemBytes, `must remain "local"`...)
	proposal.ItemBytes = append(proposal.ItemBytes, "unclear scope"...)
	proposal.ItemCitationStarts = append(proposal.ItemCitationStarts, uint32(len(proposal.ItemCitationIDs)), uint32(len(proposal.ItemCitationIDs)+1))
	proposal.ItemCitationCounts = append(proposal.ItemCitationCounts, 1, 1)
	proposal.ItemCitationIDs = append(proposal.ItemCitationIDs, 2, 2)

	var reviewer Reviewer
	review, diagnostics, err := reviewer.AppendReview(nil, document, proposal, public.DefaultLimits())
	if err != nil {
		t.Fatalf("AppendReview() error = %v", err)
	}
	if len(review) == 0 || !json.Valid(review) {
		t.Fatalf("AppendReview() review = %q, want valid artifact", review)
	}
	if len(diagnostics) != 2 || diagnostics[0].Code != public.CodeConflict || diagnostics[1].Code != public.CodeAmbiguity {
		t.Fatalf("AppendReview() diagnostics = %#v, want conflict and ambiguity", diagnostics)
	}
	var artifact struct {
		Diagnostics []struct {
			Code public.DiagnosticCode `json:"code"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(review, &artifact); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(artifact.Diagnostics) != 2 || artifact.Diagnostics[0].Code != public.CodeConflict || artifact.Diagnostics[1].Code != public.CodeAmbiguity {
		t.Fatalf("artifact diagnostics = %#v, want conflict and ambiguity", artifact.Diagnostics)
	}
}

func TestAppendReviewReturnsValidationDiagnostics(t *testing.T) {
	document, proposal := reviewProposal(t)
	proposal.CitationQuoteBytes[0] = 'X'
	var reviewer Reviewer
	dst := []byte("prefix")
	got, diagnostics, err := reviewer.AppendReview(dst, document, proposal, public.DefaultLimits())
	if err != nil {
		t.Fatalf("AppendReview() error = %v", err)
	}
	if !reflect.DeepEqual(got, dst) {
		t.Fatalf("output changed on diagnostics: got %q, want %q", got, dst)
	}
	if len(diagnostics) == 0 || diagnostics[0].Code != public.CodeCitation {
		t.Fatalf("diagnostics = %#v, want citation failure", diagnostics)
	}
}

func TestAppendReviewRetainsStructuralDiagnosticAtLimit(t *testing.T) {
	document, proposal := reviewProposal(t)
	appendItem := func(kind public.ItemKind, text string, citation public.CitationID) {
		proposal.ItemKinds = append(proposal.ItemKinds, kind)
		proposal.ItemParents = append(proposal.ItemParents, 1)
		proposal.ItemTextStarts = append(proposal.ItemTextStarts, uint32(len(proposal.ItemBytes)))
		proposal.ItemTextLengths = append(proposal.ItemTextLengths, uint32(len(text)))
		proposal.ItemCitationStarts = append(proposal.ItemCitationStarts, uint32(len(proposal.ItemCitationIDs)))
		proposal.ItemCitationCounts = append(proposal.ItemCitationCounts, 1)
		proposal.ItemBytes = append(proposal.ItemBytes, text...)
		proposal.ItemCitationIDs = append(proposal.ItemCitationIDs, citation)
	}
	appendItem(public.ItemKindAmbiguity, "scope is unclear", 1)
	appendItem(public.ItemKindAssumption, "malformed citation", 0)

	limits := public.DefaultLimits()
	limits.MaxDiagnostics = 1
	dst := []byte("prefix")
	var reviewer Reviewer
	got, diagnostics, err := reviewer.AppendReview(dst, document, proposal, limits)
	if err != nil {
		t.Fatalf("AppendReview() error = %v", err)
	}
	if !bytes.Equal(got, dst) {
		t.Fatalf("AppendReview() output = %q, want unchanged %q", got, dst)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != public.CodeInvalidProposal {
		t.Fatalf("AppendReview() diagnostics = %#v, want structural failure", diagnostics)
	}
}

func TestAppendReviewOutputLimitIsAtomicAndRedacted(t *testing.T) {
	document, proposal := reviewProposal(t)
	limits := public.DefaultLimits()
	limits.MaxReviewBytes = 8
	var reviewer Reviewer
	dst := []byte("prefix")
	got, diagnostics, err := reviewer.AppendReview(dst, document, proposal, limits)
	if !errors.Is(err, public.ErrLimit) {
		t.Fatalf("AppendReview() error = %v, want ErrLimit", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("AppendReview() diagnostics = %#v, want none", diagnostics)
	}
	if !bytes.Equal(got, dst) {
		t.Fatalf("output changed on limit failure: got %q, want %q", got, dst)
	}
	if bytes.Contains([]byte(err.Error()), []byte("must remain")) {
		t.Fatalf("error contains source text: %v", err)
	}
}

func BenchmarkAppendReviewWarm(b *testing.B) {
	document, proposal := reviewProposal(b)
	var reviewer Reviewer
	dst := make([]byte, 0, 4096)
	var diagnostics []public.Diagnostic
	var err error
	dst, diagnostics, err = reviewer.AppendReview(dst, document, proposal, public.DefaultLimits())
	if err != nil || len(diagnostics) != 0 {
		b.Fatalf("warm AppendReview() = diagnostics %#v, error %v", diagnostics, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		dst, diagnostics, err = reviewer.AppendReview(dst[:0], document, proposal, public.DefaultLimits())
		if err != nil || len(diagnostics) != 0 {
			b.Fatalf("AppendReview() = diagnostics %#v, error %v", diagnostics, err)
		}
	}
}

func reviewProposal(tb testing.TB) (*public.Document, *public.Proposal) {
	tb.Helper()
	source := []byte("R1 must remain \"local\".")
	document, err := public.NewDocument(source, []uint32{0}, public.DefaultLimits())
	if err != nil {
		tb.Fatalf("NewDocument() error = %v", err)
	}
	var builder public.Builder
	builder.Reset(document.Digest, public.ProviderInfo{ID: "fixture", Version: "1"}, public.DefaultLimits())
	requirementCitation, err := builder.AddCitation(0, public.Span{Start: 0, End: 2}, source[0:2])
	if err != nil {
		tb.Fatalf("AddCitation(requirement) error = %v", err)
	}
	restrictionCitation, err := builder.AddCitation(0, public.Span{Start: 3, End: 22}, source[3:22])
	if err != nil {
		tb.Fatalf("AddCitation(restriction) error = %v", err)
	}
	requirement, err := builder.AddItem(public.ItemKindRequirement, 0, []byte("R1"), []public.CitationID{requirementCitation})
	if err != nil {
		tb.Fatalf("AddItem(requirement) error = %v", err)
	}
	if _, err := builder.AddItem(public.ItemKindRestriction, requirement, []byte("must remain \"local\""), []public.CitationID{restrictionCitation}); err != nil {
		tb.Fatalf("AddItem(restriction) error = %v", err)
	}
	proposal := builder.Finish()
	return document, &proposal
}

func cloneWithCapacity[T any](source []T, capacity int) []T {
	cloned := make([]T, len(source), capacity)
	copy(cloned, source)
	return cloned
}
