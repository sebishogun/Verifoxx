package jsonbatch

import (
	"errors"
	"testing"

	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/fixtures"
)

func FuzzDecodeBatch(f *testing.F) {
	canonicalRequests := []byte(fixtures.RequestsJSON())
	canonicalEvidence := []byte(fixtures.EvidenceJSON())
	f.Add(canonicalRequests, canonicalEvidence)
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
		batch, err := decoder.Decode(&builder, p, requests, evidence, limits)
		if err == nil {
			if uint64(len(batch.RequestIDs)) != uint64(batch.Rows) ||
				uint64(len(batch.EvidenceOffsets)) != uint64(batch.Rows)+1 {
				t.Fatalf("decoded batch shape = rows:%d requests:%d offsets:%d",
					batch.Rows, len(batch.RequestIDs), len(batch.EvidenceOffsets))
			}
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
		if _, reuseErr := decoder.Decode(&builder, p, canonicalRequests, canonicalEvidence, limits); reuseErr != nil {
			t.Fatalf("decoder reuse after arbitrary input: %v", reuseErr)
		}
	})
}
