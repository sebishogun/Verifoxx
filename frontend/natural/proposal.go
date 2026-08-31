package natural

import "crypto/sha256"

// ItemID and CitationID are one-based proposal identifiers.
type ItemID uint32
type CitationID uint32

// ProviderInfo identifies the extraction implementation and version.
type ProviderInfo struct {
	ID      string
	Version string
}

// ReviewedDraft is reviewer-owned native policy source plus CSR provenance
// from each native requirement to proposal items.
type ReviewedDraft struct {
	PolicySource         []byte
	RequirementIDs       []uint32
	MappingStarts        []uint32
	MappingCounts        []uint16
	MappingProposalItems []ItemID
}

// Signer authenticates one fixed approval-message digest.
type Signer interface {
	Sign(message []byte) ([]byte, error)
}

// Verifier authenticates one fixed approval-message digest and signature.
type Verifier interface {
	Verify(message, signature []byte) error
}

// ApprovalToken binds one reviewed proposal/draft pair to a reviewer and
// bounded validity interval.
type ApprovalToken struct {
	Reviewer  []byte
	Signature []byte

	ProposalDigest [sha256.Size]byte
	DraftDigest    [sha256.Size]byte
	IssuedUnix     int64
	ExpiresUnix    int64
	SchemaVersion  uint16
}

// Proposal is untrusted, non-executable provider output stored in parallel
// typed columns and shared CSR citation edges.
type Proposal struct {
	Provider ProviderInfo

	ItemKinds          []ItemKind
	ItemParents        []ItemID
	ItemTextStarts     []uint32
	ItemTextLengths    []uint32
	ItemCitationStarts []uint32
	ItemCitationCounts []uint16
	ItemBytes          []byte
	ItemCitationIDs    []CitationID

	CitationPages        []uint32
	CitationSourceStarts []uint32
	CitationSourceEnds   []uint32
	CitationQuoteStarts  []uint32
	CitationQuoteLengths []uint32
	CitationQuoteBytes   []byte

	DocumentDigest [sha256.Size]byte
}

// Builder appends one proposal atomically. It is reusable and not safe for
// concurrent use.
type Builder struct {
	proposal Proposal
	limits   Limits
}

// Reset clears prior rows while retaining builder-owned capacity.
func (builder *Builder) Reset(digest [sha256.Size]byte, provider ProviderInfo, limits Limits) {
	proposal := &builder.proposal
	proposal.ItemKinds = proposal.ItemKinds[:0]
	proposal.ItemParents = proposal.ItemParents[:0]
	proposal.ItemTextStarts = proposal.ItemTextStarts[:0]
	proposal.ItemTextLengths = proposal.ItemTextLengths[:0]
	proposal.ItemCitationStarts = proposal.ItemCitationStarts[:0]
	proposal.ItemCitationCounts = proposal.ItemCitationCounts[:0]
	proposal.ItemBytes = proposal.ItemBytes[:0]
	proposal.ItemCitationIDs = proposal.ItemCitationIDs[:0]
	proposal.CitationPages = proposal.CitationPages[:0]
	proposal.CitationSourceStarts = proposal.CitationSourceStarts[:0]
	proposal.CitationSourceEnds = proposal.CitationSourceEnds[:0]
	proposal.CitationQuoteStarts = proposal.CitationQuoteStarts[:0]
	proposal.CitationQuoteLengths = proposal.CitationQuoteLengths[:0]
	proposal.CitationQuoteBytes = proposal.CitationQuoteBytes[:0]
	proposal.Provider = provider
	proposal.DocumentDigest = digest
	builder.limits = limits
}

// AddCitation appends one exact source quote after preflighting every column.
func (builder *Builder) AddCitation(page uint32, span Span, quote []byte) (CitationID, error) {
	proposal := &builder.proposal
	if overLimit(len(proposal.CitationPages)+1, builder.limits.MaxCitations) ||
		overLimit(len(proposal.CitationQuoteBytes)+len(quote), builder.limits.MaxQuoteBytes) ||
		uint64(len(proposal.CitationQuoteBytes))+uint64(len(quote)) > uint64(^uint32(0)) {
		return 0, ErrLimit
	}
	if len(quote) == 0 || span.End <= span.Start || uint64(len(quote)) > uint64(^uint32(0)) {
		return 0, ErrInvalidProposal
	}

	start := uint32(len(proposal.CitationQuoteBytes))
	proposal.CitationPages = append(proposal.CitationPages, page)
	proposal.CitationSourceStarts = append(proposal.CitationSourceStarts, span.Start)
	proposal.CitationSourceEnds = append(proposal.CitationSourceEnds, span.End)
	proposal.CitationQuoteStarts = append(proposal.CitationQuoteStarts, start)
	proposal.CitationQuoteLengths = append(proposal.CitationQuoteLengths, uint32(len(quote)))
	proposal.CitationQuoteBytes = append(proposal.CitationQuoteBytes, quote...)
	return CitationID(len(proposal.CitationPages)), nil
}

// AddItem appends one extracted item and its owned citation edge range.
func (builder *Builder) AddItem(kind ItemKind, parent ItemID, text []byte, citations []CitationID) (ItemID, error) {
	proposal := &builder.proposal
	if overLimit(len(proposal.ItemKinds)+1, builder.limits.MaxItems) ||
		overLimit(len(proposal.ItemCitationIDs)+len(citations), builder.limits.MaxCitationEdges) ||
		overLimit(len(proposal.ItemBytes)+len(text), builder.limits.MaxClaimBytes) ||
		uint64(len(proposal.ItemBytes))+uint64(len(text)) > uint64(^uint32(0)) ||
		uint64(len(proposal.ItemCitationIDs))+uint64(len(citations)) > uint64(^uint32(0)) ||
		len(citations) > int(^uint16(0)) {
		return 0, ErrLimit
	}
	if !kind.Valid() || len(text) == 0 || len(citations) == 0 || uint64(parent) > uint64(len(proposal.ItemKinds)) {
		return 0, ErrInvalidProposal
	}
	for _, citation := range citations {
		if citation == 0 || uint64(citation) > uint64(len(proposal.CitationPages)) {
			return 0, ErrInvalidProposal
		}
	}

	textStart := uint32(len(proposal.ItemBytes))
	citationStart := uint32(len(proposal.ItemCitationIDs))
	proposal.ItemKinds = append(proposal.ItemKinds, kind)
	proposal.ItemParents = append(proposal.ItemParents, parent)
	proposal.ItemTextStarts = append(proposal.ItemTextStarts, textStart)
	proposal.ItemTextLengths = append(proposal.ItemTextLengths, uint32(len(text)))
	proposal.ItemCitationStarts = append(proposal.ItemCitationStarts, citationStart)
	proposal.ItemCitationCounts = append(proposal.ItemCitationCounts, uint16(len(citations)))
	proposal.ItemBytes = append(proposal.ItemBytes, text...)
	proposal.ItemCitationIDs = append(proposal.ItemCitationIDs, citations...)
	return ItemID(len(proposal.ItemKinds)), nil
}

// Finish returns a self-contained proposal that survives builder reuse.
func (builder *Builder) Finish() Proposal {
	proposal := builder.proposal
	proposal.ItemKinds = clone(proposal.ItemKinds)
	proposal.ItemParents = clone(proposal.ItemParents)
	proposal.ItemTextStarts = clone(proposal.ItemTextStarts)
	proposal.ItemTextLengths = clone(proposal.ItemTextLengths)
	proposal.ItemCitationStarts = clone(proposal.ItemCitationStarts)
	proposal.ItemCitationCounts = clone(proposal.ItemCitationCounts)
	proposal.ItemBytes = clone(proposal.ItemBytes)
	proposal.ItemCitationIDs = clone(proposal.ItemCitationIDs)
	proposal.CitationPages = clone(proposal.CitationPages)
	proposal.CitationSourceStarts = clone(proposal.CitationSourceStarts)
	proposal.CitationSourceEnds = clone(proposal.CitationSourceEnds)
	proposal.CitationQuoteStarts = clone(proposal.CitationQuoteStarts)
	proposal.CitationQuoteLengths = clone(proposal.CitationQuoteLengths)
	proposal.CitationQuoteBytes = clone(proposal.CitationQuoteBytes)
	return proposal
}

func clone[T any](values []T) []T {
	if values == nil {
		return nil
	}
	cloned := make([]T, len(values))
	copy(cloned, values)
	return cloned
}
