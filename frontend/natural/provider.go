package natural

import (
	"context"
	"unicode/utf8"
)

// Segment is one exact source range supplied to an extraction provider.
// PageEnd is exclusive.
type Segment struct {
	Span      Span
	PageStart uint32
	PageEnd   uint32
}

// Provider extracts a bounded, non-executable proposal. It receives no
// compilation, persistence, registry, or publication capability.
type Provider interface {
	Info() ProviderInfo
	Extract(context.Context, *Document, []Segment, *Builder, Limits) error
}

// AppendSegments appends deterministic paragraph segments to dst. On error it
// returns dst unchanged.
func AppendSegments(dst []Segment, document *Document, maxBytes uint32, limits Limits) ([]Segment, error) {
	base := len(dst)
	if document == nil || len(document.Source) == 0 || maxBytes < utf8.UTFMax {
		return dst, ErrInvalidDocument
	}

	source := document.Source
	for cursor := 0; cursor < len(source); {
		for cursor < len(source) && source[cursor] == '\n' {
			cursor++
		}
		if cursor == len(source) {
			break
		}

		paragraphStart := cursor
		paragraphEnd := len(source)
		for cursor < len(source) {
			if source[cursor] != '\n' {
				cursor++
				continue
			}
			runStart := cursor
			for cursor < len(source) && source[cursor] == '\n' {
				cursor++
			}
			if cursor-runStart >= 2 {
				paragraphEnd = runStart
				break
			}
		}

		for start := paragraphStart; start < paragraphEnd; {
			end := start + int(maxBytes)
			if end >= paragraphEnd {
				end = paragraphEnd
			} else {
				for end > start && !utf8.RuneStart(source[end]) {
					end--
				}
			}
			if end == start {
				return dst[:base], ErrInvalidDocument
			}
			if overLimit(len(dst)+1, limits.MaxSegments) || uint64(start) > uint64(^uint32(0)) || uint64(end) > uint64(^uint32(0)) {
				return dst[:base], ErrLimit
			}
			pageStart := pageForOffset(document.PageStarts, uint32(start))
			pageEnd := pageForOffset(document.PageStarts, uint32(end-1)) + 1
			dst = append(dst, Segment{
				Span:      Span{Start: uint32(start), End: uint32(end)},
				PageStart: pageStart,
				PageEnd:   pageEnd,
			})
			start = end
		}
	}
	return dst, nil
}

func pageForOffset(starts []uint32, offset uint32) uint32 {
	low, high := 0, len(starts)
	for low < high {
		middle := low + (high-low)/2
		if starts[middle] <= offset {
			low = middle + 1
		} else {
			high = middle
		}
	}
	return uint32(low - 1)
}

// FixtureCitation is one deterministic provider citation.
type FixtureCitation struct {
	Quote []byte
	Span  Span
	Page  uint32
}

// FixtureRow is one deterministic extracted item.
type FixtureRow struct {
	Text      []byte
	Citations []FixtureCitation
	Parent    ItemID
	Kind      ItemKind
}

// FixtureProvider supplies deterministic offline extraction for tests and
// demonstrations. It is not safe for concurrent use.
type FixtureProvider struct {
	Provider    ProviderInfo
	Rows        []FixtureRow
	citationIDs []CitationID
}

var _ Provider = (*FixtureProvider)(nil)

// Info returns the stable provider identity.
func (provider *FixtureProvider) Info() ProviderInfo {
	if provider == nil {
		return ProviderInfo{}
	}
	return provider.Provider
}

// Extract appends all fixture rows atomically.
func (provider *FixtureProvider) Extract(ctx context.Context, document *Document, _ []Segment, builder *Builder, limits Limits) error {
	if provider == nil || document == nil || builder == nil {
		return ErrInvalidProposal
	}
	saved := *builder
	maxCitations := 0
	for row := range provider.Rows {
		if len(provider.Rows[row].Citations) > maxCitations {
			maxCitations = len(provider.Rows[row].Citations)
		}
	}
	if overLimit(len(provider.Rows), limits.MaxItems) || overLimit(maxCitations, limits.MaxCitations) {
		return ErrLimit
	}
	provider.citationIDs = resizeFixtureIDs(provider.citationIDs, maxCitations)

	for row := range provider.Rows {
		if err := ctx.Err(); err != nil {
			*builder = saved
			return err
		}
		fixture := &provider.Rows[row]
		ids := provider.citationIDs[:len(fixture.Citations)]
		for citationRow := range fixture.Citations {
			if err := ctx.Err(); err != nil {
				*builder = saved
				return err
			}
			citation := &fixture.Citations[citationRow]
			id, err := builder.AddCitation(citation.Page, citation.Span, citation.Quote)
			if err != nil {
				*builder = saved
				return err
			}
			ids[citationRow] = id
		}
		if _, err := builder.AddItem(fixture.Kind, fixture.Parent, fixture.Text, ids); err != nil {
			*builder = saved
			return err
		}
	}
	return nil
}

func resizeFixtureIDs(ids []CitationID, length int) []CitationID {
	if cap(ids) < length {
		return make([]CitationID, length)
	}
	return ids[:length]
}
