package jsonpolicy

import (
	"testing"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func TestFindCatalogNameSkipsMalformedRowsAndReturnsOneBasedRow(t *testing.T) {
	builder := ast.NewBuilder(ast.Hints{Values: 2, SymbolValues: 2, SymbolBytes: 16})
	first, err := builder.AddSymbolValue([]byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := builder.AddSymbolValue([]byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	names := []schema.ValueID{0, first, 99, second}
	if row, ok := findCatalogName(builder.Document(), names, []byte("second")); !ok || row != 4 {
		t.Fatalf("findCatalogName(second) = %d, %t, want 4, true", row, ok)
	}
	if row, ok := findCatalogName(builder.Document(), names, []byte("absent")); ok || row != 0 {
		t.Fatalf("findCatalogName(absent) = %d, %t, want 0, false", row, ok)
	}
}
