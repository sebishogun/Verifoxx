// Package natural defines the bounded, non-executable proposal contract for
// reviewed natural-language policy extraction.
package natural

import (
	"crypto/sha256"
	"errors"
	"unicode/utf8"
)

var (
	ErrInvalidDocument = errors.New("natural frontend: invalid document")
	ErrInvalidProposal = errors.New("natural frontend: invalid proposal")
	ErrInvalidDraft    = errors.New("natural frontend: invalid reviewed draft")
	ErrInvalidToken    = errors.New("natural frontend: invalid approval token")
	ErrExpiredToken    = errors.New("natural frontend: expired approval token")
	ErrLimit           = errors.New("natural frontend: limit exceeded")
)

// ItemKind classifies one extracted proposal row.
type ItemKind uint8

const (
	ItemKindInvalid ItemKind = iota
	ItemKindRequirement
	ItemKindApplicability
	ItemKindAssertion
	ItemKindEvidence
	ItemKindException
	ItemKindRestriction
	ItemKindAssumption
	ItemKindAmbiguity
)

func (kind ItemKind) Valid() bool {
	return kind >= ItemKindRequirement && kind <= ItemKindAmbiguity
}

var itemKindNames = [...]string{
	"invalid", "requirement", "applicability", "assertion", "evidence",
	"exception", "restriction", "assumption", "ambiguity",
}

func (kind ItemKind) String() string {
	if !kind.Valid() {
		return "invalid"
	}
	return itemKindNames[kind]
}

// DiagnosticCode identifies a stable natural-frontend failure class.
type DiagnosticCode uint8

const (
	CodeInvalid DiagnosticCode = iota
	CodeInvalidDocument
	CodeInvalidProposal
	CodeLimit
	CodeCitation
	CodeDuplicate
	CodeConflict
	CodeAmbiguity
	CodeOmittedRestriction
	CodeInvalidDraft
	CodeInvalidToken
	CodeExpiredToken
)

func (code DiagnosticCode) Valid() bool {
	return code >= CodeInvalidDocument && code <= CodeExpiredToken
}

// Span is a half-open UTF-8 byte range in a document.
type Span struct {
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
}

// Limits bounds natural-language ingestion and review storage.
type Limits struct {
	MaxSourceBytes   uint32
	MaxPages         uint32
	MaxSegments      uint32
	MaxItems         uint32
	MaxCitations     uint32
	MaxCitationEdges uint32
	MaxClaimBytes    uint32
	MaxQuoteBytes    uint32
	MaxDiagnostics   uint32
	MaxReviewBytes   uint32
	MaxDraftBytes    uint32
	MaxMappings      uint32
	MaxTokenBytes    uint32
}

// DefaultLimits returns conservative offline extraction limits.
func DefaultLimits() Limits {
	return Limits{
		MaxSourceBytes:   4 << 20,
		MaxPages:         4096,
		MaxSegments:      1 << 16,
		MaxItems:         1 << 16,
		MaxCitations:     1 << 17,
		MaxCitationEdges: 1 << 18,
		MaxClaimBytes:    4 << 20,
		MaxQuoteBytes:    8 << 20,
		MaxDiagnostics:   1024,
		MaxReviewBytes:   16 << 20,
		MaxDraftBytes:    8 << 20,
		MaxMappings:      1 << 18,
		MaxTokenBytes:    1 << 16,
	}
}

// Document owns bounded source bytes and page offsets.
type Document struct {
	Source     []byte
	PageStarts []uint32
	Digest     [sha256.Size]byte
}

// NewDocument validates and owns one page-addressable UTF-8 document.
func NewDocument(source []byte, pageStarts []uint32, limits Limits) (*Document, error) {
	if len(source) == 0 || !utf8.Valid(source) || len(pageStarts) == 0 || pageStarts[0] != 0 {
		return nil, ErrInvalidDocument
	}
	if overLimit(len(source), limits.MaxSourceBytes) || overLimit(len(pageStarts), limits.MaxPages) {
		return nil, ErrLimit
	}
	for row := 1; row < len(pageStarts); row++ {
		if pageStarts[row] <= pageStarts[row-1] || uint64(pageStarts[row]) >= uint64(len(source)) {
			return nil, ErrInvalidDocument
		}
	}

	document := &Document{
		Source:     append([]byte(nil), source...),
		PageStarts: append([]uint32(nil), pageStarts...),
		Digest:     sha256.Sum256(source),
	}
	return document, nil
}

func overLimit(length int, limit uint32) bool {
	return limit != 0 && uint64(length) > uint64(limit)
}
