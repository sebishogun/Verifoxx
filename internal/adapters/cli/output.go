package cli

import (
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/sebishogun/verifoxx/internal/compile"
)

const outputLowerHex = "0123456789abcdef"

func writeComplete(w io.Writer, data []byte) error {
	if w == nil {
		return io.ErrClosedPipe
	}
	n, err := w.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}

func appendOutputString(dst, value []byte) []byte {
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
				dst = append(dst, '\\', 'u', '0', '0', outputLowerHex[c>>4], outputLowerHex[c&0xf])
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
			dst = append(dst, '\\', 'u', '2', '0', '2', outputLowerHex[byte(r)&0xf])
			i += size
			start = i
			continue
		}
		i += size
	}
	dst = append(dst, value[start:]...)
	return append(dst, '"')
}

func appendOutputHash(dst []byte, hash [32]byte) []byte {
	for _, value := range hash {
		dst = append(dst, outputLowerHex[value>>4], outputLowerHex[value&0xf])
	}
	return dst
}

func appendDiagnostics(dst []byte, diagnostics []compile.Diagnostic) []byte {
	dst = append(dst, "{\"valid\":false,\"diagnostics\":["...)
	for row, diagnostic := range diagnostics {
		if row != 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, "{\"code\":"...)
		dst = appendOutputString(dst, []byte(diagnostic.Code.String()))
		dst = append(dst, ",\"table\":"...)
		dst = appendOutputString(dst, []byte(diagnostic.Table.String()))
		dst = append(dst, ",\"row\":"...)
		dst = strconv.AppendUint(dst, uint64(diagnostic.Row), 10)
		dst = append(dst, ",\"member\":"...)
		dst = appendOutputString(dst, []byte(diagnostic.Member.String()))
		dst = append(dst, ",\"span\":{\"start\":"...)
		dst = strconv.AppendUint(dst, uint64(diagnostic.Span.Start), 10)
		dst = append(dst, ",\"end\":"...)
		dst = strconv.AppendUint(dst, uint64(diagnostic.Span.End), 10)
		dst = append(dst, "},\"ids\":{"...)
		first := true
		dst, first = appendDiagnosticID(dst, first, "node", uint32(diagnostic.Node))
		dst, first = appendDiagnosticID(dst, first, "clause", uint32(diagnostic.Clause))
		dst, first = appendDiagnosticID(dst, first, "requirement", uint32(diagnostic.Requirement))
		dst, first = appendDiagnosticID(dst, first, "field", uint32(diagnostic.Field))
		dst, first = appendDiagnosticID(dst, first, "value", uint32(diagnostic.Value))
		dst, first = appendDiagnosticID(dst, first, "outcome", uint32(diagnostic.Outcome))
		dst, first = appendDiagnosticID(dst, first, "remediation", uint32(diagnostic.Remediation))
		dst, first = appendDiagnosticID(dst, first, "evidence_kind", uint32(diagnostic.EvidenceKind))
		dst, _ = appendDiagnosticID(dst, first, "evidence_state", uint32(diagnostic.EvidenceState))
		dst = append(dst, "}}"...)
	}
	return append(dst, "]}\n"...)
}

func appendDiagnosticID(dst []byte, first bool, name string, id uint32) ([]byte, bool) {
	if id == 0 {
		return dst, first
	}
	if !first {
		dst = append(dst, ',')
	}
	dst = appendOutputString(dst, []byte(name))
	dst = append(dst, ':')
	dst = strconv.AppendUint(dst, uint64(id), 10)
	return dst, false
}
