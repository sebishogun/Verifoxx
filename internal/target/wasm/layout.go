package wasm

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"math"
)

const (
	artifactHeaderBytes     = 64
	artifactDescriptorBytes = 24
	artifactHashOffset      = 32
	artifactHashBytes       = sha256.Size
)

var errInvalidArtifact = errors.New("wasm: invalid artifact")

type sectionSpec struct {
	data  []byte
	count uint32
	id    uint16
	width uint8
}

type artifactHeader struct {
	totalBytes   uint64
	capabilities Capability
	sectionCount uint32
	abi          ABIVersion
	schema       SchemaVersion
	profile      Profile
}

type sectionDescriptor struct {
	offset    uint64
	bytes     uint64
	count     uint32
	id        uint16
	width     uint8
	alignment uint8
}

func buildEnvelope(dst []byte, manifest Manifest, sections []sectionSpec) ([]byte, error) {
	if manifest.Validate() != nil || uint64(len(sections)) > uint64(manifest.Limits.MaxProgramColumns) {
		return nil, errInvalidArtifact
	}
	return buildSections(
		dst, ArtifactMagic, byte(manifest.Profile), uint32(manifest.RequiredCapabilities),
		manifest.Limits.MaxArtifactBytes, manifest.Limits.MaxProgramColumns, sections,
	)
}

func buildSections(dst []byte, magic uint32, tag byte, flags uint32, maxBytes uint64, maxSections uint32, sections []sectionSpec) ([]byte, error) {
	if maxBytes == 0 || uint64(len(sections)) > uint64(maxSections) {
		return nil, errInvalidArtifact
	}
	total := uint64(artifactHeaderBytes) + uint64(len(sections))*artifactDescriptorBytes
	if total < artifactHeaderBytes {
		return nil, errInvalidArtifact
	}
	previousID := uint16(0)
	for row := range sections {
		section := sections[row]
		if section.id == 0 || section.id <= previousID || !validWidth(section.width) {
			return nil, errInvalidArtifact
		}
		bytes, ok := checkedProduct(uint64(section.count), uint64(section.width))
		if !ok || bytes != uint64(len(section.data)) {
			return nil, errInvalidArtifact
		}
		total, ok = alignOffset(total, uint64(section.width))
		if !ok || bytes > math.MaxUint64-total {
			return nil, errInvalidArtifact
		}
		total += bytes
		previousID = section.id
	}
	if total > maxBytes || total > uint64(math.MaxInt) {
		return nil, errInvalidArtifact
	}
	if cap(dst) < int(total) {
		dst = make([]byte, int(total))
	} else {
		dst = dst[:int(total)]
		clear(dst)
	}

	binary.LittleEndian.PutUint32(dst[0:4], magic)
	binary.LittleEndian.PutUint16(dst[4:6], uint16(CurrentABIVersion))
	binary.LittleEndian.PutUint16(dst[6:8], uint16(CurrentSchemaVersion))
	dst[8] = tag
	binary.LittleEndian.PutUint32(dst[12:16], flags)
	binary.LittleEndian.PutUint32(dst[16:20], uint32(len(sections)))
	binary.LittleEndian.PutUint32(dst[20:24], artifactDescriptorBytes)
	binary.LittleEndian.PutUint64(dst[24:32], total)
	offset := uint64(artifactHeaderBytes) + uint64(len(sections))*artifactDescriptorBytes
	for row := range sections {
		section := sections[row]
		offset, _ = alignOffset(offset, uint64(section.width))
		sectionBytes := uint64(len(section.data))
		start := artifactHeaderBytes + row*artifactDescriptorBytes
		binary.LittleEndian.PutUint16(dst[start:start+2], section.id)
		dst[start+2] = section.width
		dst[start+3] = section.width
		binary.LittleEndian.PutUint32(dst[start+4:start+8], section.count)
		binary.LittleEndian.PutUint64(dst[start+8:start+16], offset)
		binary.LittleEndian.PutUint64(dst[start+16:start+24], sectionBytes)
		copy(dst[int(offset):int(offset+sectionBytes)], section.data)
		offset += sectionBytes
	}
	sum := sha256.Sum256(dst)
	copy(dst[artifactHashOffset:artifactHashOffset+artifactHashBytes], sum[:])
	return dst, nil
}

func preflightEnvelope(src []byte, limits Limits) (artifactHeader, []sectionDescriptor, error) {
	header, descriptors, err := preflightSections(src, ArtifactMagic, limits.MaxArtifactBytes, limits.MaxProgramColumns)
	if err != nil || !header.profile.Valid() || header.capabilities&^CapabilityAll != 0 {
		return artifactHeader{}, nil, errInvalidArtifact
	}
	return header, descriptors, nil
}

func preflightSections(src []byte, magic uint32, maxBytes uint64, maxSections uint32) (artifactHeader, []sectionDescriptor, error) {
	if len(src) < artifactHeaderBytes || maxBytes == 0 || uint64(len(src)) > maxBytes ||
		binary.LittleEndian.Uint32(src[0:4]) != magic {
		return artifactHeader{}, nil, errInvalidArtifact
	}
	header := artifactHeader{
		abi:          ABIVersion(binary.LittleEndian.Uint16(src[4:6])),
		schema:       SchemaVersion(binary.LittleEndian.Uint16(src[6:8])),
		profile:      Profile(src[8]),
		capabilities: Capability(binary.LittleEndian.Uint32(src[12:16])),
		sectionCount: binary.LittleEndian.Uint32(src[16:20]),
		totalBytes:   binary.LittleEndian.Uint64(src[24:32]),
	}
	if header.abi != CurrentABIVersion || header.schema != CurrentSchemaVersion ||
		header.totalBytes != uint64(len(src)) || header.sectionCount > maxSections ||
		binary.LittleEndian.Uint32(src[20:24]) != artifactDescriptorBytes ||
		!validArtifactHash(src) {
		return artifactHeader{}, nil, errInvalidArtifact
	}
	descriptorEnd := uint64(artifactHeaderBytes) + uint64(header.sectionCount)*artifactDescriptorBytes
	if descriptorEnd < artifactHeaderBytes || descriptorEnd > uint64(len(src)) {
		return artifactHeader{}, nil, errInvalidArtifact
	}
	descriptors := make([]sectionDescriptor, int(header.sectionCount))
	previousID := uint16(0)
	previousEnd := descriptorEnd
	for row := range descriptors {
		start := artifactHeaderBytes + row*artifactDescriptorBytes
		descriptor := sectionDescriptor{
			id:        binary.LittleEndian.Uint16(src[start : start+2]),
			width:     src[start+2],
			alignment: src[start+3],
			count:     binary.LittleEndian.Uint32(src[start+4 : start+8]),
			offset:    binary.LittleEndian.Uint64(src[start+8 : start+16]),
			bytes:     binary.LittleEndian.Uint64(src[start+16 : start+24]),
		}
		bytes, ok := checkedProduct(uint64(descriptor.count), uint64(descriptor.width))
		if descriptor.id == 0 || descriptor.id <= previousID || !validWidth(descriptor.width) ||
			descriptor.alignment != descriptor.width || !ok || descriptor.bytes != bytes ||
			descriptor.offset%uint64(descriptor.alignment) != 0 || descriptor.offset < previousEnd ||
			descriptor.offset > uint64(len(src)) ||
			descriptor.bytes > uint64(len(src))-descriptor.offset {
			return artifactHeader{}, nil, errInvalidArtifact
		}
		descriptors[row] = descriptor
		previousID = descriptor.id
		previousEnd = descriptor.offset + descriptor.bytes
	}
	if previousEnd != uint64(len(src)) {
		return artifactHeader{}, nil, errInvalidArtifact
	}
	return header, descriptors, nil
}

func validWidth(width uint8) bool { return width == 1 || width == 2 || width == 4 || width == 8 }

func checkedProduct(left, right uint64) (uint64, bool) {
	if left != 0 && right > math.MaxUint64/left {
		return 0, false
	}
	return left * right, true
}

func alignOffset(offset, alignment uint64) (uint64, bool) {
	padding := -offset & (alignment - 1)
	if padding > math.MaxUint64-offset {
		return 0, false
	}
	return offset + padding, true
}

func validArtifactHash(src []byte) bool {
	want := [sha256.Size]byte(src[artifactHashOffset : artifactHashOffset+artifactHashBytes])
	state := sha256.New()
	writeHashParts(state, src)
	got := state.Sum(nil)
	return bytesEqual(got, want[:])
}

func writeHashParts(state hash.Hash, src []byte) {
	_, _ = state.Write(src[:artifactHashOffset])
	var zero [artifactHashBytes]byte
	_, _ = state.Write(zero[:])
	_, _ = state.Write(src[artifactHashOffset+artifactHashBytes:])
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for row := range left {
		difference |= left[row] ^ right[row]
	}
	return difference == 0
}
