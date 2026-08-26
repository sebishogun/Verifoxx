// Package rego translates a bounded Rego v1 subset into the shared semantic
// frontend representation.
package rego

import (
	"bytes"
	"strings"
	"unicode/utf8"

	opaast "github.com/open-policy-agent/opa/v1/ast"

	public "github.com/sebishogun/nornrune/frontend"
)

// Parsed owns one official OPA module and the inputs that produced it. OPA
// parser objects never leave this package.
type Parsed struct {
	module   *opaast.Module
	source   []byte
	bindings public.BindingSet
	limits   public.Limits
}

// Parse validates bounded inputs and parses one Rego v1 module.
func Parse(source []byte, bindings public.BindingSet, limits public.Limits) (*Parsed, []public.Diagnostic) {
	if diagnostics := validateInputs(source, bindings, limits); len(diagnostics) != 0 {
		return nil, diagnostics
	}
	module, err := opaast.ParseModuleWithOpts("policy.rego", string(source), opaast.ParserOptions{RegoVersion: opaast.RegoV1})
	if err != nil {
		return nil, parseDiagnostics(source, err, limits.MaxDiagnostics)
	}
	if module == nil || module.Package == nil || len(module.Package.Path) < 2 {
		return nil, []public.Diagnostic{newDiagnostic(public.CodeInvalidPolicy, public.Span{}, 0, 0)}
	}
	if len(module.Imports) != 0 {
		diagnostics := make([]public.Diagnostic, 0, min(len(module.Imports), int(limits.MaxDiagnostics)))
		for _, imported := range module.Imports {
			if uint64(len(diagnostics)) >= uint64(limits.MaxDiagnostics) {
				break
			}
			diagnostics = append(diagnostics, newDiagnostic(public.CodeUnsupported, locationSpan(source, imported.Location), uint32(len(diagnostics)+1), 0))
		}
		public.SortDiagnostics(diagnostics)
		return nil, diagnostics
	}
	return &Parsed{module: module.Copy(), source: bytes.Clone(source), bindings: cloneBindings(bindings), limits: limits}, nil
}

func validateInputs(source []byte, bindings public.BindingSet, limits public.Limits) []public.Diagnostic {
	if !limits.Valid() {
		return []public.Diagnostic{newDiagnostic(public.CodeLimit, public.Span{}, 0, 0)}
	}
	if uint64(len(source)) > uint64(limits.MaxSourceBytes) {
		return []public.Diagnostic{newDiagnostic(public.CodeLimit, public.Span{End: uint32(min(len(source), 1))}, 0, 0)}
	}
	if !utf8.Valid(source) {
		start := firstInvalidUTF8(source)
		return []public.Diagnostic{newDiagnostic(public.CodeSyntax, public.Span{Start: uint32(start), End: uint32(start + 1)}, 0, 0)}
	}
	if uint64(len(bindings.Fields)) > uint64(limits.MaxFields) || bindingStringBytes(bindings) > uint64(limits.MaxStringBytes) {
		return []public.Diagnostic{newDiagnostic(public.CodeLimit, public.Span{}, 0, 0)}
	}
	if err := bindings.Validate(limits); err != nil || bindings.Decision == "" || strings.IndexByte(bindings.Decision, '.') >= 0 {
		return []public.Diagnostic{newDiagnostic(public.CodeInvalidBinding, public.Span{}, 0, 0)}
	}
	for row := range bindings.Fields {
		if !strings.HasPrefix(bindings.Fields[row].Source, "input.") || len(bindings.Fields[row].Source) == len("input.") {
			return []public.Diagnostic{newDiagnostic(public.CodeInvalidBinding, public.Span{}, uint32(row+1), public.FieldID(row+1))}
		}
	}
	return nil
}

func parseDiagnostics(source []byte, err error, limit uint32) []public.Diagnostic {
	errors, ok := err.(opaast.Errors)
	if !ok || len(errors) == 0 {
		return []public.Diagnostic{newDiagnostic(public.CodeSyntax, public.Span{}, 0, 0)}
	}
	diagnostics := make([]public.Diagnostic, 0, min(len(errors), int(limit)))
	for _, parseErr := range errors {
		if uint64(len(diagnostics)) >= uint64(limit) {
			break
		}
		row := uint32(0)
		if parseErr.Location != nil && parseErr.Location.Row > 0 {
			row = uint32(parseErr.Location.Row)
		}
		diagnostics = append(diagnostics, newDiagnostic(public.CodeSyntax, locationSpan(source, parseErr.Location), row, 0))
	}
	public.SortDiagnostics(diagnostics)
	return diagnostics
}

func locationSpan(source []byte, location *opaast.Location) public.Span {
	if location == nil {
		return public.Span{}
	}
	start := location.Offset
	if start < 0 {
		start = 0
	} else if start > len(source) {
		start = len(source)
	}
	end := start + len(location.Text)
	if end < start || end > len(source) {
		end = len(source)
	}
	return public.Span{Start: uint32(start), End: uint32(end)}
}

func newDiagnostic(code public.DiagnosticCode, span public.Span, row uint32, field public.FieldID) public.Diagnostic {
	return public.Diagnostic{Span: span, Row: row, Field: field, Language: public.LanguageRego, Code: code}
}

func bindingStringBytes(bindings public.BindingSet) uint64 {
	total := uint64(len(bindings.Name)) + uint64(len(bindings.Version)) + uint64(len(bindings.Decision))
	for row := range bindings.Fields {
		total += uint64(len(bindings.Fields[row].Source)) + uint64(len(bindings.Fields[row].Target))
	}
	return total
}

func firstInvalidUTF8(source []byte) int {
	for offset := 0; offset < len(source); {
		_, size := utf8.DecodeRune(source[offset:])
		if size == 1 && source[offset] >= utf8.RuneSelf {
			return offset
		}
		offset += size
	}
	return 0
}

func cloneBindings(bindings public.BindingSet) public.BindingSet {
	cloned := bindings
	cloned.Fields = append([]public.Binding(nil), bindings.Fields...)
	return cloned
}

func equalBindings(left, right public.BindingSet) bool {
	if left.Name != right.Name || left.Version != right.Version || left.Decision != right.Decision || len(left.Fields) != len(right.Fields) {
		return false
	}
	for row := range left.Fields {
		if left.Fields[row] != right.Fields[row] {
			return false
		}
	}
	return true
}
