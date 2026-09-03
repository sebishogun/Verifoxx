package wasm

import (
	"encoding/binary"
	"errors"
)

const (
	MetadataBytes     = 128
	metadataMagic     = 0x4e52574d
	metadataHashStart = 56
)

var errInvalidMetadata = errors.New("wasm: invalid metadata")

func encodeMetadata(dst []byte, metadata Metadata) (int, error) {
	if len(dst) < MetadataBytes || !validMetadataManifest(metadata) {
		return 0, errInvalidMetadata
	}
	record := dst[:MetadataBytes]
	clear(record)
	binary.LittleEndian.PutUint32(record[0:4], metadataMagic)
	binary.LittleEndian.PutUint16(record[4:6], uint16(metadata.ABI))
	binary.LittleEndian.PutUint16(record[6:8], uint16(metadata.Schema))
	record[8] = byte(metadata.Profile)
	binary.LittleEndian.PutUint32(record[12:16], uint32(metadata.RequiredCapabilities))
	binary.LittleEndian.PutUint64(record[16:24], metadata.Limits.MaxArtifactBytes)
	binary.LittleEndian.PutUint64(record[24:32], metadata.Limits.MaxInputBytes)
	binary.LittleEndian.PutUint64(record[32:40], metadata.Limits.MaxOutputBytes)
	binary.LittleEndian.PutUint64(record[40:48], metadata.Limits.MaxFuel)
	binary.LittleEndian.PutUint32(record[48:52], metadata.Limits.MaxRows)
	binary.LittleEndian.PutUint32(record[52:56], metadata.Limits.MaxProgramColumns)
	copy(record[metadataHashStart:metadataHashStart+32], metadata.ArtifactHash[:])
	copy(record[metadataHashStart+32:metadataHashStart+64], metadata.ProgramHash[:])
	return MetadataBytes, nil
}

func DecodeMetadata(src []byte) (Metadata, error) {
	if len(src) != MetadataBytes || binary.LittleEndian.Uint32(src[0:4]) != metadataMagic {
		return Metadata{}, errInvalidMetadata
	}
	metadata := Metadata{
		ABI:                  ABIVersion(binary.LittleEndian.Uint16(src[4:6])),
		Schema:               SchemaVersion(binary.LittleEndian.Uint16(src[6:8])),
		Profile:              Profile(src[8]),
		RequiredCapabilities: Capability(binary.LittleEndian.Uint32(src[12:16])),
		Limits: Limits{
			MaxArtifactBytes:  binary.LittleEndian.Uint64(src[16:24]),
			MaxInputBytes:     binary.LittleEndian.Uint64(src[24:32]),
			MaxOutputBytes:    binary.LittleEndian.Uint64(src[32:40]),
			MaxFuel:           binary.LittleEndian.Uint64(src[40:48]),
			MaxRows:           binary.LittleEndian.Uint32(src[48:52]),
			MaxProgramColumns: binary.LittleEndian.Uint32(src[52:56]),
		},
	}
	copy(metadata.ArtifactHash[:], src[metadataHashStart:metadataHashStart+32])
	copy(metadata.ProgramHash[:], src[metadataHashStart+32:metadataHashStart+64])
	if !validMetadataManifest(metadata) {
		return Metadata{}, errInvalidMetadata
	}
	for _, value := range src[9:12] {
		if value != 0 {
			return Metadata{}, errInvalidMetadata
		}
	}
	for _, value := range src[120:MetadataBytes] {
		if value != 0 {
			return Metadata{}, errInvalidMetadata
		}
	}
	return metadata, nil
}

func validMetadataManifest(metadata Metadata) bool {
	manifest := Manifest{
		Limits: metadata.Limits, RequiredCapabilities: metadata.RequiredCapabilities,
		ABI: metadata.ABI, Schema: metadata.Schema, Profile: metadata.Profile,
	}
	return manifest.Validate() == nil
}
