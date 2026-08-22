// Package jsonbatch decodes request and evidence JSON directly into typed SoA
// evaluation batches without maps, reflection, or success-path strings.
package jsonbatch

import (
	"bytes"
	"math"
	"unicode/utf8"

	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

var (
	literalTrue  = []byte("true")
	literalFalse = []byte("false")
	literalNull  = []byte("null")
	keyVersion   = []byte("schema_version")
	keyPack      = []byte("pack")
	keyRequests  = []byte("requests")
	keyEvidence  = []byte("evidence")
	keyRefs      = []byte("evidence_refs")
)

// Decoder is a reusable request/evidence decode worker.
type Decoder struct {
	program        *program.Program
	fieldTable     symbolTable
	kindTable      symbolTable
	stateTable     symbolTable
	evidenceIDKeys []schema.EvidenceID
	evidenceIDRows []uint32
	requestIDKeys  []schema.RequestID
	requestOffsets []uint32
	requestRefs    []uint32
	seenFields     []uint64
	seenRefs       []uint64
	pathScratch    []byte
	scan           scanner
}

type symbolTable struct {
	keys []schema.SymbolID
	rows []uint32
}

type shape struct {
	requests uint32
	evidence uint32
	refs     uint32
}

func resizeZero[T any](dst []T, n int) []T {
	if cap(dst) < n {
		return make([]T, n)
	}
	dst = dst[:n]
	clear(dst)
	return dst
}

func symbolSlot(id schema.SymbolID, mask uint64) int {
	return int((uint64(id) * 11400714819323198485) & mask)
}

func (t *symbolTable) build(p *program.Program, names []schema.SymbolID) bool {
	size := 4
	for size < 2*len(names) {
		if size > math.MaxInt/2 {
			return false
		}
		size <<= 1
	}
	t.keys = resizeZero(t.keys, size)
	t.rows = resizeZero(t.rows, size)
	mask := uint64(size - 1)
	for row, name := range names {
		value, ok := p.Symbol(name)
		if !ok {
			return false
		}
		found, ok := p.LookupSymbol(value)
		if !ok || found != name {
			return false
		}
		slot := symbolSlot(name, mask)
		for probes := 0; probes < size; probes++ {
			if t.keys[slot] == 0 {
				t.keys[slot] = name
				t.rows[slot] = uint32(row + 1)
				break
			}
			if t.keys[slot] == name {
				return false
			}
			slot = (slot + 1) & int(mask)
			if probes == size-1 {
				return false
			}
		}
	}
	return true
}

func (t *symbolTable) lookup(id schema.SymbolID) (uint32, bool) {
	if id == 0 || len(t.keys) == 0 || len(t.keys) != len(t.rows) {
		return 0, false
	}
	mask := uint64(len(t.keys) - 1)
	slot := symbolSlot(id, mask)
	for probes := 0; probes < len(t.keys); probes++ {
		key := t.keys[slot]
		if key == 0 {
			return 0, false
		}
		if key == id {
			return t.rows[slot], true
		}
		slot = (slot + 1) & int(mask)
	}
	return 0, false
}

func (d *Decoder) bind(p *program.Program) error {
	if p == nil {
		return ErrInvalidProgram
	}
	if d.program == p {
		return nil
	}
	if p.PolicyName == 0 {
		return ErrInvalidProgram
	}
	if _, ok := p.Symbol(p.PolicyName); !ok {
		return ErrInvalidProgram
	}
	n := len(p.FieldNames)
	if n != len(p.FieldKinds) || n != len(p.FieldGroups) || n != len(p.FieldIndex.Kinds) || n != len(p.FieldIndex.Columns) {
		return ErrInvalidProgram
	}
	for row, kind := range p.FieldKinds {
		if !kind.Valid() || !p.FieldGroups[row].Valid() || p.FieldIndex.Kinds[row] != kind {
			return ErrInvalidProgram
		}
	}
	if !d.fieldTable.build(p, p.FieldNames) ||
		!d.kindTable.build(p, p.EvidenceKindNames) ||
		!d.stateTable.build(p, p.EvidenceStateNames) {
		return ErrInvalidProgram
	}
	d.program = p
	return nil
}

func (d *Decoder) lookup(table *symbolTable, value []byte) (uint32, bool) {
	if d.program == nil {
		return 0, false
	}
	id, ok := d.program.LookupSymbol(value)
	if !ok {
		return 0, false
	}
	return table.lookup(id)
}

func (d *Decoder) lookupField(value []byte) (schema.FieldID, bool) {
	row, ok := d.lookup(&d.fieldTable, value)
	return schema.FieldID(row), ok
}

func (d *Decoder) lookupEvidenceKind(value []byte) (schema.EvidenceKindID, bool) {
	row, ok := d.lookup(&d.kindTable, value)
	return schema.EvidenceKindID(row), ok
}

func (d *Decoder) lookupEvidenceState(value []byte) (schema.EvidenceStateID, bool) {
	row, ok := d.lookup(&d.stateTable, value)
	return schema.EvidenceStateID(row), ok
}

// Decode materializes evidence and requests into dst. The returned Batch is a
// borrowed view valid until dst's next successful Begin call.
func (d *Decoder) Decode(dst *eval.Builder, p *program.Program, requests, evidence []byte, limits Limits) (eval.Batch, error) {
	if d == nil {
		return eval.Batch{}, ErrInvalidProgram
	}
	defer func() { d.scan.src = nil }()
	if err := d.bind(p); err != nil {
		return eval.Batch{}, err
	}
	evidenceShape, err := d.count(InputEvidence, evidence, limits)
	if err != nil {
		return eval.Batch{}, err
	}
	requestShape, err := d.count(InputRequests, requests, limits)
	if err != nil {
		return eval.Batch{}, err
	}
	if err := dst.Begin(p, requestShape.requests, evidenceShape.evidence, requestShape.refs); err != nil {
		return eval.Batch{}, err
	}
	if err := d.decodeEvidence(dst, evidence, limits, evidenceShape.evidence); err != nil {
		dst.Abort()
		return eval.Batch{}, err
	}
	if err := d.decodeRequests(dst, requests, limits, requestShape.requests, requestShape.refs); err != nil {
		dst.Abort()
		return eval.Batch{}, err
	}
	batch, err := dst.Finish()
	if err != nil {
		dst.Abort()
		return eval.Batch{}, err
	}
	return batch, nil
}

// Decode is equivalent to one call on a fresh Decoder.
func Decode(dst *eval.Builder, p *program.Program, requests, evidence []byte, limits Limits) (eval.Batch, error) {
	var decoder Decoder
	return decoder.Decode(dst, p, requests, evidence, limits)
}

type scanner struct {
	src          []byte
	keyScratch   []byte
	valueScratch []byte
	limits       Limits
	pos          int
	input        Input
}

func (s *scanner) reset(input Input, src []byte, limits Limits) {
	s.src = src
	s.pos = 0
	s.input = input
	s.limits = limits
	s.keyScratch = s.keyScratch[:0]
	s.valueScratch = s.valueScratch[:0]
}

func (s *scanner) fail(code ErrorCode, message string) error {
	return &Error{Input: s.input, Code: code, Offset: s.pos, Message: message}
}

func (s *scanner) failAt(code ErrorCode, offset int, message string) error {
	return &Error{Input: s.input, Code: code, Offset: offset, Message: message}
}

func (s *scanner) eof() bool { return s.pos >= len(s.src) }

func (s *scanner) peek() byte {
	if s.eof() {
		return 0
	}
	return s.src[s.pos]
}

func (s *scanner) skipWS() {
	for s.pos < len(s.src) {
		switch s.src[s.pos] {
		case ' ', '\t', '\n', '\r':
			s.pos++
		default:
			return
		}
	}
}

func (s *scanner) finish() error {
	s.skipWS()
	if !s.eof() {
		return s.fail(CodeTrailing, "data follows the root value")
	}
	return nil
}

func (s *scanner) expect(want byte) error {
	if s.eof() {
		return s.fail(CodeTruncated, "unexpected end of input")
	}
	if s.src[s.pos] != want {
		return s.fail(CodeMalformed, "unexpected punctuation")
	}
	s.pos++
	return nil
}

func (s *scanner) checkStringLimit(start int, dst []byte) error {
	if s.limits.MaxStringBytes > 0 && len(dst) > s.limits.MaxStringBytes {
		return s.failAt(CodeLimit, start, "decoded string exceeds MaxStringBytes")
	}
	return nil
}

func (s *scanner) parseString(dst *[]byte) ([]byte, error) {
	start := s.pos
	*dst = (*dst)[:0]
	if err := s.expect('"'); err != nil {
		return nil, err
	}
	for {
		if s.eof() {
			return nil, s.fail(CodeTruncated, "unterminated string")
		}
		c := s.src[s.pos]
		switch {
		case c == '"':
			s.pos++
			return *dst, nil
		case c == '\\':
			if err := s.parseEscape(dst, start); err != nil {
				return nil, err
			}
		case c < 0x20:
			return nil, s.fail(CodeMalformed, "control character in string")
		case c < utf8.RuneSelf:
			*dst = append(*dst, c)
			s.pos++
		default:
			_, size := utf8.DecodeRune(s.src[s.pos:])
			if size == 1 {
				return nil, s.fail(CodeInvalidUTF8, "invalid UTF-8 in string")
			}
			*dst = append(*dst, s.src[s.pos:s.pos+size]...)
			s.pos += size
		}
		if err := s.checkStringLimit(start, *dst); err != nil {
			return nil, err
		}
	}
}

func (s *scanner) parseEscape(dst *[]byte, start int) error {
	s.pos++
	if s.eof() {
		return s.fail(CodeTruncated, "unterminated escape")
	}
	c := s.src[s.pos]
	s.pos++
	switch c {
	case '"', '\\', '/':
		*dst = append(*dst, c)
	case 'b':
		*dst = append(*dst, '\b')
	case 'f':
		*dst = append(*dst, '\f')
	case 'n':
		*dst = append(*dst, '\n')
	case 'r':
		*dst = append(*dst, '\r')
	case 't':
		*dst = append(*dst, '\t')
	case 'u':
		r, err := s.parseUnicode()
		if err != nil {
			return err
		}
		*dst = utf8.AppendRune(*dst, r)
	default:
		return s.fail(CodeMalformed, "invalid escape")
	}
	return s.checkStringLimit(start, *dst)
}

func (s *scanner) parseUnicode() (rune, error) {
	hi, err := s.parseHex4()
	if err != nil {
		return 0, err
	}
	if hi >= 0xd800 && hi <= 0xdbff {
		if s.pos+2 > len(s.src) {
			return 0, s.fail(CodeTruncated, "incomplete surrogate pair")
		}
		if s.src[s.pos] != '\\' || s.src[s.pos+1] != 'u' {
			return 0, s.fail(CodeMalformed, "invalid surrogate pair")
		}
		s.pos += 2
		lo, err := s.parseHex4()
		if err != nil {
			return 0, err
		}
		if lo < 0xdc00 || lo > 0xdfff {
			return 0, s.fail(CodeMalformed, "invalid low surrogate")
		}
		return 0x10000 + (rune(hi)-0xd800)<<10 + rune(lo-0xdc00), nil
	}
	if hi >= 0xdc00 && hi <= 0xdfff {
		return 0, s.fail(CodeMalformed, "lone low surrogate")
	}
	return rune(hi), nil
}

func (s *scanner) parseHex4() (uint16, error) {
	if s.pos+4 > len(s.src) {
		return 0, s.fail(CodeTruncated, "incomplete unicode escape")
	}
	var value uint16
	for range 4 {
		c := s.src[s.pos]
		s.pos++
		value <<= 4
		switch {
		case c >= '0' && c <= '9':
			value |= uint16(c - '0')
		case c >= 'a' && c <= 'f':
			value |= uint16(c-'a') + 10
		case c >= 'A' && c <= 'F':
			value |= uint16(c-'A') + 10
		default:
			return 0, s.fail(CodeMalformed, "invalid unicode escape")
		}
	}
	return value, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isDelimiter(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ',', ']', '}':
		return true
	}
	return false
}

func (s *scanner) parseInteger() (int64, error) {
	start := s.pos
	negative := false
	if s.peek() == '-' {
		negative = true
		s.pos++
	}
	if s.eof() {
		return 0, s.fail(CodeTruncated, "incomplete integer")
	}
	var value uint64
	if s.peek() == '0' {
		s.pos++
		if !s.eof() && isDigit(s.peek()) {
			return 0, s.fail(CodeMalformed, "leading zero")
		}
	} else if s.peek() >= '1' && s.peek() <= '9' {
		for !s.eof() && isDigit(s.peek()) {
			digit := uint64(s.peek() - '0')
			if value > (uint64(math.MaxInt64)+boolUint64(negative)-digit)/10 {
				return 0, s.failAt(CodeLimit, start, "integer exceeds int64")
			}
			value = value*10 + digit
			s.pos++
		}
	} else {
		return 0, s.fail(CodeMalformed, "expected integer")
	}
	if !s.eof() && !isDelimiter(s.peek()) {
		return 0, s.fail(CodeMalformed, "invalid integer suffix")
	}
	if negative {
		if value == 1<<63 {
			return math.MinInt64, nil
		}
		return -int64(value), nil
	}
	return int64(value), nil
}

func boolUint64(v bool) uint64 {
	if v {
		return 1
	}
	return 0
}

func (s *scanner) parseLiteral() error {
	var literal []byte
	switch s.peek() {
	case 't':
		literal = literalTrue
	case 'f':
		literal = literalFalse
	case 'n':
		literal = literalNull
	default:
		return s.fail(CodeMalformed, "expected JSON value")
	}
	if s.pos+len(literal) > len(s.src) {
		return s.fail(CodeTruncated, "incomplete literal")
	}
	if !bytes.Equal(s.src[s.pos:s.pos+len(literal)], literal) {
		return s.fail(CodeMalformed, "invalid literal")
	}
	s.pos += len(literal)
	if !s.eof() && !isDelimiter(s.peek()) {
		return s.fail(CodeMalformed, "invalid literal suffix")
	}
	return nil
}

func (s *scanner) skipValue(depth int) error {
	if s.limits.MaxDepth > 0 && depth > s.limits.MaxDepth {
		return s.fail(CodeLimit, "JSON depth exceeds MaxDepth")
	}
	s.skipWS()
	switch s.peek() {
	case '"':
		_, err := s.parseString(&s.valueScratch)
		return err
	case '{':
		return s.skipObject(depth)
	case '[':
		return s.skipArray(depth)
	case 't', 'f', 'n':
		return s.parseLiteral()
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		_, err := s.parseInteger()
		return err
	case 0:
		return s.fail(CodeTruncated, "missing JSON value")
	default:
		return s.fail(CodeMalformed, "invalid JSON value")
	}
}

func (s *scanner) skipObject(depth int) error {
	s.pos++
	s.skipWS()
	if s.peek() == '}' {
		s.pos++
		return nil
	}
	for {
		if s.peek() != '"' {
			return s.fail(CodeMalformed, "object key must be a string")
		}
		if _, err := s.parseString(&s.keyScratch); err != nil {
			return err
		}
		s.skipWS()
		if err := s.expect(':'); err != nil {
			return err
		}
		if err := s.skipValue(depth + 1); err != nil {
			return err
		}
		s.skipWS()
		switch s.peek() {
		case '}':
			s.pos++
			return nil
		case ',':
			s.pos++
			s.skipWS()
			if s.peek() == '}' {
				return s.fail(CodeMalformed, "trailing object comma")
			}
		default:
			return s.fail(CodeMalformed, "expected object delimiter")
		}
	}
}

func (s *scanner) skipArray(depth int) error {
	s.pos++
	s.skipWS()
	if s.peek() == ']' {
		s.pos++
		return nil
	}
	for {
		if err := s.skipValue(depth + 1); err != nil {
			return err
		}
		s.skipWS()
		switch s.peek() {
		case ']':
			s.pos++
			return nil
		case ',':
			s.pos++
			s.skipWS()
			if s.peek() == ']' {
				return s.fail(CodeMalformed, "trailing array comma")
			}
		default:
			return s.fail(CodeMalformed, "expected array delimiter")
		}
	}
}

func (d *Decoder) count(input Input, source []byte, limits Limits) (shape, error) {
	maxBytes := limits.MaxRequestBytes
	if input == InputEvidence {
		maxBytes = limits.MaxEvidenceBytes
	}
	if maxBytes > 0 && len(source) > maxBytes {
		return shape{}, &Error{Input: input, Code: CodeLimit, Offset: maxBytes, Message: "source exceeds byte limit"}
	}
	s := &d.scan
	s.reset(input, source, limits)
	s.skipWS()
	if err := s.expect('{'); err != nil {
		return shape{}, err
	}
	var result shape
	var saw uint8
	for {
		s.skipWS()
		if s.eof() {
			return shape{}, s.fail(CodeTruncated, "unterminated root object")
		}
		if s.peek() == '}' {
			s.pos++
			if saw != 0b111 {
				return shape{}, s.fail(CodeMissingKey, "root object is missing required keys")
			}
			if err := s.finish(); err != nil {
				return shape{}, err
			}
			return result, nil
		}
		if s.peek() != '"' {
			return shape{}, s.fail(CodeMalformed, "root key must be a string")
		}
		keyOffset := s.pos
		key, err := s.parseString(&s.keyScratch)
		if err != nil {
			return shape{}, err
		}
		s.skipWS()
		if err := s.expect(':'); err != nil {
			return shape{}, err
		}
		s.skipWS()
		var bit uint8
		switch {
		case bytes.Equal(key, keyVersion):
			bit = 1
			version, err := s.parseInteger()
			if err != nil {
				return shape{}, err
			}
			if version != 1 {
				return shape{}, s.failAt(CodeInvalidVersion, keyOffset, "schema_version must be 1")
			}
		case bytes.Equal(key, keyPack):
			bit = 2
			if s.peek() != '"' {
				return shape{}, s.fail(CodeInvalidType, "pack must be a string")
			}
			if _, err := s.parseString(&s.valueScratch); err != nil {
				return shape{}, err
			}
		case input == InputRequests && bytes.Equal(key, keyRequests):
			bit = 4
			if err := d.countRows(s, input, &result); err != nil {
				return shape{}, err
			}
		case input == InputEvidence && bytes.Equal(key, keyEvidence):
			bit = 4
			if err := d.countRows(s, input, &result); err != nil {
				return shape{}, err
			}
		default:
			return shape{}, s.failAt(CodeUnknownKey, keyOffset, "unknown root key")
		}
		if saw&bit != 0 {
			return shape{}, s.failAt(CodeDuplicateKey, keyOffset, "duplicate root key")
		}
		saw |= bit
		s.skipWS()
		switch s.peek() {
		case ',':
			s.pos++
			s.skipWS()
			if s.peek() == '}' {
				return shape{}, s.fail(CodeMalformed, "trailing root comma")
			}
		case '}':
		default:
			return shape{}, s.fail(CodeMalformed, "expected root delimiter")
		}
	}
}

func (d *Decoder) countRows(s *scanner, input Input, result *shape) error {
	if s.peek() != '[' {
		return s.fail(CodeInvalidType, "payload must be an array")
	}
	s.pos++
	s.skipWS()
	if s.peek() == ']' {
		s.pos++
		return nil
	}
	for {
		if s.peek() != '{' {
			return s.fail(CodeInvalidType, "payload row must be an object")
		}
		if input == InputRequests {
			if result.requests == math.MaxUint32 ||
				(s.limits.MaxRequests > 0 && result.requests >= s.limits.MaxRequests) {
				return s.fail(CodeLimit, "requests exceed MaxRequests")
			}
			result.requests++
			if err := d.countRequestRefs(s, result); err != nil {
				return err
			}
		} else {
			if result.evidence == math.MaxUint32 ||
				(s.limits.MaxEvidence > 0 && result.evidence >= s.limits.MaxEvidence) {
				return s.fail(CodeLimit, "evidence exceeds MaxEvidence")
			}
			result.evidence++
			if err := s.skipValue(2); err != nil {
				return err
			}
		}
		s.skipWS()
		switch s.peek() {
		case ']':
			s.pos++
			return nil
		case ',':
			s.pos++
			s.skipWS()
			if s.peek() == ']' {
				return s.fail(CodeMalformed, "trailing payload comma")
			}
		default:
			return s.fail(CodeMalformed, "expected payload delimiter")
		}
	}
}

func (d *Decoder) countRequestRefs(s *scanner, result *shape) error {
	s.pos++
	s.skipWS()
	if s.peek() == '}' {
		s.pos++
		return nil
	}
	sawRefs := false
	for {
		if s.peek() != '"' {
			return s.fail(CodeMalformed, "request key must be a string")
		}
		keyOffset := s.pos
		key, err := s.parseString(&s.keyScratch)
		if err != nil {
			return err
		}
		s.skipWS()
		if err := s.expect(':'); err != nil {
			return err
		}
		s.skipWS()
		if bytes.Equal(key, keyRefs) {
			if sawRefs {
				return s.failAt(CodeDuplicateKey, keyOffset, "duplicate evidence_refs")
			}
			sawRefs = true
			if err := d.countRefs(s, result); err != nil {
				return err
			}
		} else if err := s.skipValue(3); err != nil {
			return err
		}
		s.skipWS()
		switch s.peek() {
		case '}':
			s.pos++
			return nil
		case ',':
			s.pos++
			s.skipWS()
			if s.peek() == '}' {
				return s.fail(CodeMalformed, "trailing request comma")
			}
		default:
			return s.fail(CodeMalformed, "expected request delimiter")
		}
	}
}

func (d *Decoder) countRefs(s *scanner, result *shape) error {
	if s.peek() != '[' {
		return s.fail(CodeInvalidType, "evidence_refs must be an array")
	}
	s.pos++
	s.skipWS()
	if s.peek() == ']' {
		s.pos++
		return nil
	}
	for {
		if s.peek() != '"' {
			return s.fail(CodeInvalidType, "evidence reference must be a string")
		}
		if _, err := s.parseString(&s.valueScratch); err != nil {
			return err
		}
		if result.refs == math.MaxUint32 ||
			(s.limits.MaxEvidenceRefs > 0 && result.refs >= s.limits.MaxEvidenceRefs) {
			return s.fail(CodeLimit, "references exceed MaxEvidenceRefs")
		}
		result.refs++
		s.skipWS()
		switch s.peek() {
		case ']':
			s.pos++
			return nil
		case ',':
			s.pos++
			s.skipWS()
			if s.peek() == ']' {
				return s.fail(CodeMalformed, "trailing reference comma")
			}
		default:
			return s.fail(CodeMalformed, "expected reference delimiter")
		}
	}
}
