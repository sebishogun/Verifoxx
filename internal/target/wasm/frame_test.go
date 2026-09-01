package wasm

import (
	"reflect"
	"testing"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
)

func TestInputFrameRoundTripIsDeterministicOwnedAndReusable(t *testing.T) {
	input := eval.Batch{
		SymbolValues:    []schema.SymbolID{10, 11, 12, 13},
		IntegerValues:   []int64{-1, 2},
		TimestampValues: []int64{100, 200},
		BooleanValues:   []uint64{1},
		PresenceMasks:   []uint64{3, 1},
		RequestIDs:      []schema.RequestID{1, 2},
		EvidenceOffsets: []uint32{0, 1, 1},
		EvidenceRefs:    []uint32{0},
		Evidence: eval.EvidenceBatch{
			IDs: []schema.EvidenceID{1}, Kinds: []schema.EvidenceKindID{1}, States: []schema.EvidenceStateID{2},
			Subjects: []schema.SymbolID{10}, Scopes: []schema.SymbolID{11}, Reviewers: []schema.SymbolID{12},
			Timings: []schema.SymbolID{13}, Timestamps: []int64{123},
		},
		Rows: 2,
	}
	limits := testManifest().Limits
	encoded, err := EncodeInputFrame(nil, input, limits)
	if err != nil {
		t.Fatal(err)
	}
	again, err := EncodeInputFrame(nil, input, limits)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(encoded, again) {
		t.Fatal("input frame encoding is not deterministic")
	}

	destination := eval.Batch{RequestIDs: make([]schema.RequestID, 2, 8)}
	requestStorage := &destination.RequestIDs[0]
	if err := DecodeInputFrame(&destination, encoded, limits); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, destination) {
		t.Fatalf("decoded input = %#v, want %#v", destination, input)
	}
	if requestStorage != &destination.RequestIDs[0] {
		t.Fatal("DecodeInputFrame discarded reusable request storage")
	}
	destination.SymbolValues[0]++
	if input.SymbolValues[0] == destination.SymbolValues[0] {
		t.Fatal("decoded input borrowed source batch")
	}
}

func TestResultFrameRoundTripPreservesAllCSRColumns(t *testing.T) {
	output := result.Batch{
		OutcomeIDs:         []schema.OutcomeID{1, 4},
		RequirementOffsets: []uint32{0, 1, 3}, RequirementIDs: []schema.RequirementID{1, 1, 2},
		DriverOffsets: []uint32{0, 1, 2}, DriverRequirements: []schema.RequirementID{1, 2},
		DriverClauses: []schema.ClauseID{1, 2}, DriverNodes: []schema.NodeID{3, 4},
		DriverReasons: []schema.ReasonID{1, 2}, DriverExplanations: []schema.ExplanationID{1, 2},
		EvidenceOffsets: []uint32{0, 0, 1}, EvidenceIDs: []schema.EvidenceID{1},
		ReasonOffsets: []uint32{0, 1, 2}, ReasonIDs: []schema.ReasonID{1, 2}, ReasonNodes: []schema.NodeID{3, 4},
		ReasonEvidenceIDs: []schema.EvidenceID{0, 1}, ReasonEvidenceStates: []schema.EvidenceStateID{0, 2},
		RemediationOffsets: []uint32{0, 0, 1}, RemediationIDs: []schema.RemediationID{1},
		Rows: 2,
	}
	limits := testManifest().Limits
	encoded, err := EncodeResultFrame(nil, output, limits)
	if err != nil {
		t.Fatal(err)
	}
	var decoded result.Batch
	if err := DecodeResultFrame(&decoded, encoded, limits); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(output, decoded) {
		t.Fatalf("decoded result = %#v, want %#v", decoded, output)
	}
}

func TestFrameCodecRejectsInvalidCSRKindAndLimits(t *testing.T) {
	limits := testManifest().Limits
	invalid := eval.Batch{Rows: 1, RequestIDs: []schema.RequestID{1}, EvidenceOffsets: []uint32{0, 1}}
	if _, err := EncodeInputFrame(nil, invalid, limits); err == nil {
		t.Fatal("EncodeInputFrame accepted invalid evidence CSR")
	}
	valid := eval.Batch{Rows: 1, RequestIDs: []schema.RequestID{1}, EvidenceOffsets: []uint32{0, 0}}
	encoded, err := EncodeInputFrame(nil, valid, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := DecodeResultFrame(new(result.Batch), encoded, limits); err == nil {
		t.Fatal("DecodeResultFrame accepted an input frame")
	}
	tooSmall := limits
	tooSmall.MaxInputBytes = uint64(len(encoded) - 1)
	if err := DecodeInputFrame(new(eval.Batch), encoded, tooSmall); err == nil {
		t.Fatal("DecodeInputFrame ignored MaxInputBytes")
	}
}

func TestFrameSchemaVersionPinsInputAndResultLayouts(t *testing.T) {
	if got := frameLayoutDigest(reflect.TypeFor[eval.Batch]()); got != version1InputFrameLayoutDigest {
		t.Errorf("input frame layout digest = %x, want %x; bump schema version for a wire-layout change", got, version1InputFrameLayoutDigest)
	}
	if got := frameLayoutDigest(reflect.TypeFor[result.Batch]()); got != version1ResultFrameLayoutDigest {
		t.Errorf("result frame layout digest = %x, want %x; bump schema version for a wire-layout change", got, version1ResultFrameLayoutDigest)
	}
}
