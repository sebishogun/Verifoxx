package natural

import (
	"encoding/binary"
	"unicode/utf8"
)

const approvalTokenSchema uint16 = 1
const approvalTokenFixedBytes = 4 + 2 + 8 + 8 + 32 + 32 + 4 + 4

var approvalTokenMagic = [...]byte{'N', 'R', 'A', 'T'}

// AppendApprovalToken appends the canonical binary representation of token.
// On error dst is unchanged.
func AppendApprovalToken(dst []byte, token ApprovalToken, limits Limits) ([]byte, error) {
	if token.SchemaVersion != approvalTokenSchema || token.IssuedUnix >= token.ExpiresUnix ||
		len(token.Reviewer) == 0 || !utf8.Valid(token.Reviewer) || len(token.Signature) == 0 ||
		uint64(len(token.Reviewer)) > uint64(^uint32(0)) || uint64(len(token.Signature)) > uint64(^uint32(0)) {
		return dst, ErrInvalidToken
	}
	size := uint64(approvalTokenFixedBytes) + uint64(len(token.Reviewer)) + uint64(len(token.Signature))
	if limits.MaxTokenBytes != 0 && size > uint64(limits.MaxTokenBytes) || size > uint64(maxInt()) {
		return dst, ErrLimit
	}

	start := len(dst)
	dst = append(dst, approvalTokenMagic[:]...)
	dst = appendTokenUint16(dst, token.SchemaVersion)
	dst = appendTokenUint64(dst, uint64(token.IssuedUnix))
	dst = appendTokenUint64(dst, uint64(token.ExpiresUnix))
	dst = append(dst, token.ProposalDigest[:]...)
	dst = append(dst, token.DraftDigest[:]...)
	dst = appendTokenUint32(dst, uint32(len(token.Reviewer)))
	dst = appendTokenUint32(dst, uint32(len(token.Signature)))
	dst = append(dst, token.Reviewer...)
	dst = append(dst, token.Signature...)
	if len(dst)-start != int(size) {
		return dst[:start], ErrInvalidToken
	}
	return dst, nil
}

// ParseApprovalToken decodes and owns one canonical binary approval token.
func ParseApprovalToken(source []byte, limits Limits) (ApprovalToken, error) {
	if limits.MaxTokenBytes != 0 && uint64(len(source)) > uint64(limits.MaxTokenBytes) {
		return ApprovalToken{}, ErrLimit
	}
	if len(source) < approvalTokenFixedBytes || source[0] != approvalTokenMagic[0] || source[1] != approvalTokenMagic[1] ||
		source[2] != approvalTokenMagic[2] || source[3] != approvalTokenMagic[3] {
		return ApprovalToken{}, ErrInvalidToken
	}
	offset := 4
	schemaVersion := binary.LittleEndian.Uint16(source[offset:])
	offset += 2
	issuedUnix := int64(binary.LittleEndian.Uint64(source[offset:]))
	offset += 8
	expiresUnix := int64(binary.LittleEndian.Uint64(source[offset:]))
	offset += 8
	var proposalDigest [32]byte
	copy(proposalDigest[:], source[offset:offset+len(proposalDigest)])
	offset += len(proposalDigest)
	var draftDigest [32]byte
	copy(draftDigest[:], source[offset:offset+len(draftDigest)])
	offset += len(draftDigest)
	reviewerLength := binary.LittleEndian.Uint32(source[offset:])
	offset += 4
	signatureLength := binary.LittleEndian.Uint32(source[offset:])
	offset += 4
	end := uint64(offset) + uint64(reviewerLength) + uint64(signatureLength)
	if schemaVersion != approvalTokenSchema || issuedUnix >= expiresUnix || reviewerLength == 0 || signatureLength == 0 || end != uint64(len(source)) {
		return ApprovalToken{}, ErrInvalidToken
	}
	reviewerEnd := offset + int(reviewerLength)
	reviewer := source[offset:reviewerEnd]
	if !utf8.Valid(reviewer) {
		return ApprovalToken{}, ErrInvalidToken
	}
	return ApprovalToken{
		Reviewer:       append([]byte(nil), reviewer...),
		Signature:      append([]byte(nil), source[reviewerEnd:]...),
		ProposalDigest: proposalDigest,
		DraftDigest:    draftDigest,
		IssuedUnix:     issuedUnix,
		ExpiresUnix:    expiresUnix,
		SchemaVersion:  schemaVersion,
	}, nil
}

func appendTokenUint16(dst []byte, value uint16) []byte {
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	return append(dst, encoded[:]...)
}

func appendTokenUint32(dst []byte, value uint32) []byte {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	return append(dst, encoded[:]...)
}

func appendTokenUint64(dst []byte, value uint64) []byte {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	return append(dst, encoded[:]...)
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
