package jsonbatch

import (
	"errors"
	"testing"

	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/fixtures"
)

func FuzzDecodeBatch(f *testing.F) {
	f.Add([]byte(fixtures.RequestsJSON()), []byte(fixtures.EvidenceJSON()))
	f.Add([]byte(`{"schema_version":1,"pack":"verifoxx","requests":[]}`), []byte(`{"schema_version":1,"pack":"verifoxx","evidence":[]}`))
	f.Add([]byte(`{"schema_version":1,"pack":"verifoxx","requests":[`), []byte(`null`))
	p := fixtureDecoderProgram(f)
	limits := Limits{
		MaxRequestBytes:       4096,
		MaxEvidenceBytes:      4096,
		MaxStringBytes:        256,
		MaxRequests:           64,
		MaxEvidence:           64,
		MaxEvidenceRefs:       256,
		MaxFactsPerRequest:    32,
		MaxEvidenceAttributes: 32,
		MaxDepth:              8,
	}
	f.Fuzz(func(t *testing.T, requests, evidence []byte) {
		var decoder Decoder
		var builder eval.Builder
		_, err := decoder.Decode(&builder, p, requests, evidence, limits)
		if err == nil {
			return
		}
		var decodeErr *Error
		if errors.As(err, &decodeErr) {
			length := len(requests)
			if decodeErr.Input == InputEvidence {
				length = len(evidence)
			}
			if decodeErr.Offset < 0 || decodeErr.Offset > length {
				t.Fatalf("error offset %d outside [0,%d]: %v", decodeErr.Offset, length, err)
			}
		}
	})
}
