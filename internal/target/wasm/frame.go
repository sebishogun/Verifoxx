package wasm

import (
	"crypto/sha256"
	"errors"
	"math"
	"reflect"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/result"
)

type frameKind uint8

const (
	frameInput frameKind = iota + 1
	frameResult
)

var errInvalidFrame = errors.New("wasm: invalid frame")

var (
	version1InputFrameLayoutDigest = [sha256.Size]byte{
		0x9e, 0x9b, 0x53, 0x4b, 0xea, 0xb6, 0x68, 0x3b,
		0xfb, 0x5b, 0x71, 0x8f, 0xaf, 0x13, 0x1a, 0x63,
		0xfa, 0x97, 0x16, 0xbf, 0x20, 0x54, 0xa4, 0x53,
		0x64, 0x8d, 0xe0, 0x61, 0x93, 0x46, 0xb6, 0xdc,
	}
	version1ResultFrameLayoutDigest = [sha256.Size]byte{
		0x5f, 0x86, 0x59, 0xce, 0x90, 0xff, 0x5e, 0x07,
		0x37, 0xa3, 0x2b, 0xba, 0xf3, 0x2e, 0x30, 0x13,
		0x42, 0x13, 0x30, 0x5b, 0xef, 0x48, 0x1c, 0x4e,
		0x1a, 0xf8, 0xbe, 0xc4, 0xbf, 0xf7, 0x1d, 0x2a,
	}
	currentInputFrameLayoutDigest  = frameLayoutDigest(reflect.TypeFor[eval.Batch]())
	currentResultFrameLayoutDigest = frameLayoutDigest(reflect.TypeFor[result.Batch]())
)

func frameLayoutDigest(valueType reflect.Type) [sha256.Size]byte {
	return layoutDigest(valueType)
}

func frameLayoutSupported(kind frameKind) bool {
	switch kind {
	case frameInput:
		return currentInputFrameLayoutDigest == version1InputFrameLayoutDigest
	case frameResult:
		return currentResultFrameLayoutDigest == version1ResultFrameLayoutDigest
	default:
		return false
	}
}

type frameEncoder struct {
	sections []sectionSpec
}

func EncodeInputFrame(dst []byte, input eval.Batch, limits Limits) ([]byte, error) {
	if uint64(input.Rows) > uint64(limits.MaxRows) || !validInputBatch(input) {
		return nil, errInvalidFrame
	}
	return encodeFrame(dst, frameInput, reflect.ValueOf(input), limits.MaxInputBytes, limits.MaxProgramColumns)
}

func DecodeInputFrame(dst *eval.Batch, src []byte, limits Limits) error {
	if dst == nil {
		return errInvalidFrame
	}
	if err := decodeFrame(reflect.ValueOf(dst).Elem(), src, frameInput, limits.MaxInputBytes, limits.MaxProgramColumns); err != nil ||
		uint64(dst.Rows) > uint64(limits.MaxRows) || !validInputBatch(*dst) {
		return errInvalidFrame
	}
	return nil
}

func EncodeResultFrame(dst []byte, output result.Batch, limits Limits) ([]byte, error) {
	if uint64(output.Rows) > uint64(limits.MaxRows) || !validResultBatch(output) {
		return nil, errInvalidFrame
	}
	return encodeFrame(dst, frameResult, reflect.ValueOf(output), limits.MaxOutputBytes, limits.MaxProgramColumns)
}

func DecodeResultFrame(dst *result.Batch, src []byte, limits Limits) error {
	if dst == nil {
		return errInvalidFrame
	}
	if err := decodeFrame(reflect.ValueOf(dst).Elem(), src, frameResult, limits.MaxOutputBytes, limits.MaxProgramColumns); err != nil ||
		uint64(dst.Rows) > uint64(limits.MaxRows) || !validResultBatch(*dst) {
		return errInvalidFrame
	}
	return nil
}

func encodeFrame(dst []byte, kind frameKind, value reflect.Value, maxBytes uint64, maxSections uint32) ([]byte, error) {
	var encoder frameEncoder
	return encoder.encode(dst, kind, value, maxBytes, maxSections)
}

func (encoder *frameEncoder) encode(dst []byte, kind frameKind, value reflect.Value, maxBytes uint64, maxSections uint32) ([]byte, error) {
	if !frameLayoutSupported(kind) {
		return nil, errInvalidFrame
	}
	count := programLeafCount(value)
	if count > math.MaxUint16 || uint64(count) > uint64(maxSections) {
		return nil, errInvalidFrame
	}
	if cap(encoder.sections) < count {
		encoder.sections = make([]sectionSpec, count)
	} else {
		encoder.sections = encoder.sections[:count]
	}
	row := 0
	if err := fillProgramSections(value, encoder.sections, &row); err != nil || row != count {
		return nil, errInvalidFrame
	}
	encoded, err := buildSections(
		dst, FrameMagic, byte(kind), 0, frameHeaderBytes, maxBytes, maxSections, Limits{}, encoder.sections,
	)
	if err != nil {
		return nil, errInvalidFrame
	}
	return encoded, nil
}

func programLeafCount(value reflect.Value) int {
	count := 0
	typeOfValue := value.Type()
	for row := 0; row < value.NumField(); row++ {
		fieldType := typeOfValue.Field(row)
		if fieldType.PkgPath != "" || (typeOfValue == programType && derivedProgramField(fieldType.Name)) {
			continue
		}
		field := value.Field(row)
		if field.Kind() == reflect.Struct {
			count += programLeafCount(field)
		} else {
			count++
		}
	}
	return count
}

func fillProgramSections(value reflect.Value, sections []sectionSpec, row *int) error {
	typeOfValue := value.Type()
	for fieldRow := 0; fieldRow < value.NumField(); fieldRow++ {
		fieldType := typeOfValue.Field(fieldRow)
		if fieldType.PkgPath != "" || (typeOfValue == programType && derivedProgramField(fieldType.Name)) {
			continue
		}
		field := value.Field(fieldRow)
		if field.Kind() == reflect.Struct {
			if err := fillProgramSections(field, sections, row); err != nil {
				return err
			}
			continue
		}
		section, err := encodeProgramLeafReuse(uint16(*row+1), field, sections[*row].data)
		if err != nil {
			return err
		}
		sections[*row] = section
		*row++
	}
	return nil
}

func decodeFrame(dst reflect.Value, src []byte, want frameKind, maxBytes uint64, maxSections uint32) error {
	if !frameLayoutSupported(want) {
		return errInvalidFrame
	}
	header, descriptors, err := preflightSections(src, FrameMagic, frameHeaderBytes, maxBytes, maxSections)
	if err != nil || frameKind(header.profile) != want || header.capabilities != 0 {
		return errInvalidFrame
	}
	row := 0
	if err := decodeProgramSections(dst, src, descriptors, &row); err != nil || row != len(descriptors) {
		return errInvalidFrame
	}
	return nil
}

func validInputBatch(input eval.Batch) bool {
	if uint64(len(input.RequestIDs)) != uint64(input.Rows) ||
		!validOffsets(input.EvidenceOffsets, input.Rows, len(input.EvidenceRefs)) {
		return false
	}
	for _, id := range input.RequestIDs {
		if id == 0 {
			return false
		}
	}
	evidenceRows := len(input.Evidence.IDs)
	if len(input.Evidence.Kinds) != evidenceRows || len(input.Evidence.States) != evidenceRows ||
		len(input.Evidence.Subjects) != evidenceRows || len(input.Evidence.Scopes) != evidenceRows ||
		len(input.Evidence.Reviewers) != evidenceRows || len(input.Evidence.Timings) != evidenceRows ||
		len(input.Evidence.Timestamps) != evidenceRows {
		return false
	}
	for row := range evidenceRows {
		if input.Evidence.IDs[row] == 0 || input.Evidence.Kinds[row] == 0 || input.Evidence.States[row] == 0 {
			return false
		}
	}
	for _, ref := range input.EvidenceRefs {
		if uint64(ref) >= uint64(evidenceRows) {
			return false
		}
	}
	return true
}

func validResultBatch(output result.Batch) bool {
	rows := output.Rows
	if uint64(len(output.OutcomeIDs)) != uint64(rows) ||
		!validOffsets(output.RequirementOffsets, rows, len(output.RequirementIDs)) ||
		!validOffsets(output.DriverOffsets, rows, len(output.DriverRequirements)) ||
		!validOffsets(output.EvidenceOffsets, rows, len(output.EvidenceIDs)) ||
		!validOffsets(output.ReasonOffsets, rows, len(output.ReasonIDs)) ||
		!validOffsets(output.RemediationOffsets, rows, len(output.RemediationIDs)) {
		return false
	}
	drivers := len(output.DriverRequirements)
	if len(output.DriverClauses) != drivers || len(output.DriverNodes) != drivers ||
		len(output.DriverReasons) != drivers || len(output.DriverExplanations) != drivers {
		return false
	}
	reasons := len(output.ReasonIDs)
	return len(output.ReasonNodes) == reasons && len(output.ReasonEvidenceIDs) == reasons &&
		len(output.ReasonEvidenceStates) == reasons
}

func validOffsets(offsets []uint32, rows uint32, edgeCount int) bool {
	if uint64(len(offsets)) != uint64(rows)+1 || len(offsets) == 0 || offsets[0] != 0 ||
		uint64(offsets[len(offsets)-1]) != uint64(edgeCount) {
		return false
	}
	previous := uint32(0)
	for _, offset := range offsets[1:] {
		if offset < previous || uint64(offset) > uint64(edgeCount) {
			return false
		}
		previous = offset
	}
	return true
}
