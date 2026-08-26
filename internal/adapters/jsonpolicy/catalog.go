package jsonpolicy

import (
	"bytes"

	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/schema"
)

func findCatalogName(doc *ast.Document, names []schema.ValueID, name []byte) (uint32, bool) {
	if doc == nil {
		return 0, false
	}
	for row, valueID := range names {
		existing, ok := doc.SymbolValue(valueID)
		if ok && bytes.Equal(existing, name) {
			return uint32(row + 1), true
		}
	}
	return 0, false
}
