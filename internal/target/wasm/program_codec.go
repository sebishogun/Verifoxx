package wasm

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"math"
	"reflect"

	"github.com/sebishogun/nornrune/internal/eval"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/result"
	"github.com/sebishogun/nornrune/internal/schema"
)

type Metadata struct {
	ArtifactHash         [sha256.Size]byte
	ProgramHash          [sha256.Size]byte
	Limits               Limits
	RequiredCapabilities Capability
	ABI                  ABIVersion
	Schema               SchemaVersion
	Profile              Profile
}

var programType = reflect.TypeFor[program.Program]()

var version1ProgramLayoutDigest = [sha256.Size]byte{
	0xec, 0x38, 0x5d, 0x61, 0xc8, 0x4a, 0xe1, 0x81,
	0x8f, 0xb0, 0xec, 0xdc, 0x9c, 0xf0, 0xae, 0xdc,
	0x0d, 0x84, 0x56, 0xa3, 0xea, 0xde, 0xcd, 0x2b,
	0xaa, 0x24, 0x88, 0x37, 0x39, 0xfd, 0xf7, 0xcb,
}

var currentProgramLayoutDigest = programLayoutDigest()

func programLayoutDigest() [sha256.Size]byte {
	return layoutDigest(programType)
}

func layoutDigest(valueType reflect.Type) [sha256.Size]byte {
	state := sha256.New()
	appendLayoutDigest(state, valueType)
	var digest [sha256.Size]byte
	state.Sum(digest[:0])
	return digest
}

func appendLayoutDigest(state hash.Hash, valueType reflect.Type) {
	var encoded [4]byte
	for row := 0; row < valueType.NumField(); row++ {
		field := valueType.Field(row)
		if field.PkgPath != "" || (valueType == programType && derivedProgramField(field.Name)) {
			continue
		}
		binary.LittleEndian.PutUint32(encoded[:], uint32(len(field.Name)))
		_, _ = state.Write(encoded[:])
		_, _ = state.Write([]byte(field.Name))
		fieldType := field.Type
		_, _ = state.Write([]byte{byte(fieldType.Kind())})
		if fieldType.Kind() == reflect.Struct {
			appendLayoutDigest(state, fieldType)
			continue
		}
		width, _ := programLeafWidth(fieldType)
		leafType := fieldType
		if leafType.Kind() == reflect.Array || leafType.Kind() == reflect.Slice {
			leafType = leafType.Elem()
		}
		_, _ = state.Write([]byte{width, byte(leafType.Kind())})
		if fieldType.Kind() == reflect.Array {
			binary.LittleEndian.PutUint32(encoded[:], uint32(fieldType.Len()))
			_, _ = state.Write(encoded[:])
		}
	}
}

func EncodeProgram(dst []byte, compiled *program.Program, manifest Manifest) ([]byte, error) {
	if currentProgramLayoutDigest != version1ProgramLayoutDigest || compiled == nil ||
		sha256.Sum256(compiled.InputBytes) != compiled.ContentHash {
		return nil, errInvalidArtifact
	}
	frozen, err := program.Freeze(compiled)
	if err != nil {
		return nil, errInvalidArtifact
	}
	sections := make([]sectionSpec, 0, 128)
	if err := appendProgramSections(reflect.ValueOf(frozen), &sections); err != nil {
		return nil, err
	}
	return buildEnvelope(dst, manifest, sections)
}

func DecodeProgram(src []byte, manifest Manifest) (*program.Program, Metadata, error) {
	if currentProgramLayoutDigest != version1ProgramLayoutDigest {
		return nil, Metadata{}, ErrIncompatibleVersion
	}
	header, descriptors, err := preflightEnvelope(src, manifest)
	if err != nil {
		return nil, Metadata{}, err
	}
	decoded := new(program.Program)
	row := 0
	if err := decodeProgramSections(reflect.ValueOf(decoded).Elem(), src, descriptors, &row); err != nil || row != len(descriptors) {
		return nil, Metadata{}, errInvalidArtifact
	}
	if sha256.Sum256(decoded.InputBytes) != decoded.ContentHash || decoded.ValidateResultTables() != nil ||
		!validDecodedSymbols(decoded) || !validDecodedProgram(decoded) {
		return nil, Metadata{}, errInvalidArtifact
	}
	metadata := Metadata{
		ProgramHash: decoded.ContentHash, Limits: header.limits, RequiredCapabilities: header.capabilities,
		ABI: header.abi, Schema: header.schema, Profile: header.profile,
	}
	copy(metadata.ArtifactHash[:], src[artifactHashOffset:artifactHashOffset+artifactHashBytes])
	return decoded, metadata, nil
}

func validDecodedSymbols(decoded *program.Program) bool {
	count := len(decoded.SymbolStarts)
	if count != len(decoded.SymbolLengths) || uint64(count) != uint64(decoded.ProgramSymbolCount) ||
		len(decoded.SymbolHashes) != len(decoded.SymbolIDs) || len(decoded.SymbolHashes) == 0 || len(decoded.SymbolHashes)&(len(decoded.SymbolHashes)-1) != 0 {
		return false
	}
	var end uint64
	for row := 0; row < count; row++ {
		start := uint64(decoded.SymbolStarts[row])
		length := uint64(decoded.SymbolLengths[row])
		if start != end || length > uint64(len(decoded.SymbolBytes))-start {
			return false
		}
		end = start + length
		symbol, ok := decoded.Symbol(schema.SymbolID(row + 1))
		if !ok {
			return false
		}
		id, found := decoded.LookupSymbol(symbol)
		if !found || uint64(id) != uint64(row+1) {
			return false
		}
	}
	return end == uint64(len(decoded.SymbolBytes))
}

func validDecodedProgram(decoded *program.Program) (valid bool) {
	defer func() {
		if recover() != nil {
			valid = false
		}
	}()
	var executor eval.Executor
	var output result.Batch
	input := eval.Batch{EvidenceOffsets: []uint32{0}}
	return executor.Execute(&output, decoded, input) == nil
}

func appendProgramSections(value reflect.Value, sections *[]sectionSpec) error {
	typeOfValue := value.Type()
	for row := 0; row < value.NumField(); row++ {
		fieldType := typeOfValue.Field(row)
		if fieldType.PkgPath != "" || (typeOfValue == programType && derivedProgramField(fieldType.Name)) {
			continue
		}
		field := value.Field(row)
		if field.Kind() == reflect.Struct {
			if err := appendProgramSections(field, sections); err != nil {
				return err
			}
			continue
		}
		if len(*sections) >= math.MaxUint16 {
			return errInvalidArtifact
		}
		section, err := encodeProgramLeaf(uint16(len(*sections)+1), field)
		if err != nil {
			return err
		}
		*sections = append(*sections, section)
	}
	return nil
}

func encodeProgramLeaf(id uint16, value reflect.Value) (sectionSpec, error) {
	return encodeProgramLeafReuse(id, value, nil)
}

func encodeProgramLeafReuse(id uint16, value reflect.Value, data []byte) (sectionSpec, error) {
	width, ok := programLeafWidth(value.Type())
	if !ok {
		return sectionSpec{}, errInvalidArtifact
	}
	count := 1
	switch value.Kind() {
	case reflect.Array, reflect.Slice:
		count = value.Len()
	}
	if uint64(count) > math.MaxUint32 {
		return sectionSpec{}, errInvalidArtifact
	}
	byteCount, ok := checkedProduct(uint64(count), uint64(width))
	if !ok || byteCount > uint64(math.MaxInt) {
		return sectionSpec{}, errInvalidArtifact
	}
	if cap(data) < int(byteCount) {
		data = make([]byte, int(byteCount))
	} else {
		data = data[:int(byteCount)]
		clear(data)
	}
	if value.Kind() == reflect.Array || value.Kind() == reflect.Slice {
		for row := 0; row < count; row++ {
			if !putProgramScalar(data[row*int(width):], value.Index(row), width) {
				return sectionSpec{}, errInvalidArtifact
			}
		}
	} else if !putProgramScalar(data, value, width) {
		return sectionSpec{}, errInvalidArtifact
	}
	return sectionSpec{id: id, width: width, count: uint32(count), data: data}, nil
}

func decodeProgramSections(value reflect.Value, src []byte, descriptors []sectionDescriptor, row *int) error {
	typeOfValue := value.Type()
	for fieldRow := 0; fieldRow < value.NumField(); fieldRow++ {
		fieldType := typeOfValue.Field(fieldRow)
		if fieldType.PkgPath != "" || (typeOfValue == programType && derivedProgramField(fieldType.Name)) {
			continue
		}
		field := value.Field(fieldRow)
		if field.Kind() == reflect.Struct {
			if err := decodeProgramSections(field, src, descriptors, row); err != nil {
				return err
			}
			continue
		}
		if *row >= len(descriptors) {
			return errInvalidArtifact
		}
		descriptor := descriptors[*row]
		*row++
		width, ok := programLeafWidth(field.Type())
		if !ok || descriptor.id != uint16(*row) || descriptor.width != width {
			return errInvalidArtifact
		}
		data := src[int(descriptor.offset):int(descriptor.offset+descriptor.bytes)]
		switch field.Kind() {
		case reflect.Slice:
			if descriptor.count == 0 {
				field.SetLen(0)
				continue
			}
			count := int(descriptor.count)
			if field.Cap() < count {
				field.Set(reflect.MakeSlice(field.Type(), count, count))
			} else {
				field.SetLen(count)
			}
		case reflect.Array:
			if uint64(descriptor.count) != uint64(field.Len()) {
				return errInvalidArtifact
			}
		default:
			if descriptor.count != 1 {
				return errInvalidArtifact
			}
		}
		if field.Kind() == reflect.Array || field.Kind() == reflect.Slice {
			for element := 0; element < field.Len(); element++ {
				if !readProgramScalar(field.Index(element), data[element*int(width):], width) {
					return errInvalidArtifact
				}
			}
		} else if !readProgramScalar(field, data, width) {
			return errInvalidArtifact
		}
	}
	return nil
}

func derivedProgramField(name string) bool {
	return name == "Templates" || name == "Explanations"
}

func programLeafWidth(valueType reflect.Type) (uint8, bool) {
	if valueType.Kind() == reflect.Array || valueType.Kind() == reflect.Slice {
		valueType = valueType.Elem()
	}
	switch valueType.Kind() {
	case reflect.Bool, reflect.Uint8, reflect.Int8:
		return 1, true
	case reflect.Uint16, reflect.Int16:
		return 2, true
	case reflect.Uint32, reflect.Int32:
		return 4, true
	case reflect.Uint64, reflect.Int64:
		return 8, true
	default:
		return 0, false
	}
}

func putProgramScalar(dst []byte, value reflect.Value, width uint8) bool {
	switch value.Kind() {
	case reflect.Bool:
		if value.Bool() {
			dst[0] = 1
		}
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		putProgramUint(dst, value.Uint(), width)
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		putProgramUint(dst, uint64(value.Int()), width)
	default:
		return false
	}
	return true
}

func readProgramScalar(dst reflect.Value, src []byte, width uint8) bool {
	value := readProgramUint(src, width)
	switch dst.Kind() {
	case reflect.Bool:
		if value > 1 {
			return false
		}
		dst.SetBool(value != 0)
	case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		dst.SetUint(value)
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		dst.SetInt(int64(value))
	default:
		return false
	}
	return true
}

func putProgramUint(dst []byte, value uint64, width uint8) {
	switch width {
	case 1:
		dst[0] = byte(value)
	case 2:
		binary.LittleEndian.PutUint16(dst, uint16(value))
	case 4:
		binary.LittleEndian.PutUint32(dst, uint32(value))
	case 8:
		binary.LittleEndian.PutUint64(dst, value)
	}
}

func readProgramUint(src []byte, width uint8) uint64 {
	switch width {
	case 1:
		return uint64(src[0])
	case 2:
		return uint64(binary.LittleEndian.Uint16(src))
	case 4:
		return uint64(binary.LittleEndian.Uint32(src))
	case 8:
		return binary.LittleEndian.Uint64(src)
	default:
		return 0
	}
}
