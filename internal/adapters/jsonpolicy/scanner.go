package jsonpolicy

import (
	"bytes"
	"math"
	"unicode/utf8"

	"github.com/sebishogun/nornrune/internal/schema"
)

// decoder walks one JSON byte slice without allocation. The scratch buffers
// are reused across every string token in one Decode call.
type decoder struct {
	fields  *schema.Schema
	symbols *schema.Interner

	keyScratch   []byte
	valueScratch []byte

	nodeScratch    []schema.NodeID
	valueIDScratch []schema.ValueID
	clauseScratch  []schema.ClauseID
	remedyScratch  []schema.RemediationID

	// Keep src and pos adjacent for the byte scanner after grouping every
	// pointer-bearing field into the shortest possible GC scan region.
	src    []byte
	pos    int
	limits Limits
	saw    uint8
}

// fail returns an Error at the current position.
func (d *decoder) fail(code ErrorCode, message string) error {
	return &Error{Code: code, Offset: d.pos, Message: message}
}

// failAt returns an Error at a recorded position.
func (d *decoder) failAt(code ErrorCode, offset int, message string) error {
	return &Error{Code: code, Offset: offset, Message: message}
}

// atEOF reports whether the whole source has been consumed.
func (d *decoder) atEOF() bool { return d.pos >= len(d.src) }

// peek returns the next byte, or 0 at end of input.
func (d *decoder) peek() byte {
	if d.atEOF() {
		return 0
	}
	return d.src[d.pos]
}

// skipWS advances past JSON whitespace.
func (d *decoder) skipWS() {
	for d.pos < len(d.src) {
		switch d.src[d.pos] {
		case ' ', '\t', '\n', '\r':
			d.pos++
		default:
			return
		}
	}
}

// expectPunct consumes one required punctuation byte.
func (d *decoder) expectPunct(b byte) error {
	if d.atEOF() {
		return d.fail(CodeTruncated, "unexpected end of input")
	}
	if d.src[d.pos] != b {
		return d.fail(CodeMalformed, "expected '"+string(b)+"'")
	}
	d.pos++
	return nil
}

func isDigit(b byte) bool { return '0' <= b && b <= '9' }

// isDelimiter reports whether b can legally follow a literal or integer.
func isDelimiter(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', ',', ']', '}', ':':
		return true
	}
	return false
}

var (
	litTrue  = []byte("true")
	litFalse = []byte("false")
	litNull  = []byte("null")
)

// parseLiteral consumes one exact true, false, or null literal and reports
// which. A literal must be followed by a delimiter or end of input.
func (d *decoder) parseLiteral() (isTrue, isNull bool, err error) {
	var lit []byte
	switch d.peek() {
	case 't':
		lit = litTrue
	case 'f':
		lit = litFalse
	case 'n':
		lit = litNull
	default:
		return false, false, d.fail(CodeMalformed, "expected a JSON literal")
	}
	if d.pos+len(lit) > len(d.src) {
		return false, false, d.fail(CodeTruncated, "unexpected end of input in literal")
	}
	if !bytes.Equal(d.src[d.pos:d.pos+len(lit)], lit) {
		return false, false, d.fail(CodeMalformed, "malformed literal")
	}
	d.pos += len(lit)
	if !d.atEOF() && !isDelimiter(d.src[d.pos]) {
		return false, false, d.fail(CodeMalformed, "literal must be followed by a delimiter")
	}
	return lit[0] == 't', lit[0] == 'n', nil
}

// parseInteger consumes a JSON integer literal. Only the grammar -?[0-9]+
// with no leading zeros is accepted; fractions, exponents, and out-of-int64
// values are rejected.
func (d *decoder) parseInteger() (int64, error) {
	start := d.pos
	neg := false
	if d.peek() == '-' {
		neg = true
		d.pos++
		if d.atEOF() {
			return 0, d.fail(CodeTruncated, "unexpected end of input in integer")
		}
	}
	var v uint64
	switch b := d.peek(); {
	case b == '0':
		d.pos++
	case '1' <= b && b <= '9':
		d.pos++
		v = uint64(b - '0')
		for !d.atEOF() && isDigit(d.src[d.pos]) {
			if v > math.MaxInt64/10 {
				return 0, d.failAt(CodeLimit, start, "integer literal exceeds int64")
			}
			v = v*10 + uint64(d.src[d.pos]-'0')
			d.pos++
		}
	default:
		return 0, d.fail(CodeMalformed, "expected a digit")
	}
	if !d.atEOF() {
		b := d.src[d.pos]
		if b == '.' || b == 'e' || b == 'E' {
			return 0, d.fail(CodeMalformed, "fractions and exponents are not accepted")
		}
		if isDigit(b) {
			return 0, d.fail(CodeMalformed, "leading zeros are not allowed")
		}
		if !isDelimiter(b) {
			return 0, d.fail(CodeMalformed, "expected a delimiter after integer")
		}
	}
	if neg {
		if v > 1<<63 {
			return 0, d.failAt(CodeLimit, start, "integer literal exceeds int64")
		}
		return -int64(v), nil
	}
	if v > math.MaxInt64 {
		return 0, d.failAt(CodeLimit, start, "integer literal exceeds int64")
	}
	return int64(v), nil
}

// checkStringLimit rejects a decoded string that has grown past
// MaxStringBytes. start is the opening-quote position.
func (d *decoder) checkStringLimit(start int, scratch []byte) error {
	if d.limits.MaxStringBytes > 0 && len(scratch) > d.limits.MaxStringBytes {
		return d.failAt(CodeLimit, start, "decoded string exceeds MaxStringBytes")
	}
	return nil
}

// parseString consumes a JSON string literal, decodes every escape into
// UTF-8, and appends the result to scratch. The returned slice is a view of
// scratch and is only valid until the next parseString call on the same
// buffer. Surrogate pairs must be adjacent and well formed; lone surrogates
// are rejected. MaxStringBytes is enforced during growth so an oversized
// token cannot grow far past the bound.
func (d *decoder) parseString(scratch *[]byte) ([]byte, error) {
	start := d.pos
	*scratch = (*scratch)[:0]
	d.pos++ // opening quote; callers guarantee it
	for {
		if d.atEOF() {
			return nil, d.fail(CodeTruncated, "unterminated string")
		}
		c := d.src[d.pos]
		switch {
		case c == '"':
			d.pos++
			if err := d.checkStringLimit(start, *scratch); err != nil {
				return nil, err
			}
			return *scratch, nil
		case c == '\\':
			if err := d.parseEscape(scratch, start); err != nil {
				return nil, err
			}
		case c < 0x20:
			return nil, d.fail(CodeMalformed, "control character in string")
		case c < 0x80:
			*scratch = append(*scratch, c)
			if err := d.checkStringLimit(start, *scratch); err != nil {
				return nil, err
			}
			d.pos++
		default:
			r, size := utf8.DecodeRune(d.src[d.pos:])
			if r == utf8.RuneError && size == 1 {
				return nil, d.fail(CodeInvalidUTF8, "invalid UTF-8 in string")
			}
			*scratch = append(*scratch, d.src[d.pos:d.pos+size]...)
			if err := d.checkStringLimit(start, *scratch); err != nil {
				return nil, err
			}
			d.pos += size
		}
	}
}

// parseEscape consumes the bytes after a backslash and decodes them.
func (d *decoder) parseEscape(scratch *[]byte, start int) error {
	d.pos++ // backslash
	if d.atEOF() {
		return d.fail(CodeTruncated, "unexpected end of input in escape")
	}
	c := d.src[d.pos]
	d.pos++
	switch c {
	case '"', '\\', '/':
		*scratch = append(*scratch, c)
		return d.checkStringLimit(start, *scratch)
	case 'b':
		*scratch = append(*scratch, '\b')
		return d.checkStringLimit(start, *scratch)
	case 'f':
		*scratch = append(*scratch, '\f')
		return d.checkStringLimit(start, *scratch)
	case 'n':
		*scratch = append(*scratch, '\n')
		return d.checkStringLimit(start, *scratch)
	case 'r':
		*scratch = append(*scratch, '\r')
		return d.checkStringLimit(start, *scratch)
	case 't':
		*scratch = append(*scratch, '\t')
		return d.checkStringLimit(start, *scratch)
	case 'u':
		r, err := d.parseUnicodeEscape()
		if err != nil {
			return err
		}
		*scratch = utf8.AppendRune(*scratch, r)
		return d.checkStringLimit(start, *scratch)
	default:
		return d.fail(CodeMalformed, "invalid escape sequence")
	}
}

// parseUnicodeEscape consumes one \uXXXX escape, combining a surrogate pair
// when present.
func (d *decoder) parseUnicodeEscape() (rune, error) {
	hi, err := d.parseHex4()
	if err != nil {
		return 0, err
	}
	if hi >= 0xD800 && hi <= 0xDBFF {
		if d.atEOF() {
			return 0, d.fail(CodeTruncated, "unexpected end of input after high surrogate")
		}
		if d.src[d.pos] != '\\' {
			return 0, d.fail(CodeMalformed, "high surrogate must be followed by \\u")
		}
		d.pos++
		if d.atEOF() {
			return 0, d.fail(CodeTruncated, "unexpected end of input after high surrogate")
		}
		if d.src[d.pos] != 'u' {
			return 0, d.fail(CodeMalformed, "high surrogate must be followed by \\u")
		}
		d.pos++
		lo, err := d.parseHex4()
		if err != nil {
			return 0, err
		}
		if lo < 0xDC00 || lo > 0xDFFF {
			return 0, d.fail(CodeMalformed, "invalid low surrogate")
		}
		return 0x10000 + (rune(hi)-0xD800)<<10 + (rune(lo) - 0xDC00), nil
	}
	if hi >= 0xDC00 && hi <= 0xDFFF {
		return 0, d.fail(CodeMalformed, "lone low surrogate")
	}
	return rune(hi), nil
}

// parseHex4 consumes exactly four hexadecimal digits.
func (d *decoder) parseHex4() (uint16, error) {
	if d.pos+4 > len(d.src) {
		return 0, d.fail(CodeTruncated, "unexpected end of input in \\u escape")
	}
	var v uint16
	for i := 0; i < 4; i++ {
		c := d.src[d.pos]
		d.pos++
		v <<= 4
		switch {
		case '0' <= c && c <= '9':
			v |= uint16(c - '0')
		case 'a' <= c && c <= 'f':
			v |= uint16(c-'a') + 10
		case 'A' <= c && c <= 'F':
			v |= uint16(c-'A') + 10
		default:
			return 0, d.fail(CodeMalformed, "invalid hex digit in \\u escape")
		}
	}
	return v, nil
}
