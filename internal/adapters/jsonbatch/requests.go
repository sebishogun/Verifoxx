package jsonbatch

import (
	"bytes"
	"math"

	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func (d *Decoder) prepareRequests(rows, refs uint32) bool {
	size := 4
	for uint64(size) < 2*uint64(rows) {
		if size > math.MaxInt/2 {
			return false
		}
		size <<= 1
	}
	d.requestIDKeys = resizeZero(d.requestIDKeys, size)
	d.requestOffsets = resizeZero(d.requestOffsets, int(uint64(rows)+1))
	d.requestRefs = resizeZero(d.requestRefs, int(refs))
	d.seenFields = resizeZero(d.seenFields, (len(d.program.FieldNames)+63)>>6)
	d.seenRefs = resizeZero(d.seenRefs, (len(d.evidenceIDKeys)+63)>>6)
	return true
}

func (d *Decoder) insertRequestID(id schema.RequestID) bool {
	mask := uint64(len(d.requestIDKeys) - 1)
	slot := numericSlot(uint32(id), mask)
	for probes := 0; probes < len(d.requestIDKeys); probes++ {
		if d.requestIDKeys[slot] == 0 {
			d.requestIDKeys[slot] = id
			return true
		}
		if d.requestIDKeys[slot] == id {
			return false
		}
		slot = (slot + 1) & int(mask)
	}
	return false
}

func (d *Decoder) decodeRequests(dst *eval.Builder, source []byte, limits Limits, expectedRows, expectedRefs uint32) error {
	if !d.prepareRequests(expectedRows, expectedRefs) {
		return &Error{Input: InputRequests, Code: CodeLimit, Message: "request scratch exceeds host limits"}
	}
	s := &d.scan
	s.reset(InputRequests, source, limits)
	s.skipWS()
	if err := s.expect('{'); err != nil {
		return err
	}
	var saw uint8
	var rows, refs uint32
	for {
		s.skipWS()
		if s.eof() {
			return s.fail(CodeTruncated, "unterminated request root")
		}
		if s.peek() == '}' {
			s.pos++
			if saw != 0b111 || rows != expectedRows || refs != expectedRefs {
				return s.fail(CodeMissingKey, "incomplete request root")
			}
			if err := dst.SetEvidenceCSR(d.requestOffsets, d.requestRefs); err != nil {
				return s.fail(CodeInvalidReference, "invalid evidence relation")
			}
			return s.finish()
		}
		keyOffset := s.pos
		if s.peek() != '"' {
			return s.fail(CodeMalformed, "request root key must be a string")
		}
		key, err := s.parseString(&s.keyScratch)
		if err != nil {
			return err
		}
		s.skipWS()
		if err := s.expect(':'); err != nil {
			return err
		}
		s.skipWS()
		var bit uint8
		switch {
		case bytes.Equal(key, keyVersion):
			bit = 1
			version, err := s.parseInteger()
			if err != nil {
				return err
			}
			if version != 1 {
				return s.failAt(CodeInvalidVersion, keyOffset, "schema_version must be 1")
			}
		case bytes.Equal(key, keyPack):
			bit = 2
			if err := d.decodePack(s); err != nil {
				return err
			}
		case bytes.Equal(key, keyRequests):
			bit = 4
			rowCount, refCount, err := d.decodeRequestArray(dst, s)
			if err != nil {
				return err
			}
			rows, refs = rowCount, refCount
		default:
			return s.failAt(CodeUnknownKey, keyOffset, "unknown request root key")
		}
		if saw&bit != 0 {
			return s.failAt(CodeDuplicateKey, keyOffset, "duplicate request root key")
		}
		saw |= bit
		if err := consumeObjectDelimiter(s, "request root"); err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeRequestArray(dst *eval.Builder, s *scanner) (uint32, uint32, error) {
	if s.peek() != '[' {
		return 0, 0, s.fail(CodeInvalidType, "requests must be an array")
	}
	s.pos++
	s.skipWS()
	if s.peek() == ']' {
		s.pos++
		return 0, 0, nil
	}
	var row, ref uint32
	for {
		if s.peek() != '{' {
			return 0, 0, s.fail(CodeInvalidType, "request row must be an object")
		}
		clear(d.seenFields)
		clear(d.seenRefs)
		d.requestOffsets[row] = ref
		if err := d.decodeRequestRecord(dst, s, row, &ref); err != nil {
			return 0, 0, err
		}
		row++
		d.requestOffsets[row] = ref
		s.skipWS()
		switch s.peek() {
		case ']':
			s.pos++
			return row, ref, nil
		case ',':
			s.pos++
			s.skipWS()
			if s.peek() == ']' {
				return 0, 0, s.fail(CodeMalformed, "trailing request comma")
			}
		default:
			return 0, 0, s.fail(CodeMalformed, "expected request delimiter")
		}
	}
}

func (d *Decoder) decodeRequestRecord(dst *eval.Builder, s *scanner, row uint32, ref *uint32) error {
	s.pos++
	s.skipWS()
	var id schema.RequestID
	var sawID, sawRefs bool
	var facts uint32
	for {
		if s.peek() == '}' {
			s.pos++
			if !sawID {
				return s.fail(CodeMissingKey, "request id is required")
			}
			if !d.insertRequestID(id) {
				return s.fail(CodeDuplicateID, "duplicate request ID")
			}
			if err := dst.SetRequestID(row, id); err != nil {
				return s.fail(CodeInvalidType, "request row does not fit batch")
			}
			return nil
		}
		keyOffset := s.pos
		if s.peek() != '"' {
			return s.fail(CodeMalformed, "request key must be a string")
		}
		key, err := s.parseString(&s.keyScratch)
		if err != nil {
			return err
		}
		s.skipWS()
		if err := s.expect(':'); err != nil {
			return err
		}
		s.skipWS()
		switch {
		case bytes.Equal(key, keyID):
			if sawID {
				return s.failAt(CodeDuplicateKey, keyOffset, "duplicate request id")
			}
			sawID = true
			value, err := requireString(s, "request id")
			if err != nil {
				return err
			}
			n, ok := canonicalID(value, 'R')
			if !ok {
				return s.failAt(CodeInvalidID, keyOffset, "invalid request ID")
			}
			id = schema.RequestID(n)
		case bytes.Equal(key, keyRefs):
			if sawRefs {
				return s.failAt(CodeDuplicateKey, keyOffset, "duplicate evidence_refs")
			}
			sawRefs = true
			if err := d.decodeRequestRefs(s, ref); err != nil {
				return err
			}
		default:
			if s.limits.MaxFactsPerRequest > 0 && facts >= s.limits.MaxFactsPerRequest {
				return s.fail(CodeLimit, "facts exceed MaxFactsPerRequest")
			}
			if s.peek() == '{' {
				if err := d.decodeFactGroup(dst, s, row, key, &facts); err != nil {
					return err
				}
			} else {
				if err := d.decodeFact(dst, s, row, key, keyOffset); err != nil {
					return err
				}
				facts++
			}
		}
		if err := consumeObjectDelimiter(s, "request"); err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeFactGroup(dst *eval.Builder, s *scanner, row uint32, prefix []byte, facts *uint32) error {
	d.pathScratch = append(d.pathScratch[:0], prefix...)
	prefixLen := len(d.pathScratch)
	s.pos++
	s.skipWS()
	count := 0
	for {
		if s.peek() == '}' {
			s.pos++
			if count == 0 {
				return s.fail(CodeInvalidReference, "empty fact group")
			}
			return nil
		}
		keyOffset := s.pos
		if s.peek() != '"' {
			return s.fail(CodeMalformed, "fact key must be a string")
		}
		key, err := s.parseString(&s.keyScratch)
		if err != nil {
			return err
		}
		d.pathScratch = d.pathScratch[:prefixLen]
		d.pathScratch = append(d.pathScratch, '.')
		d.pathScratch = append(d.pathScratch, key...)
		s.skipWS()
		if err := s.expect(':'); err != nil {
			return err
		}
		s.skipWS()
		if s.peek() == '{' || s.peek() == '[' {
			return s.fail(CodeInvalidType, "fact value must be scalar")
		}
		if s.limits.MaxFactsPerRequest > 0 && *facts >= s.limits.MaxFactsPerRequest {
			return s.fail(CodeLimit, "facts exceed MaxFactsPerRequest")
		}
		if err := d.decodeFact(dst, s, row, d.pathScratch, keyOffset); err != nil {
			return err
		}
		*facts++
		count++
		if err := consumeObjectDelimiter(s, "fact group"); err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeFact(dst *eval.Builder, s *scanner, row uint32, path []byte, offset int) error {
	field, ok := d.lookupField(path)
	if !ok {
		return s.failAt(CodeInvalidReference, offset, "unknown request field")
	}
	field0 := uint32(field - 1)
	word, bit := field0>>6, uint64(1)<<(field0&63)
	if d.seenFields[word]&bit != 0 {
		return s.failAt(CodeDuplicateField, offset, "duplicate request field")
	}
	d.seenFields[word] |= bit
	if s.peek() == 'n' {
		return s.parseLiteral()
	}
	kind, _, ok := d.program.FieldIndex.Lookup(field)
	if !ok {
		return s.failAt(CodeInvalidReference, offset, "invalid Program field")
	}
	var err error
	switch kind {
	case schema.ValueKindSymbol:
		value, parseErr := requireString(s, "symbol fact")
		if parseErr != nil {
			return parseErr
		}
		id, internErr := dst.InternSymbol(value)
		if internErr != nil {
			return s.failAt(CodeLimit, offset, "symbol namespace exhausted")
		}
		err = dst.SetSymbol(row, field, id)
	case schema.ValueKindInteger:
		if s.peek() != '-' && !isDigit(s.peek()) {
			return s.fail(CodeInvalidType, "integer fact must be a JSON integer")
		}
		value, parseErr := s.parseInteger()
		if parseErr != nil {
			return parseErr
		}
		err = dst.SetInteger(row, field, value)
	case schema.ValueKindTimestamp:
		if s.peek() != '-' && !isDigit(s.peek()) {
			return s.fail(CodeInvalidType, "timestamp fact must be a JSON integer")
		}
		value, parseErr := s.parseInteger()
		if parseErr != nil {
			return parseErr
		}
		err = dst.SetTimestamp(row, field, value)
	case schema.ValueKindBoolean:
		if s.peek() != 't' && s.peek() != 'f' {
			return s.fail(CodeInvalidType, "Boolean fact must be true or false")
		}
		value := s.peek() == 't'
		if parseErr := s.parseLiteral(); parseErr != nil {
			return parseErr
		}
		err = dst.SetBoolean(row, field, value)
	case schema.ValueKindPresence:
		if s.peek() != 't' {
			return s.fail(CodeInvalidType, "presence fact must be true or null")
		}
		if parseErr := s.parseLiteral(); parseErr != nil {
			return parseErr
		}
		err = dst.SetPresent(row, field)
	default:
		return s.failAt(CodeInvalidReference, offset, "invalid Program field kind")
	}
	if err != nil {
		return s.failAt(CodeInvalidType, offset, "fact does not fit batch")
	}
	return nil
}

func (d *Decoder) decodeRequestRefs(s *scanner, next *uint32) error {
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
		value, err := requireString(s, "evidence reference")
		if err != nil {
			return err
		}
		id, ok := canonicalID(value, 'E')
		if !ok {
			return s.fail(CodeInvalidID, "invalid evidence reference")
		}
		row, ok := d.lookupEvidenceRow(schema.EvidenceID(id))
		if !ok {
			return s.fail(CodeInvalidReference, "evidence reference does not exist")
		}
		word, bit := row>>6, uint64(1)<<(row&63)
		if d.seenRefs[word]&bit != 0 {
			return s.fail(CodeDuplicateReference, "duplicate evidence reference")
		}
		d.seenRefs[word] |= bit
		if uint64(*next) >= uint64(len(d.requestRefs)) {
			return s.fail(CodeLimit, "evidence reference count changed after sizing")
		}
		d.requestRefs[*next] = row
		*next++
		s.skipWS()
		switch s.peek() {
		case ']':
			s.pos++
			return nil
		case ',':
			s.pos++
			s.skipWS()
			if s.peek() == ']' {
				return s.fail(CodeMalformed, "trailing evidence_refs comma")
			}
		default:
			return s.fail(CodeMalformed, "expected evidence_refs delimiter")
		}
	}
}
