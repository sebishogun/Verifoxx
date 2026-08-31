package natural

import (
	"crypto/sha256"
	"encoding/binary"

	public "github.com/sebishogun/nornrune/frontend/natural"
)

const proposalDigestVersion uint16 = 1
const draftDigestVersion uint16 = 1
const approvalTokenVersion uint16 = 1

func (reviewer *Reviewer) proposalDigestUnchecked(proposal *public.Proposal) [sha256.Size]byte {
	dst := reviewer.canonical[:0]
	dst = appendUint16(dst, proposalDigestVersion)
	dst = append(dst, proposal.DocumentDigest[:]...)
	dst = appendString(dst, proposal.Provider.ID)
	dst = appendString(dst, proposal.Provider.Version)

	dst = appendUint32(dst, uint32(len(proposal.ItemKinds)))
	for _, value := range proposal.ItemKinds {
		dst = append(dst, byte(value))
	}
	dst = appendItemIDs(dst, proposal.ItemParents)
	dst = appendUint32s(dst, proposal.ItemTextStarts)
	dst = appendUint32s(dst, proposal.ItemTextLengths)
	dst = appendUint32s(dst, proposal.ItemCitationStarts)
	dst = appendUint16s(dst, proposal.ItemCitationCounts)
	dst = appendBytes(dst, proposal.ItemBytes)
	dst = appendCitationIDs(dst, proposal.ItemCitationIDs)

	dst = appendUint32s(dst, proposal.CitationPages)
	dst = appendUint32s(dst, proposal.CitationSourceStarts)
	dst = appendUint32s(dst, proposal.CitationSourceEnds)
	dst = appendUint32s(dst, proposal.CitationQuoteStarts)
	dst = appendUint32s(dst, proposal.CitationQuoteLengths)
	dst = appendBytes(dst, proposal.CitationQuoteBytes)
	reviewer.canonical = dst
	return sha256.Sum256(dst)
}

func (reviewer *Reviewer) draftDigestUnchecked(draft *public.ReviewedDraft) [sha256.Size]byte {
	dst := reviewer.draftCanonical[:0]
	dst = appendUint16(dst, draftDigestVersion)
	dst = appendBytes(dst, draft.PolicySource)
	dst = appendUint32s(dst, draft.RequirementIDs)
	dst = appendUint32s(dst, draft.MappingStarts)
	dst = appendUint16s(dst, draft.MappingCounts)
	dst = appendItemIDs(dst, draft.MappingProposalItems)
	reviewer.draftCanonical = dst
	return sha256.Sum256(dst)
}

func (reviewer *Reviewer) approvalMessage(token *public.ApprovalToken) [sha256.Size]byte {
	dst := reviewer.tokenPayload[:0]
	dst = appendUint16(dst, token.SchemaVersion)
	dst = appendInt64(dst, token.IssuedUnix)
	dst = appendInt64(dst, token.ExpiresUnix)
	dst = append(dst, token.ProposalDigest[:]...)
	dst = append(dst, token.DraftDigest[:]...)
	dst = appendBytes(dst, token.Reviewer)
	reviewer.tokenPayload = dst
	return sha256.Sum256(dst)
}

func appendString(dst []byte, value string) []byte {
	dst = appendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendBytes(dst, value []byte) []byte {
	dst = appendUint32(dst, uint32(len(value)))
	return append(dst, value...)
}

func appendItemIDs(dst []byte, values []public.ItemID) []byte {
	dst = appendUint32(dst, uint32(len(values)))
	for _, value := range values {
		dst = appendUint32(dst, uint32(value))
	}
	return dst
}

func appendCitationIDs(dst []byte, values []public.CitationID) []byte {
	dst = appendUint32(dst, uint32(len(values)))
	for _, value := range values {
		dst = appendUint32(dst, uint32(value))
	}
	return dst
}

func appendUint32s(dst []byte, values []uint32) []byte {
	dst = appendUint32(dst, uint32(len(values)))
	for _, value := range values {
		dst = appendUint32(dst, value)
	}
	return dst
}

func appendUint16s(dst []byte, values []uint16) []byte {
	dst = appendUint32(dst, uint32(len(values)))
	for _, value := range values {
		dst = appendUint16(dst, value)
	}
	return dst
}

func appendUint32(dst []byte, value uint32) []byte {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	return append(dst, encoded[:]...)
}

func appendUint16(dst []byte, value uint16) []byte {
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	return append(dst, encoded[:]...)
}

func appendInt64(dst []byte, value int64) []byte {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], uint64(value))
	return append(dst, encoded[:]...)
}
