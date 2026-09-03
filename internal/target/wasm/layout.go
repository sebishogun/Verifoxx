package wasm

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"math"
)

const (
	frameHeaderBytes        = 64
	artifactHeaderBytes     = 104
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
	limits       Limits
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
		artifactHeaderBytes, manifest.Limits.MaxArtifactBytes, manifest.Limits.MaxProgramColumns,
		manifest.Limits, sections,
	)
}

func buildSections(
	dst []byte,
	magic uint32,
	tag byte,
	flags uint32,
	headerBytes int,
	maxBytes uint64,
	maxSections uint32,
	limits Limits,
	sections []sectionSpec,
) ([]byte, error) {
	if maxBytes == 0 || uint64(len(sections)) > uint64(maxSections) ||
		(headerBytes != frameHeaderBytes && headerBytes != artifactHeaderBytes) ||
		(headerBytes == artifactHeaderBytes && limits.Validate() != nil) ||
		(headerBytes == frameHeaderBytes && limits != (Limits{})) {
		return nil, errInvalidArtifact
	}
	total := uint64(headerBytes) + uint64(len(sections))*artifactDescriptorBytes
	if total < uint64(headerBytes) {
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
	if total > maxBytes || total > uint64(math.MaxInt-len(dst)) {
		return nil, errInvalidArtifact
	}
	start := len(dst)
	end := start + int(total)
	if cap(dst) < end {
		grown := make([]byte, end)
		copy(grown, dst)
		dst = grown
	} else {
		dst = dst[:end]
	}
	artifact := dst[start:end]
	clear(artifact)

	binary.LittleEndian.PutUint32(artifact[0:4], magic)
	binary.LittleEndian.PutUint16(artifact[4:6], uint16(CurrentABIVersion))
	binary.LittleEndian.PutUint16(artifact[6:8], uint16(CurrentSchemaVersion))
	artifact[8] = tag
	binary.LittleEndian.PutUint32(artifact[12:16], flags)
	binary.LittleEndian.PutUint32(artifact[16:20], uint32(len(sections)))
	binary.LittleEndian.PutUint32(artifact[20:24], artifactDescriptorBytes)
	binary.LittleEndian.PutUint64(artifact[24:32], total)
	if headerBytes == artifactHeaderBytes {
		binary.LittleEndian.PutUint64(artifact[64:72], limits.MaxArtifactBytes)
		binary.LittleEndian.PutUint64(artifact[72:80], limits.MaxInputBytes)
		binary.LittleEndian.PutUint64(artifact[80:88], limits.MaxOutputBytes)
		binary.LittleEndian.PutUint64(artifact[88:96], limits.MaxFuel)
		binary.LittleEndian.PutUint32(artifact[96:100], limits.MaxRows)
		binary.LittleEndian.PutUint32(artifact[100:104], limits.MaxProgramColumns)
	}
	offset := uint64(headerBytes) + uint64(len(sections))*artifactDescriptorBytes
	for row := range sections {
		section := sections[row]
		offset, _ = alignOffset(offset, uint64(section.width))
		sectionBytes := uint64(len(section.data))
		start := headerBytes + row*artifactDescriptorBytes
		binary.LittleEndian.PutUint16(artifact[start:start+2], section.id)
		artifact[start+2] = section.width
		artifact[start+3] = section.width
		binary.LittleEndian.PutUint32(artifact[start+4:start+8], section.count)
		binary.LittleEndian.PutUint64(artifact[start+8:start+16], offset)
		binary.LittleEndian.PutUint64(artifact[start+16:start+24], sectionBytes)
		copy(artifact[int(offset):int(offset+sectionBytes)], section.data)
		offset += sectionBytes
	}
	sum := sha256.Sum256(artifact)
	copy(artifact[artifactHashOffset:artifactHashOffset+artifactHashBytes], sum[:])
	return dst, nil
}

func preflightEnvelope(src []byte, host Manifest) (artifactHeader, []sectionDescriptor, error) {
	if err := host.Validate(); err != nil {
		return artifactHeader{}, nil, err
	}
	header, descriptorEnd, err := preflightHeader(src, ArtifactMagic, artifactHeaderBytes, host.Limits.MaxArtifactBytes)
	if err != nil {
		return artifactHeader{}, nil, errInvalidArtifact
	}
	if header.abi != CurrentABIVersion || header.schema != CurrentSchemaVersion {
		return artifactHeader{}, nil, ErrIncompatibleVersion
	}
	if !header.profile.Valid() || header.capabilities&^CapabilityAll != 0 ||
		header.limits.Validate() != nil || header.totalBytes > header.limits.MaxArtifactBytes ||
		header.sectionCount > header.limits.MaxProgramColumns {
		return artifactHeader{}, nil, errInvalidArtifact
	}
	artifactManifest := Manifest{
		ABI: header.abi, Schema: header.schema, Profile: header.profile,
		RequiredCapabilities: header.capabilities, Limits: header.limits,
	}
	if artifactManifest.Validate() != nil {
		return artifactHeader{}, nil, errInvalidArtifact
	}
	if artifactManifest != host {
		return artifactHeader{}, nil, ErrIncompatibleVersion
	}
	descriptors, err := preflightDescriptors(src, header, artifactHeaderBytes, descriptorEnd)
	if err != nil {
		return artifactHeader{}, nil, errInvalidArtifact
	}
	return header, descriptors, nil
}

func preflightSections(
	src []byte,
	magic uint32,
	headerBytes int,
	maxBytes uint64,
	maxSections uint32,
) (artifactHeader, []sectionDescriptor, error) {
	header, descriptorEnd, err := preflightHeader(src, magic, headerBytes, maxBytes)
	if err != nil || header.abi != CurrentABIVersion || header.schema != CurrentSchemaVersion || header.sectionCount > maxSections {
		return artifactHeader{}, nil, errInvalidArtifact
	}
	descriptors, err := preflightDescriptors(src, header, headerBytes, descriptorEnd)
	if err != nil {
		return artifactHeader{}, nil, errInvalidArtifact
	}
	return header, descriptors, nil
}

func preflightHeader(src []byte, magic uint32, headerBytes int, maxBytes uint64) (artifactHeader, uint64, error) {
	if (headerBytes != frameHeaderBytes && headerBytes != artifactHeaderBytes) ||
		len(src) < headerBytes || maxBytes == 0 || uint64(len(src)) > maxBytes ||
		binary.LittleEndian.Uint32(src[0:4]) != magic {
		return artifactHeader{}, 0, errInvalidArtifact
	}
	header := artifactHeader{
		abi:          ABIVersion(binary.LittleEndian.Uint16(src[4:6])),
		schema:       SchemaVersion(binary.LittleEndian.Uint16(src[6:8])),
		profile:      Profile(src[8]),
		capabilities: Capability(binary.LittleEndian.Uint32(src[12:16])),
		sectionCount: binary.LittleEndian.Uint32(src[16:20]),
		totalBytes:   binary.LittleEndian.Uint64(src[24:32]),
	}
	if headerBytes == artifactHeaderBytes {
		header.limits = Limits{
			MaxArtifactBytes:  binary.LittleEndian.Uint64(src[64:72]),
			MaxInputBytes:     binary.LittleEndian.Uint64(src[72:80]),
			MaxOutputBytes:    binary.LittleEndian.Uint64(src[80:88]),
			MaxFuel:           binary.LittleEndian.Uint64(src[88:96]),
			MaxRows:           binary.LittleEndian.Uint32(src[96:100]),
			MaxProgramColumns: binary.LittleEndian.Uint32(src[100:104]),
		}
	}
	if header.totalBytes != uint64(len(src)) ||
		binary.LittleEndian.Uint32(src[20:24]) != artifactDescriptorBytes ||
		!allZero(src[9:12]) || !validArtifactHash(src) {
		return artifactHeader{}, 0, errInvalidArtifact
	}
	descriptorEnd := uint64(headerBytes) + uint64(header.sectionCount)*artifactDescriptorBytes
	if descriptorEnd < uint64(headerBytes) || descriptorEnd > uint64(len(src)) {
		return artifactHeader{}, 0, errInvalidArtifact
	}
	return header, descriptorEnd, nil
}

func preflightDescriptors(
	src []byte,
	header artifactHeader,
	headerBytes int,
	descriptorEnd uint64,
) ([]sectionDescriptor, error) {
	descriptors := make([]sectionDescriptor, int(header.sectionCount))
	previousID := uint16(0)
	previousEnd := descriptorEnd
	for row := range descriptors {
		start := headerBytes + row*artifactDescriptorBytes
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
			return nil, errInvalidArtifact
		}
		descriptors[row] = descriptor
		previousID = descriptor.id
		previousEnd = descriptor.offset + descriptor.bytes
	}
	if previousEnd != uint64(len(src)) {
		return nil, errInvalidArtifact
	}
	return descriptors, nil
}

func allZero(values []byte) bool {
	for _, value := range values {
		if value != 0 {
			return false
		}
	}
	return true
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
