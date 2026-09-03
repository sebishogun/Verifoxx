package wasm

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
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
	header, descriptors, err := preflightEnvelope(first, manifest)
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

func TestEnvelopeBuildAppendsToDestination(t *testing.T) {
	manifest := testManifest()
	sections := []sectionSpec{{id: 1, width: 4, count: 1, data: []byte{1, 2, 3, 4}}}
	artifact, err := buildEnvelope(nil, manifest, sections)
	if err != nil {
		t.Fatal(err)
	}
	prefix := []byte("prefix")
	for _, capacity := range []int{len(prefix) + len(artifact), len(prefix)} {
		dst := make([]byte, len(prefix), capacity)
		copy(dst, prefix)
		first := &dst[0]
		got, err := buildEnvelope(dst, manifest, sections)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got[:len(prefix)], prefix) || !bytes.Equal(got[len(prefix):], artifact) {
			t.Fatalf("capacity %d: append result differs", capacity)
		}
		if capacity > len(prefix) && &got[0] != first {
			t.Fatalf("capacity %d: append replaced sufficient backing storage", capacity)
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
		host   Manifest
	}{
		{name: "magic", mutate: func(src []byte) { src[0] ^= 0xff }, host: manifest},
		{name: "checksum", mutate: func(src []byte) { src[len(src)-1] ^= 0xff }, host: manifest},
		{name: "truncated", mutate: func(src []byte) { clear(src[8:]) }, host: manifest},
		{name: "limit", mutate: func([]byte) {}, host: func() Manifest {
			host := manifest
			host.Limits.MaxArtifactBytes = uint64(len(artifact) - 1)
			return host
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := append([]byte(nil), artifact...)
			test.mutate(candidate)
			if _, _, gotErr := preflightEnvelope(candidate, test.host); !errors.Is(gotErr, errInvalidArtifact) {
				t.Fatalf("preflightEnvelope() error = %v, want %v", gotErr, errInvalidArtifact)
			}
		})
	}
}

func TestVersion1ArtifactEnvelopePinsManifestLimits(t *testing.T) {
	manifest := testManifest()
	manifest.Profile = ProfileEnvoy
	manifest.RequiredCapabilities = CapabilityClock
	manifest.Limits = Limits{
		MaxArtifactBytes:  0x0102030405060708,
		MaxInputBytes:     0x1112131415161718,
		MaxOutputBytes:    0x2122232425262728,
		MaxFuel:           0x3132333435363738,
		MaxRows:           0x41424344,
		MaxProgramColumns: 0x51525354,
	}
	artifact, err := buildEnvelope(nil, manifest, []sectionSpec{{id: 1, width: 1, count: 1, data: []byte{7}}})
	if err != nil {
		t.Fatal(err)
	}
	if artifactHeaderBytes != 104 || frameHeaderBytes != 64 {
		t.Fatalf("header bytes = artifact %d, frame %d", artifactHeaderBytes, frameHeaderBytes)
	}
	if got := binary.LittleEndian.Uint64(artifact[64:72]); got != manifest.Limits.MaxArtifactBytes {
		t.Fatalf("MaxArtifactBytes = %#x", got)
	}
	if got := binary.LittleEndian.Uint64(artifact[72:80]); got != manifest.Limits.MaxInputBytes {
		t.Fatalf("MaxInputBytes = %#x", got)
	}
	if got := binary.LittleEndian.Uint64(artifact[80:88]); got != manifest.Limits.MaxOutputBytes {
		t.Fatalf("MaxOutputBytes = %#x", got)
	}
	if got := binary.LittleEndian.Uint64(artifact[88:96]); got != manifest.Limits.MaxFuel {
		t.Fatalf("MaxFuel = %#x", got)
	}
	if got := binary.LittleEndian.Uint32(artifact[96:100]); got != manifest.Limits.MaxRows {
		t.Fatalf("MaxRows = %#x", got)
	}
	if got := binary.LittleEndian.Uint32(artifact[100:104]); got != manifest.Limits.MaxProgramColumns {
		t.Fatalf("MaxProgramColumns = %#x", got)
	}
	if firstDescriptor := binary.LittleEndian.Uint16(artifact[104:106]); firstDescriptor != 1 {
		t.Fatalf("first descriptor ID = %d", firstDescriptor)
	}
	header, _, err := preflightEnvelope(artifact, manifest)
	if err != nil || header.limits != manifest.Limits {
		t.Fatalf("preflight header = %+v, %v", header, err)
	}
}

func TestArtifactManifestLimitsAreHashBoundAndSelfBounded(t *testing.T) {
	manifest := testManifest()
	artifact, err := buildEnvelope(nil, manifest, []sectionSpec{{id: 1, width: 1, count: 1, data: []byte{7}}})
	if err != nil {
		t.Fatal(err)
	}
	limits := []struct {
		name   string
		offset int
		width  int
	}{
		{name: "artifact bytes", offset: 64, width: 8},
		{name: "input bytes", offset: 72, width: 8},
		{name: "output bytes", offset: 80, width: 8},
		{name: "fuel", offset: 88, width: 8},
		{name: "rows", offset: 96, width: 4},
		{name: "columns", offset: 100, width: 4},
	}
	for _, limit := range limits {
		t.Run(limit.name+" checksum", func(t *testing.T) {
			candidate := append([]byte(nil), artifact...)
			candidate[limit.offset]++
			if _, _, gotErr := preflightEnvelope(candidate, manifest); !errors.Is(gotErr, errInvalidArtifact) {
				t.Fatalf("preflight error = %v, want invalid artifact", gotErr)
			}
		})
		t.Run(limit.name+" mismatch", func(t *testing.T) {
			candidate := append([]byte(nil), artifact...)
			candidate[limit.offset]++
			rehashArtifact(candidate)
			if _, _, gotErr := preflightEnvelope(candidate, manifest); !errors.Is(gotErr, ErrIncompatibleVersion) {
				t.Fatalf("preflight error = %v, want incompatible version", gotErr)
			}
		})
		t.Run(limit.name+" zero", func(t *testing.T) {
			candidate := append([]byte(nil), artifact...)
			clear(candidate[limit.offset : limit.offset+limit.width])
			rehashArtifact(candidate)
			if _, _, gotErr := preflightEnvelope(candidate, manifest); !errors.Is(gotErr, errInvalidArtifact) {
				t.Fatalf("preflight error = %v, want invalid artifact", gotErr)
			}
		})
	}
	t.Run("artifact length exceeds artifact manifest", func(t *testing.T) {
		candidate := append([]byte(nil), artifact...)
		binary.LittleEndian.PutUint64(candidate[64:72], uint64(len(candidate)-1))
		rehashArtifact(candidate)
		if _, _, gotErr := preflightEnvelope(candidate, manifest); !errors.Is(gotErr, errInvalidArtifact) {
			t.Fatalf("preflight error = %v, want invalid artifact", gotErr)
		}
	})
	t.Run("sections exceed artifact manifest", func(t *testing.T) {
		candidate := append([]byte(nil), artifact...)
		binary.LittleEndian.PutUint32(candidate[100:104], 0)
		rehashArtifact(candidate)
		if _, _, gotErr := preflightEnvelope(candidate, manifest); !errors.Is(gotErr, errInvalidArtifact) {
			t.Fatalf("preflight error = %v, want invalid artifact", gotErr)
		}
	})
}

func rehashArtifact(artifact []byte) {
	clear(artifact[artifactHashOffset : artifactHashOffset+artifactHashBytes])
	sum := sha256.Sum256(artifact)
	copy(artifact[artifactHashOffset:artifactHashOffset+artifactHashBytes], sum[:])
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
			MaxArtifactBytes: 16 << 20, MaxInputBytes: 16 << 20, MaxOutputBytes: 64 << 20,
			MaxFuel: math.MaxUint32, MaxRows: 1 << 16, MaxProgramColumns: 256,
		},
	}
}
