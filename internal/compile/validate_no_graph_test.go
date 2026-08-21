package compile

import (
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// validateNoGraph runs the structural then semantic phases without the graph
// phase. It isolates semantic tests built from partial documents or targeted
// mutations that are not intended to assert graph diagnostics. Production
// Validate always runs the graph phase after semantics. The method keeps the
// receiver's reusable state so validator-reuse tests behave as before.
func (v *Validator) validateNoGraph(dst []Diagnostic, doc *ast.Document, fields *schema.Schema) []Diagnostic {
	dst = v.validateStructure(dst, doc, fields)
	return v.validateSemantics(dst, doc, fields)
}
