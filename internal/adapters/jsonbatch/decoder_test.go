package jsonbatch

import (
	"errors"
	"math"
	"testing"
)

func requireDecodeError(t *testing.T, err error, input Input, code ErrorCode) *Error {
	t.Helper()
	var got *Error
	if !errors.As(err, &got) {
		t.Fatalf("error = %v, want *Error", err)
	}
	if got.Input != input || got.Code != code {
		t.Fatalf("error = %+v, want input=%s code=%s", got, input, code)
	}
	return got
}

func TestScannerDecodesStringsIntegersAndLiterals(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		var s scanner
		s.reset(InputRequests, []byte(`"a\n\u4e2d\ud83d\ude00"`), Limits{})
		got, err := s.parseString(&s.valueScratch)
		if err != nil || string(got) != "a\n中😀" {
			t.Fatalf("parseString = (%q, %v)", got, err)
		}
		if err := s.finish(); err != nil {
			t.Fatalf("finish: %v", err)
		}
	})

	for _, tc := range []struct {
		src  string
		want int64
	}{
		{"0", 0},
		{"-1", -1},
		{"9223372036854775807", math.MaxInt64},
		{"-9223372036854775808", math.MinInt64},
	} {
		t.Run(tc.src, func(t *testing.T) {
			var s scanner
			s.reset(InputEvidence, []byte(tc.src), Limits{})
			got, err := s.parseInteger()
			if err != nil || got != tc.want {
				t.Fatalf("parseInteger = (%d, %v), want %d", got, err, tc.want)
			}
		})
	}

	var s scanner
	s.reset(InputRequests, []byte(`{"a":[true,false,null,{"b":-2}]}`), Limits{MaxDepth: 4})
	if err := s.skipValue(1); err != nil {
		t.Fatalf("skipValue: %v", err)
	}
	if err := s.finish(); err != nil {
		t.Fatalf("finish: %v", err)
	}
}

func TestScannerReturnsBoundedPositionalErrors(t *testing.T) {
	tests := []struct {
		name   string
		input  Input
		src    []byte
		limits Limits
		code   ErrorCode
		call   func(*scanner) error
	}{
		{"truncated string", InputRequests, []byte(`"x`), Limits{}, CodeTruncated, func(s *scanner) error { _, err := s.parseString(&s.valueScratch); return err }},
		{"bad escape", InputEvidence, []byte(`"\q"`), Limits{}, CodeMalformed, func(s *scanner) error { _, err := s.parseString(&s.valueScratch); return err }},
		{"invalid utf8", InputRequests, []byte{'"', 0xff, '"'}, Limits{}, CodeInvalidUTF8, func(s *scanner) error { _, err := s.parseString(&s.valueScratch); return err }},
		{"string limit", InputEvidence, []byte(`"long"`), Limits{MaxStringBytes: 3}, CodeLimit, func(s *scanner) error { _, err := s.parseString(&s.valueScratch); return err }},
		{"integer overflow", InputRequests, []byte(`9223372036854775808`), Limits{}, CodeLimit, func(s *scanner) error { _, err := s.parseInteger(); return err }},
		{"leading zero", InputEvidence, []byte(`01`), Limits{}, CodeMalformed, func(s *scanner) error { _, err := s.parseInteger(); return err }},
		{"depth", InputRequests, []byte(`[[[]]]`), Limits{MaxDepth: 2}, CodeLimit, func(s *scanner) error { return s.skipValue(1) }},
		{"trailing", InputEvidence, []byte(`null x`), Limits{}, CodeTrailing, func(s *scanner) error {
			if err := s.skipValue(1); err != nil {
				return err
			}
			return s.finish()
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s scanner
			s.reset(tc.input, tc.src, tc.limits)
			err := tc.call(&s)
			got := requireDecodeError(t, err, tc.input, tc.code)
			if got.Offset < 0 || got.Offset > len(tc.src) {
				t.Fatalf("error offset %d outside [0,%d]", got.Offset, len(tc.src))
			}
		})
	}
}

func TestInputAndErrorCodeNamesAreStable(t *testing.T) {
	if InputRequests.String() != "requests" || InputEvidence.String() != "evidence" {
		t.Fatalf("input names = (%q, %q)", InputRequests, InputEvidence)
	}
	if CodeMalformed.String() != "malformed" || CodeLimit.String() != "limit_exceeded" {
		t.Fatalf("code names = (%q, %q)", CodeMalformed, CodeLimit)
	}
}
