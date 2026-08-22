package jsonbatch

import (
	"bytes"
	"math"

	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/schema"
)

var (
	keyID               = []byte("id")
	keyType             = []byte("type")
	keyAttributes       = []byte("attributes")
	keyStatus           = []byte("status")
	keySubject          = []byte("subject")
	keyAdjustmentType   = []byte("adjustment_type")
	keyScope            = []byte("scope")
	keyReviewer         = []byte("reviewer")
	keyReviewerState    = []byte("reviewer_state")
	keyTiming           = []byte("timing")
	keyTimestamp        = []byte("timestamp")
	keyTimestampState   = []byte("timestamp_state")
	keyAttestationState = []byte("attestation_state")
	valueCurrent        = []byte("current")
	valueValid          = []byte("valid")
	valueConflict       = []byte("conflict")
	valueConflicting    = []byte("conflicting")
	valueReviewerSplit  = []byte("one_valid_one_revoked")
)

func canonicalID(value []byte, prefix byte) (uint32, bool) {
	if len(value) < 2 || value[0] != prefix || value[1] == '0' {
		return 0, false
	}
	var id uint64
	for _, c := range value[1:] {
		if c < '0' || c > '9' {
			return 0, false
		}
		id = id*10 + uint64(c-'0')
		if id > math.MaxUint32 {
			return 0, false
		}
	}
	return uint32(id), id != 0
}

func numericSlot(id uint32, mask uint64) int {
	return int((uint64(id) * 11400714819323198485) & mask)
}

func (d *Decoder) prepareEvidenceIDs(rows uint32) bool {
	size := 4
	for uint64(size) < 2*uint64(rows) {
		if size > math.MaxInt/2 {
			return false
		}
		size <<= 1
	}
	d.evidenceIDKeys = resizeZero(d.evidenceIDKeys, size)
	d.evidenceIDRows = resizeZero(d.evidenceIDRows, size)
	return true
}

func (d *Decoder) insertEvidenceID(id schema.EvidenceID, row uint32) bool {
	mask := uint64(len(d.evidenceIDKeys) - 1)
	slot := numericSlot(uint32(id), mask)
	for probes := 0; probes < len(d.evidenceIDKeys); probes++ {
		if d.evidenceIDKeys[slot] == 0 {
			d.evidenceIDKeys[slot] = id
			d.evidenceIDRows[slot] = row + 1
			return true
		}
		if d.evidenceIDKeys[slot] == id {
			return false
		}
		slot = (slot + 1) & int(mask)
	}
	return false
}

func (d *Decoder) lookupEvidenceRow(id schema.EvidenceID) (uint32, bool) {
	if id == 0 || len(d.evidenceIDKeys) == 0 {
		return 0, false
	}
	mask := uint64(len(d.evidenceIDKeys) - 1)
	slot := numericSlot(uint32(id), mask)
	for probes := 0; probes < len(d.evidenceIDKeys); probes++ {
		key := d.evidenceIDKeys[slot]
		if key == 0 {
			return 0, false
		}
		if key == id {
			return d.evidenceIDRows[slot] - 1, true
		}
		slot = (slot + 1) & int(mask)
	}
	return 0, false
}

func (d *Decoder) decodeEvidence(dst *eval.Builder, source []byte, limits Limits, expected uint32) error {
	if !d.prepareEvidenceIDs(expected) {
		return &Error{Input: InputEvidence, Code: CodeLimit, Message: "evidence ID index exceeds host limits"}
	}
	s := &d.scan
	s.reset(InputEvidence, source, limits)
	s.skipWS()
	if err := s.expect('{'); err != nil {
		return err
	}
	var saw uint8
	var rows uint32
	for {
		s.skipWS()
		if s.eof() {
			return s.fail(CodeTruncated, "unterminated evidence root")
		}
		if s.peek() == '}' {
			s.pos++
			if saw != 0b111 || rows != expected {
				return s.fail(CodeMissingKey, "incomplete evidence root")
			}
			return s.finish()
		}
		keyOffset := s.pos
		if s.peek() != '"' {
			return s.fail(CodeMalformed, "evidence root key must be a string")
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
		case bytes.Equal(key, keyEvidence):
			bit = 4
			count, err := d.decodeEvidenceArray(dst, s)
			if err != nil {
				return err
			}
			rows = count
		default:
			return s.failAt(CodeUnknownKey, keyOffset, "unknown evidence root key")
		}
		if saw&bit != 0 {
			return s.failAt(CodeDuplicateKey, keyOffset, "duplicate evidence root key")
		}
		saw |= bit
		if err := consumeObjectDelimiter(s, "evidence root"); err != nil {
			return err
		}
	}
}

func (d *Decoder) decodePack(s *scanner) error {
	if s.peek() != '"' {
		return s.fail(CodeInvalidType, "pack must be a string")
	}
	value, err := s.parseString(&s.valueScratch)
	if err != nil {
		return err
	}
	want, ok := d.program.Symbol(d.program.PolicyName)
	if !ok || !bytes.Equal(value, want) {
		return s.fail(CodeInvalidReference, "pack does not match Program")
	}
	return nil
}

func consumeObjectDelimiter(s *scanner, name string) error {
	s.skipWS()
	switch s.peek() {
	case ',':
		s.pos++
		s.skipWS()
		if s.peek() == '}' {
			return s.fail(CodeMalformed, "trailing object comma")
		}
		return nil
	case '}':
		return nil
	default:
		return s.fail(CodeMalformed, "expected object delimiter")
	}
}

func (d *Decoder) decodeEvidenceArray(dst *eval.Builder, s *scanner) (uint32, error) {
	if s.peek() != '[' {
		return 0, s.fail(CodeInvalidType, "evidence must be an array")
	}
	s.pos++
	s.skipWS()
	if s.peek() == ']' {
		s.pos++
		return 0, nil
	}
	var row uint32
	for {
		if s.peek() != '{' {
			return 0, s.fail(CodeInvalidType, "evidence row must be an object")
		}
		if err := d.decodeEvidenceRecord(dst, s, row); err != nil {
			return 0, err
		}
		row++
		s.skipWS()
		switch s.peek() {
		case ']':
			s.pos++
			return row, nil
		case ',':
			s.pos++
			s.skipWS()
			if s.peek() == ']' {
				return 0, s.fail(CodeMalformed, "trailing evidence comma")
			}
		default:
			return 0, s.fail(CodeMalformed, "expected evidence delimiter")
		}
	}
}

func (d *Decoder) decodeEvidenceRecord(dst *eval.Builder, s *scanner, row uint32) error {
	s.pos++
	s.skipWS()
	var record eval.EvidenceRecord
	var saw uint8
	for {
		if s.peek() == '}' {
			s.pos++
			if saw != 0b111 {
				return s.fail(CodeMissingKey, "evidence row is missing required keys")
			}
			if !d.insertEvidenceID(record.ID, row) {
				return s.fail(CodeDuplicateID, "duplicate evidence ID")
			}
			if err := dst.SetEvidence(row, record); err != nil {
				return s.fail(CodeInvalidType, "evidence row does not fit batch")
			}
			return nil
		}
		keyOffset := s.pos
		if s.peek() != '"' {
			return s.fail(CodeMalformed, "evidence key must be a string")
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
		case bytes.Equal(key, keyID):
			bit = 1
			value, err := requireString(s, "evidence id")
			if err != nil {
				return err
			}
			id, ok := canonicalID(value, 'E')
			if !ok {
				return s.failAt(CodeInvalidID, keyOffset, "invalid evidence ID")
			}
			record.ID = schema.EvidenceID(id)
		case bytes.Equal(key, keyType):
			bit = 2
			value, err := requireString(s, "evidence type")
			if err != nil {
				return err
			}
			kind, ok := d.lookupEvidenceKind(value)
			if !ok {
				return s.failAt(CodeInvalidReference, keyOffset, "unknown evidence type")
			}
			record.Kind = kind
		case bytes.Equal(key, keyAttributes):
			bit = 4
			if err := d.decodeEvidenceAttributes(dst, s, &record); err != nil {
				return err
			}
		default:
			return s.failAt(CodeUnknownKey, keyOffset, "unknown evidence key")
		}
		if saw&bit != 0 {
			return s.failAt(CodeDuplicateKey, keyOffset, "duplicate evidence key")
		}
		saw |= bit
		if err := consumeObjectDelimiter(s, "evidence"); err != nil {
			return err
		}
	}
}

func requireString(s *scanner, name string) ([]byte, error) {
	if s.peek() != '"' {
		return nil, s.fail(CodeInvalidType, name+" must be a string")
	}
	return s.parseString(&s.valueScratch)
}

func (d *Decoder) decodeEvidenceAttributes(dst *eval.Builder, s *scanner, record *eval.EvidenceRecord) error {
	if s.peek() != '{' {
		return s.fail(CodeInvalidType, "attributes must be an object")
	}
	s.pos++
	s.skipWS()
	var saw uint16
	var override schema.EvidenceStateID
	var count uint32
	for {
		if s.peek() == '}' {
			s.pos++
			if saw&1 == 0 {
				return s.fail(CodeMissingKey, "attributes.status is required")
			}
			if override != 0 {
				record.State = override
			}
			return nil
		}
		if s.limits.MaxEvidenceAttributes > 0 && count >= s.limits.MaxEvidenceAttributes {
			return s.fail(CodeLimit, "attributes exceed MaxEvidenceAttributes")
		}
		count++
		keyOffset := s.pos
		if s.peek() != '"' {
			return s.fail(CodeMalformed, "attribute key must be a string")
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
		bit, err := d.decodeEvidenceAttribute(dst, s, key, keyOffset, record, &override)
		if err != nil {
			return err
		}
		if saw&bit != 0 {
			return s.failAt(CodeDuplicateKey, keyOffset, "duplicate evidence attribute")
		}
		saw |= bit
		if err := consumeObjectDelimiter(s, "attributes"); err != nil {
			return err
		}
	}
}

func (d *Decoder) decodeEvidenceAttribute(dst *eval.Builder, s *scanner, key []byte, keyOffset int, record *eval.EvidenceRecord, override *schema.EvidenceStateID) (uint16, error) {
	if bytes.Equal(key, keyTimestamp) {
		value, err := s.parseInteger()
		if err != nil {
			return 0, err
		}
		record.Timestamp = value
		return 1 << 5, nil
	}
	value, err := requireString(s, "evidence attribute")
	if err != nil {
		return 0, err
	}
	switch {
	case bytes.Equal(key, keyStatus):
		state, ok := d.lookupEvidenceState(value)
		if !ok {
			return 0, s.failAt(CodeInvalidReference, keyOffset, "unknown evidence state")
		}
		record.State = state
		return 1, nil
	case bytes.Equal(key, keySubject), bytes.Equal(key, keyAdjustmentType):
		id, err := dst.InternSymbol(value)
		if err != nil {
			return 0, s.failAt(CodeLimit, keyOffset, "symbol namespace exhausted")
		}
		record.Subject = id
		return 1 << 1, nil
	case bytes.Equal(key, keyScope):
		id, err := dst.InternSymbol(value)
		if err != nil {
			return 0, s.failAt(CodeLimit, keyOffset, "symbol namespace exhausted")
		}
		record.Scope = id
		return 1 << 2, nil
	case bytes.Equal(key, keyReviewer), bytes.Equal(key, keyReviewerState):
		id, err := dst.InternSymbol(value)
		if err != nil {
			return 0, s.failAt(CodeLimit, keyOffset, "symbol namespace exhausted")
		}
		record.Reviewer = id
		if bytes.Equal(key, keyReviewerState) && bytes.Equal(value, valueReviewerSplit) {
			state, ok := d.lookupEvidenceState(valueConflicting)
			if !ok {
				return 0, s.failAt(CodeInvalidReference, keyOffset, "conflicting state is absent from Program")
			}
			*override = state
		}
		return 1 << 3, nil
	case bytes.Equal(key, keyTiming):
		id, err := dst.InternSymbol(value)
		if err != nil {
			return 0, s.failAt(CodeLimit, keyOffset, "symbol namespace exhausted")
		}
		record.Timing = id
		return 1 << 4, nil
	case bytes.Equal(key, keyTimestampState), bytes.Equal(key, keyAttestationState):
		state, ok := d.qualifierState(value)
		if !ok {
			return 0, s.failAt(CodeInvalidReference, keyOffset, "unknown evidence qualifier")
		}
		if state != 0 {
			*override = state
		}
		if bytes.Equal(key, keyTimestampState) {
			return 1 << 6, nil
		}
		return 1 << 7, nil
	default:
		return 0, s.failAt(CodeUnknownKey, keyOffset, "unknown evidence attribute")
	}
}

func (d *Decoder) qualifierState(value []byte) (schema.EvidenceStateID, bool) {
	if bytes.Equal(value, valueCurrent) || bytes.Equal(value, valueValid) {
		return 0, true
	}
	lookup := value
	if bytes.Equal(value, valueConflict) {
		lookup = valueConflicting
	}
	state, ok := d.lookupEvidenceState(lookup)
	return state, ok
}
