// Package natural validates and reviews untrusted natural-language policy
// proposals outside evaluator and publication paths.
package natural

import (
	"crypto/sha256"
	"strconv"
	"unicode/utf8"

	public "github.com/sebishogun/nornrune/frontend/natural"
)

// Reviewer owns reusable proposal validation, canonicalization, and rendering
// scratch. It is not safe for concurrent use.
type Reviewer struct {
	validator          public.Validator
	diagnostics        []public.Diagnostic
	canonical          []byte
	draftCanonical     []byte
	tokenPayload       []byte
	review             []byte
	semanticSlots      []uint64
	mappedItems        []uint8
	mappedRequirements []uint8
}

// ProposalDigest validates proposal and returns its canonical digest.
func (reviewer *Reviewer) ProposalDigest(document *public.Document, proposal *public.Proposal, limits public.Limits) ([sha256.Size]byte, error) {
	if reviewer == nil {
		return [sha256.Size]byte{}, public.ErrInvalidProposal
	}
	reviewer.diagnostics = reviewer.validator.Validate(reviewer.diagnostics[:0], document, proposal, limits)
	if len(reviewer.diagnostics) != 0 {
		return [sha256.Size]byte{}, public.ErrInvalidProposal
	}
	return reviewer.proposalDigestUnchecked(proposal), nil
}

// DraftDigest validates proposal and reviewer-owned provenance before hashing
// the exact draft bytes and mapping columns.
func (reviewer *Reviewer) DraftDigest(document *public.Document, proposal *public.Proposal, draft *public.ReviewedDraft, limits public.Limits) ([sha256.Size]byte, []public.Diagnostic, error) {
	if reviewer == nil {
		return [sha256.Size]byte{}, nil, public.ErrInvalidDraft
	}
	reviewer.diagnostics = reviewer.validator.Validate(reviewer.diagnostics[:0], document, proposal, limits)
	if len(reviewer.diagnostics) != 0 {
		return [sha256.Size]byte{}, reviewer.diagnostics, nil
	}
	if !reviewer.validDraft(proposal, draft, limits) {
		return [sha256.Size]byte{}, nil, public.ErrInvalidDraft
	}
	return reviewer.draftDigestUnchecked(draft), reviewer.diagnostics, nil
}

// IssueApproval signs one exact proposal/draft pair. It does not publish or
// activate policy.
func (reviewer *Reviewer) IssueApproval(
	document *public.Document,
	proposal *public.Proposal,
	draft *public.ReviewedDraft,
	reviewerID []byte,
	issuedUnix, expiresUnix int64,
	signer public.Signer,
	limits public.Limits,
) (public.ApprovalToken, []public.Diagnostic, error) {
	if reviewer == nil || signer == nil || len(reviewerID) == 0 || !utf8.Valid(reviewerID) || issuedUnix >= expiresUnix {
		return public.ApprovalToken{}, nil, public.ErrInvalidToken
	}
	if limits.MaxTokenBytes != 0 && tokenSize(len(reviewerID), 1) > uint64(limits.MaxTokenBytes) {
		return public.ApprovalToken{}, nil, public.ErrLimit
	}
	proposalDigest, err := reviewer.ProposalDigest(document, proposal, limits)
	if err != nil {
		return public.ApprovalToken{}, reviewer.diagnostics, err
	}
	draftDigest, diagnostics, err := reviewer.DraftDigest(document, proposal, draft, limits)
	if err != nil || len(diagnostics) != 0 {
		return public.ApprovalToken{}, diagnostics, err
	}
	token := public.ApprovalToken{
		ProposalDigest: proposalDigest,
		DraftDigest:    draftDigest,
		IssuedUnix:     issuedUnix,
		ExpiresUnix:    expiresUnix,
		SchemaVersion:  approvalTokenVersion,
		Reviewer:       reviewerID,
	}
	message := reviewer.approvalMessage(&token)
	signature, err := signer.Sign(message[:])
	if err != nil {
		return public.ApprovalToken{}, nil, err
	}
	if len(signature) == 0 || limits.MaxTokenBytes != 0 && tokenSize(len(token.Reviewer), len(signature)) > uint64(limits.MaxTokenBytes) {
		return public.ApprovalToken{}, nil, public.ErrLimit
	}
	token.Reviewer = append([]byte(nil), reviewerID...)
	token.Signature = append([]byte(nil), signature...)
	return token, reviewer.diagnostics, nil
}

// VerifyApproval authenticates one exact proposal/draft pair at caller-supplied
// time. It reads no process-global clock.
func (reviewer *Reviewer) VerifyApproval(
	document *public.Document,
	proposal *public.Proposal,
	draft *public.ReviewedDraft,
	token public.ApprovalToken,
	verifier public.Verifier,
	nowUnix, maxClockSkew int64,
	limits public.Limits,
) error {
	if reviewer == nil || verifier == nil || maxClockSkew < 0 || token.SchemaVersion != approvalTokenVersion ||
		len(token.Reviewer) == 0 || !utf8.Valid(token.Reviewer) || len(token.Signature) == 0 ||
		token.IssuedUnix >= token.ExpiresUnix {
		return public.ErrInvalidToken
	}
	if token.IssuedUnix > nowUnix && uint64(token.IssuedUnix)-uint64(nowUnix) > uint64(maxClockSkew) {
		return public.ErrInvalidToken
	}
	if nowUnix >= token.ExpiresUnix {
		return public.ErrExpiredToken
	}
	if limits.MaxTokenBytes != 0 && tokenSize(len(token.Reviewer), len(token.Signature)) > uint64(limits.MaxTokenBytes) {
		return public.ErrInvalidToken
	}
	proposalDigest, err := reviewer.ProposalDigest(document, proposal, limits)
	if err != nil || proposalDigest != token.ProposalDigest {
		return public.ErrInvalidToken
	}
	draftDigest, diagnostics, err := reviewer.DraftDigest(document, proposal, draft, limits)
	if err != nil || len(diagnostics) != 0 || draftDigest != token.DraftDigest {
		return public.ErrInvalidToken
	}
	message := reviewer.approvalMessage(&token)
	if err := verifier.Verify(message[:], token.Signature); err != nil {
		return public.ErrInvalidToken
	}
	return nil
}

func (reviewer *Reviewer) validDraft(proposal *public.Proposal, draft *public.ReviewedDraft, limits public.Limits) bool {
	if draft == nil || len(draft.PolicySource) == 0 || !utf8.Valid(draft.PolicySource) ||
		overLimit(len(draft.PolicySource), limits.MaxDraftBytes) ||
		overLimit(len(draft.SemanticKinds), limits.MaxMappings) ||
		overLimit(len(draft.MappingProposalItems), limits.MaxMappings) ||
		len(draft.SemanticKinds) == 0 ||
		len(draft.SemanticIDs) != len(draft.SemanticKinds) ||
		len(draft.MappingStarts) != len(draft.SemanticKinds) ||
		len(draft.MappingCounts) != len(draft.SemanticKinds) {
		return false
	}

	tableSize := reviewHashTableSize(len(draft.SemanticKinds))
	reviewer.semanticSlots = resizeUint64(reviewer.semanticSlots, tableSize)
	reviewer.mappedItems = resizeUint8(reviewer.mappedItems, len(proposal.ItemKinds))
	reviewer.mappedRequirements = resizeUint8(reviewer.mappedRequirements, len(proposal.ItemKinds))
	cursor := uint64(0)
	for row, kind := range draft.SemanticKinds {
		semanticID := draft.SemanticIDs[row]
		if !kind.Valid() || semanticID == 0 || insertSemanticKey(reviewer.semanticSlots, semanticKey(kind, semanticID)) {
			return false
		}
		start := draft.MappingStarts[row]
		count := uint32(draft.MappingCounts[row])
		if count == 0 || uint64(start) != cursor || uint64(start)+uint64(count) > uint64(len(draft.MappingProposalItems)) {
			return false
		}
		cursor += uint64(count)
		hasRequirement := false
		for offset := uint32(0); offset < count; offset++ {
			item := draft.MappingProposalItems[start+offset]
			if item == 0 || uint64(item) > uint64(len(proposal.ItemKinds)) || proposal.ItemKinds[item-1] == public.ItemKindAmbiguity {
				return false
			}
			reviewer.mappedItems[item-1] = 1
			if kind == public.SemanticKindRequirement && proposal.ItemKinds[item-1] == public.ItemKindRequirement {
				hasRequirement = true
				reviewer.mappedRequirements[item-1] = 1
			}
			for previous := uint32(0); previous < offset; previous++ {
				if draft.MappingProposalItems[start+previous] == item {
					return false
				}
			}
		}
		if kind == public.SemanticKindRequirement && !hasRequirement {
			return false
		}
	}
	if cursor != uint64(len(draft.MappingProposalItems)) {
		return false
	}
	for row, kind := range proposal.ItemKinds {
		if kind != public.ItemKindAmbiguity && reviewer.mappedItems[row] == 0 ||
			kind == public.ItemKindRequirement && reviewer.mappedRequirements[row] == 0 {
			return false
		}
	}
	return true
}

func tokenSize(reviewerBytes, signatureBytes int) uint64 {
	const fixed = 4 + 2 + 8 + 8 + sha256.Size + sha256.Size + 4 + 4
	return fixed + uint64(reviewerBytes) + uint64(signatureBytes)
}

func overLimit(length int, limit uint32) bool {
	return limit != 0 && uint64(length) > uint64(limit)
}

func reviewHashTableSize(rows int) int {
	size := 4
	for size < rows*2 {
		size <<= 1
	}
	return size
}

func resizeUint64(values []uint64, length int) []uint64 {
	if cap(values) < length {
		return make([]uint64, length)
	}
	values = values[:length]
	clear(values)
	return values
}

func resizeUint8(values []uint8, length int) []uint8 {
	if cap(values) < length {
		return make([]uint8, length)
	}
	values = values[:length]
	clear(values)
	return values
}

func semanticKey(kind public.SemanticKind, id uint32) uint64 {
	return uint64(kind)<<32 | uint64(id)
}

func insertSemanticKey(slots []uint64, key uint64) bool {
	mask := uint64(len(slots) - 1)
	slot := key * 11400714819323198485 & mask
	for {
		if slots[slot] == 0 {
			slots[slot] = key
			return false
		}
		if slots[slot] == key {
			return true
		}
		slot = (slot + 1) & mask
	}
}

// AppendReview appends one deterministic JSON review view. Structurally unsafe
// input leaves dst unchanged; reviewable semantic defects appear in the artifact
// and returned diagnostics so they can block approval without hiding context.
func (reviewer *Reviewer) AppendReview(dst []byte, document *public.Document, proposal *public.Proposal, limits public.Limits) ([]byte, []public.Diagnostic, error) {
	if reviewer == nil {
		return dst, nil, public.ErrInvalidProposal
	}
	reviewer.diagnostics = reviewer.validator.Validate(reviewer.diagnostics[:0], document, proposal, limits)
	if !reviewableDiagnostics(reviewer.diagnostics) {
		return dst, reviewer.diagnostics, nil
	}
	digest := reviewer.proposalDigestUnchecked(proposal)
	review, ok := reviewer.renderReview(reviewer.review[:0], document, proposal, digest, reviewer.diagnostics, limits.MaxReviewBytes)
	if !ok {
		return dst, nil, public.ErrLimit
	}
	reviewer.review = review
	if limits.MaxReviewBytes != 0 && uint64(len(dst))+uint64(len(review)) > uint64(limits.MaxReviewBytes) {
		return dst, nil, public.ErrLimit
	}
	return append(dst, review...), reviewer.diagnostics, nil
}

func reviewableDiagnostics(diagnostics []public.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		switch diagnostic.Code {
		case public.CodeDuplicate, public.CodeConflict, public.CodeAmbiguity, public.CodeOmittedRestriction:
		default:
			return false
		}
	}
	return true
}

func (reviewer *Reviewer) renderReview(dst []byte, document *public.Document, proposal *public.Proposal, digest [sha256.Size]byte, diagnostics []public.Diagnostic, limit uint32) ([]byte, bool) {
	appender := reviewAppender{dst: dst, limit: limit, ok: true}
	appender.raw(`{"schema_version":1,"document_sha256":"`)
	appender.hex(document.Digest[:])
	appender.raw(`","proposal_sha256":"`)
	appender.hex(digest[:])
	appender.raw(`","provider":{"id":`)
	appender.quoted([]byte(proposal.Provider.ID))
	appender.raw(`,"version":`)
	appender.quoted([]byte(proposal.Provider.Version))
	appender.raw(`},"items":[`)
	for row, kind := range proposal.ItemKinds {
		if row != 0 {
			appender.byte(',')
		}
		appender.raw(`{"id":`)
		appender.uint(uint64(row + 1))
		appender.raw(`,"kind":`)
		appender.quoted([]byte(kind.String()))
		appender.raw(`,"parent":`)
		appender.uint(uint64(proposal.ItemParents[row]))
		appender.raw(`,"claim":`)
		textStart := proposal.ItemTextStarts[row]
		textEnd := textStart + proposal.ItemTextLengths[row]
		appender.quoted(proposal.ItemBytes[textStart:textEnd])
		appender.raw(`,"citations":[`)
		edgeStart := proposal.ItemCitationStarts[row]
		edgeCount := uint32(proposal.ItemCitationCounts[row])
		for edge := uint32(0); edge < edgeCount; edge++ {
			if edge != 0 {
				appender.byte(',')
			}
			citationID := proposal.ItemCitationIDs[edgeStart+edge]
			citation := citationID - 1
			quoteStart := proposal.CitationQuoteStarts[citation]
			quoteEnd := quoteStart + proposal.CitationQuoteLengths[citation]
			appender.raw(`{"id":`)
			appender.uint(uint64(citationID))
			appender.raw(`,"page":`)
			appender.uint(uint64(proposal.CitationPages[citation]))
			appender.raw(`,"start":`)
			appender.uint(uint64(proposal.CitationSourceStarts[citation]))
			appender.raw(`,"end":`)
			appender.uint(uint64(proposal.CitationSourceEnds[citation]))
			appender.raw(`,"quote":`)
			appender.quoted(proposal.CitationQuoteBytes[quoteStart:quoteEnd])
			appender.byte('}')
		}
		appender.raw(`]}`)
	}
	appender.raw(`],"diagnostics":[`)
	for row, diagnostic := range diagnostics {
		if row != 0 {
			appender.byte(',')
		}
		appender.raw(`{"code":`)
		appender.uint(uint64(diagnostic.Code))
		appender.raw(`,"item":`)
		appender.uint(uint64(diagnostic.Item))
		appender.raw(`,"citation":`)
		appender.uint(uint64(diagnostic.Citation))
		appender.raw(`,"start":`)
		appender.uint(uint64(diagnostic.Span.Start))
		appender.raw(`,"end":`)
		appender.uint(uint64(diagnostic.Span.End))
		appender.byte('}')
	}
	appender.raw(`]}`)
	return appender.dst, appender.ok
}

type reviewAppender struct {
	dst   []byte
	limit uint32
	ok    bool
}

func (appender *reviewAppender) append(value []byte) {
	if !appender.ok {
		return
	}
	if appender.limit != 0 && uint64(len(appender.dst))+uint64(len(value)) > uint64(appender.limit) {
		appender.ok = false
		return
	}
	appender.dst = append(appender.dst, value...)
	appender.ok = true
}

func (appender *reviewAppender) raw(value string) { appender.append([]byte(value)) }

func (appender *reviewAppender) byte(value byte) {
	var single [1]byte
	single[0] = value
	appender.append(single[:])
}

func (appender *reviewAppender) uint(value uint64) {
	var scratch [20]byte
	appender.append(strconv.AppendUint(scratch[:0], value, 10))
}

func (appender *reviewAppender) hex(value []byte) {
	const digits = "0123456789abcdef"
	var scratch [sha256.Size * 2]byte
	for row, current := range value {
		scratch[row*2] = digits[current>>4]
		scratch[row*2+1] = digits[current&0x0f]
	}
	appender.append(scratch[:len(value)*2])
}

func (appender *reviewAppender) quoted(value []byte) {
	appender.byte('"')
	for _, current := range value {
		switch current {
		case '"', '\\':
			appender.byte('\\')
			appender.byte(current)
		case '\b':
			appender.raw(`\b`)
		case '\f':
			appender.raw(`\f`)
		case '\n':
			appender.raw(`\n`)
		case '\r':
			appender.raw(`\r`)
		case '\t':
			appender.raw(`\t`)
		default:
			if current < 0x20 {
				const digits = "0123456789abcdef"
				var escaped = [6]byte{'\\', 'u', '0', '0', digits[current>>4], digits[current&0x0f]}
				appender.append(escaped[:])
			} else {
				appender.byte(current)
			}
		}
	}
	appender.byte('"')
}
