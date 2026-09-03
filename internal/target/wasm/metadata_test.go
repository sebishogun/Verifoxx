package wasm

import (
	"errors"
	"testing"
)

func TestMetadataRejectsProfileCapabilityMismatch(t *testing.T) {
	manifest := testManifest()
	manifest.Profile = ProfileWASI
	manifest.RequiredCapabilities = CapabilityNetwork
	metadata := Metadata{
		ABI: manifest.ABI, Schema: manifest.Schema, Profile: manifest.Profile,
		RequiredCapabilities: manifest.RequiredCapabilities, Limits: manifest.Limits,
	}
	if _, err := encodeMetadata(make([]byte, MetadataBytes), metadata); !errors.Is(err, errInvalidMetadata) {
		t.Fatalf("encode mismatched metadata: got %v, want %v", err, errInvalidMetadata)
	}

	metadata.Profile = ProfileEnvoy
	record := make([]byte, MetadataBytes)
	if _, err := encodeMetadata(record, metadata); err != nil {
		t.Fatalf("encode valid metadata: %v", err)
	}
	record[8] = byte(ProfileWASI)
	if _, err := DecodeMetadata(record); !errors.Is(err, errInvalidMetadata) {
		t.Fatalf("decode mismatched metadata: got %v, want %v", err, errInvalidMetadata)
	}
}
