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

func TestCountBatchShapesInArbitraryRootOrder(t *testing.T) {
	requests := []byte(`{"requests":[{"evidence_refs":["E1","E2"],"id":"R1","facts":{}},{"id":"R2"}],"pack":"p","schema_version":1}`)
	evidence := []byte(`{"evidence":[{"id":"E1"},{"id":"E2"},{"id":"E3"}],"schema_version":1,"pack":"p"}`)
	var d Decoder
	requestShape, err := d.count(InputRequests, requests, Limits{})
	if err != nil {
		t.Fatalf("count requests: %v", err)
	}
	if requestShape.requests != 2 || requestShape.refs != 2 || requestShape.evidence != 0 {
		t.Fatalf("request shape = %+v, want 2 requests and 2 refs", requestShape)
	}
	evidenceShape, err := d.count(InputEvidence, evidence, Limits{})
	if err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	if evidenceShape.evidence != 3 || evidenceShape.requests != 0 || evidenceShape.refs != 0 {
		t.Fatalf("evidence shape = %+v, want 3 evidence rows", evidenceShape)
	}
}

func TestCountRejectsInvalidRootsAndLimits(t *testing.T) {
	tests := []struct {
		name   string
		input  Input
		source string
		limits Limits
		code   ErrorCode
	}{
		{"unknown key", InputRequests, `{"schema_version":1,"pack":"p","requests":[],"extra":0}`, Limits{}, CodeUnknownKey},
		{"duplicate key", InputEvidence, `{"schema_version":1,"schema_version":1,"pack":"p","evidence":[]}`, Limits{}, CodeDuplicateKey},
		{"missing key", InputRequests, `{"schema_version":1,"requests":[]}`, Limits{}, CodeMissingKey},
		{"version", InputEvidence, `{"schema_version":2,"pack":"p","evidence":[]}`, Limits{}, CodeInvalidVersion},
		{"payload type", InputRequests, `{"schema_version":1,"pack":"p","requests":{}}`, Limits{}, CodeInvalidType},
		{"row type", InputEvidence, `{"schema_version":1,"pack":"p","evidence":[null]}`, Limits{}, CodeInvalidType},
		{"request limit", InputRequests, `{"schema_version":1,"pack":"p","requests":[{},{}]}`, Limits{MaxRequests: 1}, CodeLimit},
		{"evidence limit", InputEvidence, `{"schema_version":1,"pack":"p","evidence":[{},{}]}`, Limits{MaxEvidence: 1}, CodeLimit},
		{"reference limit", InputRequests, `{"schema_version":1,"pack":"p","requests":[{"evidence_refs":["E1","E2"]}]}`, Limits{MaxEvidenceRefs: 1}, CodeLimit},
		{"source limit", InputRequests, `{"schema_version":1,"pack":"p","requests":[]}`, Limits{MaxRequestBytes: 4}, CodeLimit},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var d Decoder
			_, err := d.count(tc.input, []byte(tc.source), tc.limits)
			requireDecodeError(t, err, tc.input, tc.code)
		})
	}
}
