package security

import (
	"log/slog"
	"strings"
)

// RedactedValue is the stable replacement for sensitive structured values.
const RedactedValue = "[REDACTED]"

var sensitiveLogKeys = [...]string{
	"authorization",
	"cookie",
	"database_url",
	"evidence",
	"evidence_json",
	"evidence_payload",
	"password",
	"policy_source",
	"request_body",
	"request_payload",
	"requests",
	"requests_json",
	"secret",
	"source_json",
	"token",
}

var protectedRowKeys = [...]string{
	"row",
	"rows",
	"record",
	"records",
	"dataset_row",
	"dataset_rows",
	"protected_row",
	"protected_rows",
	"protected_record",
	"protected_records",
	"individual_record",
	"individual_records",
}

// RedactLogAttr is suitable for slog.HandlerOptions.ReplaceAttr. It preserves
// bounded operational metadata while replacing payloads and credentials.
func RedactLogAttr(groups []string, attr slog.Attr) slog.Attr {
	if sensitiveLogKey(attr.Key) {
		attr.Value = slog.StringValue(RedactedValue)
		return attr
	}
	for _, group := range groups {
		if sensitiveLogKey(group) {
			attr.Value = slog.StringValue(RedactedValue)
			return attr
		}
	}
	return attr
}

func sensitiveLogKey(key string) bool {
	for _, candidate := range sensitiveLogKeys {
		if strings.EqualFold(key, candidate) {
			return true
		}
	}
	return false
}

// ContainsProtectedRows reports whether a valid JSON audit object names a raw
// row or record container. The scan compares escaped object keys without
// materializing strings or maps.
func ContainsProtectedRows(source []byte) bool {
	for index := 0; index < len(source); index++ {
		if source[index] != '"' {
			continue
		}
		start := index + 1
		index = start
		for index < len(source) && source[index] != '"' {
			if source[index] == '\\' {
				index++
			}
			index++
		}
		if index >= len(source) {
			return false
		}
		end := index
		next := index + 1
		for next < len(source) && jsonSpace(source[next]) {
			next++
		}
		if next >= len(source) || source[next] != ':' {
			continue
		}
		for _, key := range protectedRowKeys {
			if encodedJSONStringEqualFold(source[start:end], key) {
				return true
			}
		}
	}
	return false
}

func encodedJSONStringEqualFold(encoded []byte, target string) bool {
	targetIndex := 0
	for index := 0; index < len(encoded); index++ {
		value := encoded[index]
		if value == '\\' {
			index++
			if index >= len(encoded) {
				return false
			}
			switch encoded[index] {
			case '"', '\\', '/':
				value = encoded[index]
			case 'u':
				if index+4 >= len(encoded) {
					return false
				}
				decoded, ok := decodeASCIIHex(encoded[index+1 : index+5])
				if !ok {
					return false
				}
				value = decoded
				index += 4
			default:
				return false
			}
		}
		if targetIndex >= len(target) || asciiLower(value) != target[targetIndex] {
			return false
		}
		targetIndex++
	}
	return targetIndex == len(target)
}

func asciiLower(value byte) byte {
	if value >= 'A' && value <= 'Z' {
		return value + ('a' - 'A')
	}
	return value
}

func decodeASCIIHex(encoded []byte) (byte, bool) {
	if len(encoded) != 4 || encoded[0] != '0' || encoded[1] != '0' {
		return 0, false
	}
	high, highOK := hexNibble(encoded[2])
	low, lowOK := hexNibble(encoded[3])
	if !highOK || !lowOK || high > 7 {
		return 0, false
	}
	return high<<4 | low, true
}

func hexNibble(value byte) (byte, bool) {
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

func jsonSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r'
}
