package natural

import (
	"bytes"
	"crypto/sha256"
	"slices"
	"unicode/utf8"
)

// Diagnostic is a bounded, pointerless validation result.
type Diagnostic struct {
	Span     Span
	Item     ItemID
	Citation CitationID
	Code     DiagnosticCode
}

// Validator owns reusable proposal-validation scratch. It is not safe for
// concurrent use.
type Validator struct {
	duplicateSlots []uint32
	conflictSlots  []uint32
	owners         []ItemID
	restrictions   []uint8
}

// Validate checks one untrusted proposal. Returned diagnostics use dst storage
// when capacity permits and remain valid until the caller mutates dst.
func (validator *Validator) Validate(dst []Diagnostic, document *Document, proposal *Proposal, limits Limits) []Diagnostic {
	diagnostics := dst[:0]
	if validator == nil || document == nil || proposal == nil {
		return appendDiagnostic(diagnostics, limits, Diagnostic{Code: CodeInvalidProposal})
	}
	if exceedsProposalLimits(document, proposal, limits) {
		return appendDiagnostic(diagnostics, limits, Diagnostic{Code: CodeLimit})
	}
	if !validDocument(document) || proposal.DocumentDigest != document.Digest || sha256.Sum256(document.Source) != document.Digest {
		diagnostics = appendDiagnostic(diagnostics, limits, Diagnostic{Code: CodeInvalidDocument})
	}
	if proposal.Provider.ID == "" || !utf8.ValidString(proposal.Provider.ID) || !utf8.ValidString(proposal.Provider.Version) {
		diagnostics = appendDiagnostic(diagnostics, limits, Diagnostic{Code: CodeInvalidProposal})
	}

	itemRows := len(proposal.ItemKinds)
	if !allEqual(itemRows,
		len(proposal.ItemParents), len(proposal.ItemTextStarts), len(proposal.ItemTextLengths),
		len(proposal.ItemCitationStarts), len(proposal.ItemCitationCounts),
	) {
		diagnostics = appendDiagnostic(diagnostics, limits, Diagnostic{Code: CodeInvalidProposal})
		return sortDiagnostics(diagnostics)
	}
	citationRows := len(proposal.CitationPages)
	if !allEqual(citationRows,
		len(proposal.CitationSourceStarts), len(proposal.CitationSourceEnds),
		len(proposal.CitationQuoteStarts), len(proposal.CitationQuoteLengths),
	) {
		diagnostics = appendDiagnostic(diagnostics, limits, Diagnostic{Code: CodeInvalidProposal})
		return sortDiagnostics(diagnostics)
	}
	if itemRows == 0 || citationRows == 0 || !utf8.Valid(proposal.ItemBytes) || !utf8.Valid(proposal.CitationQuoteBytes) {
		diagnostics = appendDiagnostic(diagnostics, limits, Diagnostic{Code: CodeInvalidProposal})
	}

	diagnostics = validator.validateCitations(diagnostics, document, proposal, limits)
	diagnostics = validator.validateItems(diagnostics, document, proposal, limits)
	return sortDiagnostics(diagnostics)
}

func (validator *Validator) validateCitations(diagnostics []Diagnostic, document *Document, proposal *Proposal, limits Limits) []Diagnostic {
	quoteCursor := uint64(0)
	for row := range proposal.CitationPages {
		start := proposal.CitationSourceStarts[row]
		end := proposal.CitationSourceEnds[row]
		span := Span{Start: start, End: end}
		quoteStart := proposal.CitationQuoteStarts[row]
		quoteLength := proposal.CitationQuoteLengths[row]
		citation := CitationID(row + 1)

		valid := uint64(quoteStart) == quoteCursor && validRange(quoteStart, quoteLength, len(proposal.CitationQuoteBytes))
		if valid {
			quoteCursor += uint64(quoteLength)
		}
		valid = valid && start < end && uint64(end) <= uint64(len(document.Source))
		if valid {
			page := proposal.CitationPages[row]
			valid = uint64(page) < uint64(len(document.PageStarts)) &&
				pageForOffset(document.PageStarts, start) == page &&
				pageForOffset(document.PageStarts, end-1) == page
		}
		if valid {
			quote := proposal.CitationQuoteBytes[quoteStart : quoteStart+quoteLength]
			valid = uint64(quoteLength) == uint64(end-start) && bytes.Equal(quote, document.Source[start:end])
		}
		if !valid {
			diagnostics = appendDiagnostic(diagnostics, limits, Diagnostic{Span: validDiagnosticSpan(span, document), Citation: citation, Code: CodeCitation})
		}
	}
	if quoteCursor != uint64(len(proposal.CitationQuoteBytes)) {
		diagnostics = appendDiagnostic(diagnostics, limits, Diagnostic{Code: CodeInvalidProposal})
	}
	return diagnostics
}

func (validator *Validator) validateItems(diagnostics []Diagnostic, document *Document, proposal *Proposal, limits Limits) []Diagnostic {
	rows := len(proposal.ItemKinds)
	validator.owners = resizeZeroed(validator.owners, rows)
	validator.restrictions = resizeZeroed(validator.restrictions, rows)
	tableSize := hashTableSize(rows)
	validator.duplicateSlots = resizeZeroed(validator.duplicateSlots, tableSize)
	validator.conflictSlots = resizeZeroed(validator.conflictSlots, tableSize)

	textCursor, edgeCursor := uint64(0), uint64(0)
	for row := 0; row < rows; row++ {
		item := ItemID(row + 1)
		kind := proposal.ItemKinds[row]
		parent := proposal.ItemParents[row]
		textStart := proposal.ItemTextStarts[row]
		textLength := proposal.ItemTextLengths[row]
		edgeStart := proposal.ItemCitationStarts[row]
		edgeCount := uint32(proposal.ItemCitationCounts[row])
		span := itemDiagnosticSpan(document, proposal, row)

		textOK := uint64(textStart) == textCursor && textLength != 0 && validRange(textStart, textLength, len(proposal.ItemBytes))
		if textOK {
			textCursor += uint64(textLength)
		}
		edgesOK := uint64(edgeStart) == edgeCursor && edgeCount != 0 && validRange(edgeStart, edgeCount, len(proposal.ItemCitationIDs))
		if edgesOK {
			edgeCursor += uint64(edgeCount)
			for offset := uint32(0); offset < edgeCount; offset++ {
				citation := proposal.ItemCitationIDs[edgeStart+offset]
				if citation == 0 || uint64(citation) > uint64(len(proposal.CitationPages)) {
					edgesOK = false
					break
				}
			}
		}
		if !kind.Valid() || !textOK || !edgesOK || uint64(parent) > uint64(row) {
			diagnostics = appendDiagnostic(diagnostics, limits, Diagnostic{Span: span, Item: item, Code: CodeInvalidProposal})
			continue
		}

		switch kind {
		case ItemKindRequirement:
			if parent != 0 {
				diagnostics = appendDiagnostic(diagnostics, limits, Diagnostic{Span: span, Item: item, Code: CodeInvalidProposal})
				continue
			}
			validator.owners[row] = item
		case ItemKindAssumption:
			if parent != 0 {
				validator.owners[row] = validator.owners[parent-1]
			}
		case ItemKindAmbiguity:
			if parent != 0 {
				validator.owners[row] = validator.owners[parent-1]
			}
			diagnostics = appendDiagnostic(diagnostics, limits, Diagnostic{Span: span, Item: item, Code: CodeAmbiguity})
		default:
			if parent == 0 || validator.owners[parent-1] == 0 {
				diagnostics = appendDiagnostic(diagnostics, limits, Diagnostic{Span: span, Item: item, Code: CodeInvalidProposal})
				continue
			}
			owner := validator.owners[parent-1]
			validator.owners[row] = owner
			if kind == ItemKindRestriction {
				validator.restrictions[owner-1] = 1
			}
		}

		text := proposal.ItemBytes[textStart : textStart+textLength]
		if previous, duplicate := findNormalized(
			validator.duplicateSlots, proposal, parent, kind, text, true,
		); duplicate {
			diagnostics = appendDiagnostic(diagnostics, limits, Diagnostic{Span: span, Item: item, Code: CodeDuplicate})
		} else if previous == 0 {
			insertNormalized(validator.duplicateSlots, parent, kind, text, uint32(row+1), true)
		}

		if kind == ItemKindRestriction || kind == ItemKindException {
			previous, found := findNormalized(validator.conflictSlots, proposal, parent, kind, text, false)
			if found && proposal.ItemKinds[previous-1] != kind {
				diagnostics = appendDiagnostic(diagnostics, limits, Diagnostic{Span: span, Item: item, Code: CodeConflict})
			} else if previous == 0 {
				insertNormalized(validator.conflictSlots, parent, kind, text, uint32(row+1), false)
			}
		}
	}
	if textCursor != uint64(len(proposal.ItemBytes)) || edgeCursor != uint64(len(proposal.ItemCitationIDs)) {
		diagnostics = appendDiagnostic(diagnostics, limits, Diagnostic{Code: CodeInvalidProposal})
	}
	for row, kind := range proposal.ItemKinds {
		if kind == ItemKindRequirement && validator.restrictions[row] == 0 {
			diagnostics = appendDiagnostic(diagnostics, limits, Diagnostic{
				Span: itemDiagnosticSpan(document, proposal, row), Item: ItemID(row + 1), Code: CodeOmittedRestriction,
			})
		}
	}
	return diagnostics
}

func exceedsProposalLimits(document *Document, proposal *Proposal, limits Limits) bool {
	return overLimit(len(document.Source), limits.MaxSourceBytes) ||
		overLimit(len(document.PageStarts), limits.MaxPages) ||
		overLimit(len(proposal.ItemKinds), limits.MaxItems) ||
		overLimit(len(proposal.CitationPages), limits.MaxCitations) ||
		overLimit(len(proposal.ItemCitationIDs), limits.MaxCitationEdges) ||
		overLimit(len(proposal.ItemBytes), limits.MaxClaimBytes) ||
		overLimit(len(proposal.CitationQuoteBytes), limits.MaxQuoteBytes)
}

func validDocument(document *Document) bool {
	if len(document.Source) == 0 || !utf8.Valid(document.Source) || len(document.PageStarts) == 0 || document.PageStarts[0] != 0 {
		return false
	}
	for row := 1; row < len(document.PageStarts); row++ {
		if document.PageStarts[row] <= document.PageStarts[row-1] || uint64(document.PageStarts[row]) >= uint64(len(document.Source)) {
			return false
		}
	}
	return true
}

func validRange(start, count uint32, length int) bool {
	return uint64(start)+uint64(count) <= uint64(length)
}

func validDiagnosticSpan(span Span, document *Document) Span {
	if span.Start < span.End && uint64(span.End) <= uint64(len(document.Source)) {
		return span
	}
	return Span{}
}

func itemDiagnosticSpan(document *Document, proposal *Proposal, row int) Span {
	if row < 0 || row >= len(proposal.ItemCitationStarts) || row >= len(proposal.ItemCitationCounts) {
		return Span{}
	}
	start := proposal.ItemCitationStarts[row]
	count := uint32(proposal.ItemCitationCounts[row])
	if count == 0 || !validRange(start, count, len(proposal.ItemCitationIDs)) {
		return Span{}
	}
	citation := proposal.ItemCitationIDs[start]
	if citation == 0 || uint64(citation) > uint64(len(proposal.CitationSourceStarts)) || uint64(citation) > uint64(len(proposal.CitationSourceEnds)) {
		return Span{}
	}
	span := Span{Start: proposal.CitationSourceStarts[citation-1], End: proposal.CitationSourceEnds[citation-1]}
	return validDiagnosticSpan(span, document)
}

func appendDiagnostic(dst []Diagnostic, limits Limits, diagnostic Diagnostic) []Diagnostic {
	if limits.MaxDiagnostics != 0 && uint64(len(dst)) >= uint64(limits.MaxDiagnostics) {
		return dst
	}
	return append(dst, diagnostic)
}

func sortDiagnostics(diagnostics []Diagnostic) []Diagnostic {
	slices.SortStableFunc(diagnostics, func(left, right Diagnostic) int {
		if left.Span.Start != right.Span.Start {
			return compare32(left.Span.Start, right.Span.Start)
		}
		if left.Span.End != right.Span.End {
			return compare32(left.Span.End, right.Span.End)
		}
		if left.Code != right.Code {
			return compare8(uint8(left.Code), uint8(right.Code))
		}
		if left.Item != right.Item {
			return compare32(uint32(left.Item), uint32(right.Item))
		}
		return compare32(uint32(left.Citation), uint32(right.Citation))
	})
	return diagnostics
}

func compare32(left, right uint32) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func compare8(left, right uint8) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func allEqual(want int, lengths ...int) bool {
	for _, length := range lengths {
		if length != want {
			return false
		}
	}
	return true
}

func resizeZeroed[T ~uint8 | ~uint32](values []T, length int) []T {
	if cap(values) < length {
		return make([]T, length)
	}
	values = values[:length]
	clear(values)
	return values
}

func hashTableSize(rows int) int {
	size := 4
	for size < rows*2 {
		size <<= 1
	}
	return size
}

func findNormalized(slots []uint32, proposal *Proposal, parent ItemID, kind ItemKind, text []byte, includeKind bool) (uint32, bool) {
	mask := uint64(len(slots) - 1)
	slot := normalizedHash(parent, kind, text, includeKind) & mask
	for {
		previous := slots[slot]
		if previous == 0 {
			return 0, false
		}
		row := previous - 1
		start := proposal.ItemTextStarts[row]
		length := proposal.ItemTextLengths[row]
		if proposal.ItemParents[row] == parent && (!includeKind || proposal.ItemKinds[row] == kind) &&
			validRange(start, length, len(proposal.ItemBytes)) && normalizedEqual(proposal.ItemBytes[start:start+length], text) {
			return previous, true
		}
		slot = (slot + 1) & mask
	}
}

func insertNormalized(slots []uint32, parent ItemID, kind ItemKind, text []byte, row uint32, includeKind bool) {
	mask := uint64(len(slots) - 1)
	slot := normalizedHash(parent, kind, text, includeKind) & mask
	for slots[slot] != 0 {
		slot = (slot + 1) & mask
	}
	slots[slot] = row
}

func normalizedHash(parent ItemID, kind ItemKind, text []byte, includeKind bool) uint64 {
	const (
		offset = uint64(14695981039346656037)
		prime  = uint64(1099511628211)
	)
	hash := offset
	for shift := uint(0); shift < 32; shift += 8 {
		hash = (hash ^ uint64(byte(uint32(parent)>>shift))) * prime
	}
	if includeKind {
		hash = (hash ^ uint64(kind)) * prime
	}
	iterator := normalizedIterator{text: text}
	for {
		value, ok := iterator.next()
		if !ok {
			return hash
		}
		hash = (hash ^ uint64(value)) * prime
	}
}

func normalizedEqual(left, right []byte) bool {
	leftIterator := normalizedIterator{text: left}
	rightIterator := normalizedIterator{text: right}
	for {
		leftValue, leftOK := leftIterator.next()
		rightValue, rightOK := rightIterator.next()
		if leftOK != rightOK {
			return false
		}
		if !leftOK {
			return true
		}
		if leftValue != rightValue {
			return false
		}
	}
}

type normalizedIterator struct {
	text         []byte
	index        int
	started      bool
	pendingValue bool
}

func (iterator *normalizedIterator) next() (byte, bool) {
	if iterator.pendingValue {
		iterator.pendingValue = false
		value := lowerASCII(iterator.text[iterator.index])
		iterator.index++
		return value, true
	}
	skipped := false
	for iterator.index < len(iterator.text) && asciiSpace(iterator.text[iterator.index]) {
		iterator.index++
		skipped = true
	}
	if iterator.index == len(iterator.text) {
		return 0, false
	}
	if skipped && iterator.started {
		iterator.pendingValue = true
		return ' ', true
	}
	iterator.started = true
	value := lowerASCII(iterator.text[iterator.index])
	iterator.index++
	return value, true
}

func asciiSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r'
}

func lowerASCII(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}
