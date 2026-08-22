// Package jsonbatch decodes request and evidence JSON directly into typed SoA
// evaluation batches without maps, reflection, or success-path strings.
package jsonbatch

import (
	"bytes"
	"math"
	"unicode/utf8"
)

var (
	literalTrue  = []byte("true")
	literalFalse = []byte("false")
	literalNull  = []byte("null")
)

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
