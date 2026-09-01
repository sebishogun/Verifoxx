package wasm

import (
	"bytes"
	"errors"
	"testing"
)

func TestEnvelopeBuildIsDeterministicAlignedAndOwned(t *testing.T) {
	manifest := testManifest()
	firstData := []byte{1, 2, 3, 4}
	sections := []sectionSpec{
		{id: 1, width: 1, count: 4, data: firstData},
		{id: 2, width: 8, count: 1, data: []byte{8, 7, 6, 5, 4, 3, 2, 1}},
	}
	prefix := []byte("reuse")
	first, err := buildEnvelope(prefix[:0], manifest, sections)
	if err != nil {
		t.Fatal(err)
	}
	second, err := buildEnvelope(nil, manifest, sections)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("same sections produced different artifacts")
	}
	firstData[0] = 99
	header, descriptors, err := preflightEnvelope(first, manifest.Limits)
	if err != nil {
		t.Fatal(err)
	}
	if header.abi != CurrentABIVersion || header.schema != CurrentSchemaVersion || len(descriptors) != 2 {
		t.Fatalf("header = %+v, descriptors = %d", header, len(descriptors))
	}
	if first[descriptors[0].offset] != 1 {
		t.Fatal("artifact borrowed source section")
	}
	for _, descriptor := range descriptors {
		if descriptor.offset%uint64(descriptor.alignment) != 0 {
			t.Fatalf("section %d offset %d is not aligned to %d", descriptor.id, descriptor.offset, descriptor.alignment)
		}
	}
}

func TestEnvelopePreflightRejectsCorruptionBeforePublication(t *testing.T) {
	manifest := testManifest()
	artifact, err := buildEnvelope(nil, manifest, []sectionSpec{{id: 1, width: 4, count: 1, data: []byte{1, 0, 0, 0}}})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func([]byte)
		limits Limits
	}{
		{name: "magic", mutate: func(src []byte) { src[0] ^= 0xff }, limits: manifest.Limits},
		{name: "checksum", mutate: func(src []byte) { src[len(src)-1] ^= 0xff }, limits: manifest.Limits},
		{name: "truncated", mutate: func(src []byte) { clear(src[8:]) }, limits: manifest.Limits},
		{name: "limit", mutate: func([]byte) {}, limits: Limits{MaxArtifactBytes: uint64(len(artifact) - 1)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := append([]byte(nil), artifact...)
			test.mutate(candidate)
			if _, _, gotErr := preflightEnvelope(candidate, test.limits); !errors.Is(gotErr, errInvalidArtifact) {
				t.Fatalf("preflightEnvelope() error = %v, want %v", gotErr, errInvalidArtifact)
			}
		})
	}
}

func TestEnvelopeBuildRejectsUnorderedAndMismatchedSections(t *testing.T) {
	manifest := testManifest()
	tests := [][]sectionSpec{
		{{id: 2, width: 1, count: 1, data: []byte{1}}, {id: 1, width: 1, count: 1, data: []byte{2}}},
		{{id: 1, width: 4, count: 2, data: []byte{1, 2, 3, 4}}},
		{{id: 1, width: 3, count: 1, data: []byte{1, 2, 3}}},
	}
	for row, sections := range tests {
		if _, err := buildEnvelope(nil, manifest, sections); !errors.Is(err, errInvalidArtifact) {
			t.Fatalf("case %d error = %v, want %v", row, err, errInvalidArtifact)
		}
	}
}

func testManifest() Manifest {
	return Manifest{
		ABI: CurrentABIVersion, Schema: CurrentSchemaVersion,
		Profile: ProfileWASI,
		Limits: Limits{
			MaxArtifactBytes: 1 << 20, MaxInputBytes: 1 << 20, MaxOutputBytes: 1 << 20,
			MaxFuel: 1 << 20, MaxRows: 1024, MaxProgramColumns: 256,
		},
	}
}
