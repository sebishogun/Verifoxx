package natural

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"unicode/utf8"
)

func TestAppendSegmentsPreservesParagraphAndPageSpans(t *testing.T) {
	document, err := NewDocument([]byte("one\n\ntwo"), []uint32{0, 5}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	segments, err := AppendSegments(nil, document, 16, DefaultLimits())
	if err != nil {
		t.Fatalf("AppendSegments() error = %v", err)
	}
	want := []Segment{
		{Span: Span{Start: 0, End: 3}, PageStart: 0, PageEnd: 1},
		{Span: Span{Start: 5, End: 8}, PageStart: 1, PageEnd: 2},
	}
	if !reflect.DeepEqual(segments, want) {
		t.Fatalf("segments = %#v, want %#v", segments, want)
	}
}

func TestAppendSegmentsSplitsLongParagraphAtUTF8Boundaries(t *testing.T) {
	document, err := NewDocument([]byte("ab世界cd"), []uint32{0}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	segments, err := AppendSegments(nil, document, 5, DefaultLimits())
	if err != nil {
		t.Fatalf("AppendSegments() error = %v", err)
	}
	want := []Segment{
		{Span: Span{Start: 0, End: 5}, PageStart: 0, PageEnd: 1},
		{Span: Span{Start: 5, End: 10}, PageStart: 0, PageEnd: 1},
	}
	if !reflect.DeepEqual(segments, want) {
		t.Fatalf("segments = %#v, want %#v", segments, want)
	}
	for _, segment := range segments {
		if !utf8.Valid(document.Source[segment.Span.Start:segment.Span.End]) {
			t.Fatalf("segment %#v splits UTF-8", segment)
		}
	}
}

func TestAppendSegmentsSkipsEmptyRuns(t *testing.T) {
	document, err := NewDocument([]byte("\n\nalpha\n\n\n\nbeta\n\n"), []uint32{0}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	segments, err := AppendSegments(nil, document, 16, DefaultLimits())
	if err != nil {
		t.Fatalf("AppendSegments() error = %v", err)
	}
	if got, want := len(segments), 2; got != want {
		t.Fatalf("segment count = %d, want %d", got, want)
	}
	if got := string(document.Source[segments[0].Span.Start:segments[0].Span.End]); got != "alpha" {
		t.Fatalf("first segment = %q, want alpha", got)
	}
	if got := string(document.Source[segments[1].Span.Start:segments[1].Span.End]); got != "beta" {
		t.Fatalf("second segment = %q, want beta", got)
	}
}

func TestAppendSegmentsLimitFailureIsAtomic(t *testing.T) {
	document, err := NewDocument([]byte("one\n\ntwo"), []uint32{0}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	dst := []Segment{{Span: Span{Start: 9, End: 10}}}
	want := append([]Segment(nil), dst...)
	limits := DefaultLimits()
	limits.MaxSegments = 1
	got, err := AppendSegments(dst, document, 16, limits)
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("AppendSegments() error = %v, want ErrLimit", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("destination changed on error: got %#v, want %#v", got, want)
	}
}

func TestFixtureProviderIsDeterministic(t *testing.T) {
	document, err := NewDocument([]byte("R1 must remain local."), []uint32{0}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	segments, err := AppendSegments(nil, document, 64, DefaultLimits())
	if err != nil {
		t.Fatalf("AppendSegments() error = %v", err)
	}
	provider := FixtureProvider{
		Provider: ProviderInfo{ID: "fixture", Version: "1"},
		Rows: []FixtureRow{
			{
				Kind: ItemKindRequirement,
				Text: []byte("R1"),
				Citations: []FixtureCitation{{
					Page: 0, Span: Span{Start: 0, End: 2}, Quote: []byte("R1"),
				}},
			},
			{
				Kind: ItemKindRestriction, Parent: 1,
				Text: []byte("must remain local"),
				Citations: []FixtureCitation{{
					Page: 0, Span: Span{Start: 3, End: 20}, Quote: []byte("must remain local"),
				}},
			},
		},
	}

	var firstBuilder, secondBuilder Builder
	firstBuilder.Reset(document.Digest, provider.Info(), DefaultLimits())
	if err := provider.Extract(context.Background(), document, segments, &firstBuilder, DefaultLimits()); err != nil {
		t.Fatalf("first Extract() error = %v", err)
	}
	secondBuilder.Reset(document.Digest, provider.Info(), DefaultLimits())
	if err := provider.Extract(context.Background(), document, segments, &secondBuilder, DefaultLimits()); err != nil {
		t.Fatalf("second Extract() error = %v", err)
	}
	if first, second := firstBuilder.Finish(), secondBuilder.Finish(); !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated extraction differs:\nfirst:  %#v\nsecond: %#v", first, second)
	}
}

func TestFixtureProviderCancellationDoesNotMutateBuilder(t *testing.T) {
	document, err := NewDocument([]byte("R1"), []uint32{0}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	provider := FixtureProvider{
		Provider: ProviderInfo{ID: "fixture", Version: "1"},
		Rows: []FixtureRow{{
			Kind: ItemKindRequirement, Text: []byte("R1"),
			Citations: []FixtureCitation{{Page: 0, Span: Span{Start: 0, End: 2}, Quote: []byte("R1")}},
		}},
	}
	var builder Builder
	builder.Reset(document.Digest, provider.Info(), DefaultLimits())
	want := builder.Finish()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := provider.Extract(ctx, document, nil, &builder, DefaultLimits()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Extract() error = %v, want context.Canceled", err)
	}
	if got := builder.Finish(); !reflect.DeepEqual(got, want) {
		t.Fatalf("builder changed after cancellation:\n got: %#v\nwant: %#v", got, want)
	}
}

func TestFixtureProviderLimitFailureDoesNotPublishPartialRows(t *testing.T) {
	document, err := NewDocument([]byte("R1"), []uint32{0}, DefaultLimits())
	if err != nil {
		t.Fatalf("NewDocument() error = %v", err)
	}
	provider := FixtureProvider{
		Provider: ProviderInfo{ID: "fixture", Version: "1"},
		Rows: []FixtureRow{
			{Kind: ItemKindRequirement, Text: []byte("R1"), Citations: []FixtureCitation{{Page: 0, Span: Span{Start: 0, End: 2}, Quote: []byte("R1")}}},
			{Kind: ItemKindRestriction, Parent: 1, Text: []byte("X"), Citations: []FixtureCitation{{Page: 0, Span: Span{Start: 0, End: 2}, Quote: []byte("R1")}}},
		},
	}
	limits := DefaultLimits()
	limits.MaxItems = 1
	var builder Builder
	builder.Reset(document.Digest, provider.Info(), limits)
	want := builder.Finish()
	if err := provider.Extract(context.Background(), document, nil, &builder, limits); !errors.Is(err, ErrLimit) {
		t.Fatalf("Extract() error = %v, want ErrLimit", err)
	}
	if got := builder.Finish(); !reflect.DeepEqual(got, want) {
		t.Fatalf("builder contains partial extraction:\n got: %#v\nwant: %#v", got, want)
	}
}

func FuzzSegments(f *testing.F) {
	f.Add([]byte("alpha\n\nbeta"), uint8(8))
	f.Add([]byte("ab世界cd"), uint8(5))
	f.Fuzz(func(t *testing.T, source []byte, width uint8) {
		if len(source) == 0 || !utf8.Valid(source) {
			return
		}
		document, err := NewDocument(source, []uint32{0}, DefaultLimits())
		if err != nil {
			return
		}
		maxBytes := uint32(width%61 + 4)
		segments, err := AppendSegments(nil, document, maxBytes, DefaultLimits())
		if err != nil {
			t.Fatalf("AppendSegments() error = %v", err)
		}
		previousEnd := uint32(0)
		for _, segment := range segments {
			if segment.Span.Start < previousEnd || segment.Span.End <= segment.Span.Start || uint64(segment.Span.End) > uint64(len(source)) {
				t.Fatalf("invalid segment %#v after end %d for source length %d", segment, previousEnd, len(source))
			}
			if segment.Span.End-segment.Span.Start > maxBytes {
				t.Fatalf("segment %#v exceeds max %d", segment, maxBytes)
			}
			if !utf8.Valid(source[segment.Span.Start:segment.Span.End]) {
				t.Fatalf("segment %#v splits UTF-8", segment)
			}
			previousEnd = segment.Span.End
		}
	})
}
