package cli

import (
	"io"
	"strconv"

	public "github.com/sebishogun/nornrune/frontend"
	"github.com/sebishogun/nornrune/internal/adapters/wire"
	"github.com/sebishogun/nornrune/internal/compile"
)

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

func writeFrontendDiagnostics(w io.Writer, diagnostics []public.Diagnostic) error {
	if err := writeComplete(w, appendFrontendDiagnostics(nil, diagnostics)); err != nil {
		return operationalError(err)
	}
	return &commandError{err: errInvalidPolicy, code: 1, quiet: true}
}

func appendFrontendDiagnostics(dst []byte, diagnostics []public.Diagnostic) []byte {
	dst = append(dst, "{\"valid\":false,\"diagnostics\":["...)
	for row, diagnostic := range diagnostics {
		if row != 0 {
			dst = append(dst, ',')
		}
		dst = append(dst, "{\"language\":"...)
		dst = appendOutputString(dst, []byte(diagnostic.Language.String()))
		dst = append(dst, ",\"code\":"...)
		dst = appendOutputString(dst, []byte(diagnostic.Code.String()))
		dst = append(dst, ",\"span\":{\"start\":"...)
		dst = strconv.AppendUint(dst, uint64(diagnostic.Span.Start), 10)
		dst = append(dst, ",\"end\":"...)
		dst = strconv.AppendUint(dst, uint64(diagnostic.Span.End), 10)
		dst = append(dst, "},\"row\":"...)
		dst = strconv.AppendUint(dst, uint64(diagnostic.Row), 10)
		dst = append(dst, ",\"field\":"...)
		dst = strconv.AppendUint(dst, uint64(diagnostic.Field), 10)
		dst = append(dst, '}')
	}
	return append(dst, "]}\n"...)
}

func appendOutputString(dst, value []byte) []byte {
	return wire.AppendJSONString(dst, value)
}

func appendOutputHash(dst []byte, hash [32]byte) []byte {
	return wire.AppendSHA256(dst, hash)
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
