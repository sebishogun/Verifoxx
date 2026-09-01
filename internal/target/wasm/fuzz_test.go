package wasm

import (
	"testing"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
)

func FuzzProgramArtifactDecode(f *testing.F) {
	compiled := compileWASMTestProgram(f)
	manifest := testManifest()
	artifact, err := EncodeProgram(nil, compiled, manifest)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(artifact)
	f.Add([]byte("not an artifact"))
	f.Fuzz(func(t *testing.T, source []byte) {
		_, _, _ = DecodeProgram(source, manifest.Limits)
	})
}

func FuzzInputFrameDecode(f *testing.F) {
	compiled := compileWASMTestProgram(f)
	manifest := testManifest()
	frame, err := EncodeInputFrame(nil, buildWASMTestBatch(f, compiled), manifest.Limits)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(frame)
	f.Add([]byte("not a frame"))
	f.Fuzz(func(t *testing.T, source []byte) {
		var destination eval.Batch
		_ = DecodeInputFrame(&destination, source, manifest.Limits)
	})
}

func FuzzResultFrameDecode(f *testing.F) {
	manifest := testManifest()
	frame, err := EncodeResultFrame(nil, result.Batch{
		Rows: 1, OutcomeIDs: []schema.OutcomeID{1},
		RequirementOffsets: []uint32{0, 0}, DriverOffsets: []uint32{0, 0},
		EvidenceOffsets: []uint32{0, 0}, ReasonOffsets: []uint32{0, 0}, RemediationOffsets: []uint32{0, 0},
	}, manifest.Limits)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(frame)
	f.Add([]byte("not a result"))
	f.Fuzz(func(t *testing.T, source []byte) {
		var destination result.Batch
		_ = DecodeResultFrame(&destination, source, manifest.Limits)
	})
}
