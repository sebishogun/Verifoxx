package sql

import (
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
)

func FuzzLex(f *testing.F) {
	f.Add([]byte("team = 'blue' AND count >= 2"), uint8(DialectPostgreSQL))
	f.Add([]byte("`team` = :team"), uint8(DialectDatabricks))
	f.Fuzz(func(t *testing.T, source []byte, encodedDialect uint8) {
		dialect := Dialect(encodedDialect%3 + 1)
		tokens, diagnostics := Lex(source, dialect, public.DefaultLimits())
		if tokens == nil {
			if len(diagnostics) == 0 {
				t.Fatal("Lex() returned neither tokens nor diagnostics")
			}
			return
		}
		if len(diagnostics) != 0 {
			t.Fatalf("Lex() returned tokens and diagnostics: %#v", diagnostics)
		}
		if len(tokens.Kinds) == 0 || tokens.Kinds[len(tokens.Kinds)-1] != TokenEOF {
			t.Fatal("token stream lacks EOF")
		}
		if len(tokens.Kinds) != len(tokens.Starts) || len(tokens.Kinds) != len(tokens.Ends) || len(tokens.Kinds) != len(tokens.Integers) {
			t.Fatal("token columns differ")
		}
		for row := range tokens.Kinds {
			if tokens.Starts[row] > tokens.Ends[row] || uint64(tokens.Ends[row]) > uint64(len(tokens.Source)) {
				t.Fatalf("invalid token span at row %d", row)
			}
		}
	})
}
