// Package wire provides allocation-free canonical encoding shared by adapters.
package wire

import (
	"crypto/sha256"
	"unicode/utf8"
)

const lowerHex = "0123456789abcdef"

// AppendJSONString appends value as one JSON string using the adapters'
// canonical HTML, line-separator, and invalid-UTF-8 escaping contract.
func AppendJSONString(dst, value []byte) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(value); {
		c := value[i]
		if c < utf8.RuneSelf {
			if c >= 0x20 && c != '\\' && c != '"' && c != '<' && c != '>' && c != '&' {
				i++
				continue
			}
			dst = append(dst, value[start:i]...)
			switch c {
			case '\\', '"':
				dst = append(dst, '\\', c)
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				dst = append(dst, '\\', 'u', '0', '0', lowerHex[c>>4], lowerHex[c&0xf])
			}
			i++
			start = i
			continue
		}

		r, size := utf8.DecodeRune(value[i:])
		if r == utf8.RuneError && size == 1 {
			dst = append(dst, value[start:i]...)
			dst = append(dst, '\\', 'u', 'f', 'f', 'f', 'd')
			i++
			start = i
			continue
		}
		if r == '\u2028' || r == '\u2029' {
			dst = append(dst, value[start:i]...)
			dst = append(dst, '\\', 'u', '2', '0', '2', lowerHex[byte(r)&0xf])
			i += size
			start = i
			continue
		}
		i += size
	}
	dst = append(dst, value[start:]...)
	return append(dst, '"')
}

// AppendSHA256 appends hash as 64 lowercase hexadecimal bytes.
func AppendSHA256(dst []byte, hash [sha256.Size]byte) []byte {
	for _, value := range hash {
		dst = append(dst, lowerHex[value>>4], lowerHex[value&0xf])
	}
	return dst
}

// DecodeSHA256 decodes 64 case-insensitive hexadecimal bytes. Failure returns
// a zero hash and does not expose a partial decode.
func DecodeSHA256(source string) ([sha256.Size]byte, bool) {
	var hash [sha256.Size]byte
	if len(source) != sha256.Size*2 {
		return hash, false
	}
	for index := range hash {
		high, highOK := decodeHexNibble(source[index*2])
		low, lowOK := decodeHexNibble(source[index*2+1])
		if !highOK || !lowOK {
			return [sha256.Size]byte{}, false
		}
		hash[index] = high<<4 | low
	}
	return hash, true
}

func decodeHexNibble(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}
