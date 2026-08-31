package sql

import (
	"reflect"
	"testing"
	"unicode/utf8"

	public "github.com/sebishogun/nornrune/frontend"
)

func TestLexPostgreSQLScalarTokensAndSpans(t *testing.T) {
	source := []byte("team = 'it''s blue' AND count >= -2 OR enabled IS NOT NULL; -- end\n$1")
	tokens, diagnostics := Lex(source, DialectPostgreSQL, public.DefaultLimits())
	if len(diagnostics) != 0 {
		t.Fatalf("Lex() diagnostics = %#v", diagnostics)
	}
	wantKinds := []TokenKind{
		TokenIdentifier, TokenEqual, TokenString, TokenAnd, TokenIdentifier,
		TokenGreaterEqual, TokenMinus, TokenInteger, TokenOr, TokenIdentifier,
		TokenIs, TokenNot, TokenNull, TokenSemicolon, TokenParameter, TokenEOF,
	}
	if !reflect.DeepEqual(tokens.Kinds, wantKinds) {
		t.Fatalf("kinds = %v, want %v", tokens.Kinds, wantKinds)
	}
	wantTexts := []string{"team", "=", "'it''s blue'", "AND", "count", ">=", "-", "2", "OR", "enabled", "IS", "NOT", "NULL", ";", "$1", ""}
	if got := tokenTexts(tokens); !reflect.DeepEqual(got, wantTexts) {
		t.Fatalf("texts = %#v, want %#v", got, wantTexts)
	}
	if len(tokens.Starts) != len(tokens.Kinds) || len(tokens.Ends) != len(tokens.Kinds) || len(tokens.Integers) != len(tokens.Kinds) {
		t.Fatalf("token columns differ: kinds=%d starts=%d ends=%d integers=%d", len(tokens.Kinds), len(tokens.Starts), len(tokens.Ends), len(tokens.Integers))
	}
	if got := tokens.Integers[7]; got != 2 {
		t.Fatalf("integer = %d, want 2", got)
	}
	source[0] = 'X'
	if tokens.Source[0] != 't' {
		t.Fatal("Lex() borrowed source")
	}
}

func TestLexDialectQuotesAndParameters(t *testing.T) {
	tests := []struct {
		name    string
		dialect Dialect
		source  string
		texts   []string
	}{
		{name: "postgresql", dialect: DialectPostgreSQL, source: `"Team" = $12`, texts: []string{`"Team"`, "=", "$12", ""}},
		{name: "snowflake named", dialect: DialectSnowflake, source: `"Team" = :team`, texts: []string{`"Team"`, "=", ":team", ""}},
		{name: "snowflake positional", dialect: DialectSnowflake, source: `team = ?`, texts: []string{"team", "=", "?", ""}},
		{name: "databricks", dialect: DialectDatabricks, source: "`Team` = :team", texts: []string{"`Team`", "=", ":team", ""}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tokens, diagnostics := Lex([]byte(test.source), test.dialect, public.DefaultLimits())
			if len(diagnostics) != 0 {
				t.Fatalf("Lex() diagnostics = %#v", diagnostics)
			}
			if got := tokenTexts(tokens); !reflect.DeepEqual(got, test.texts) {
				t.Fatalf("texts = %#v, want %#v", got, test.texts)
			}
			if tokens.Kinds[len(tokens.Kinds)-2] != TokenParameter {
				t.Fatalf("parameter kind = %v", tokens.Kinds[len(tokens.Kinds)-2])
			}
		})
	}
}

func TestLexCommentsAndUnicodeBoundaries(t *testing.T) {
	source := []byte("/* policy */ café = '蓝' -- line\nAND true")
	tokens, diagnostics := Lex(source, DialectPostgreSQL, public.DefaultLimits())
	if len(diagnostics) != 0 {
		t.Fatalf("Lex() diagnostics = %#v", diagnostics)
	}
	if got, want := tokenTexts(tokens), []string{"café", "=", "'蓝'", "AND", "true", ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("texts = %#v, want %#v", got, want)
	}
	for row := range tokens.Kinds {
		start, end := tokens.Starts[row], tokens.Ends[row]
		if start > end || uint64(end) > uint64(len(tokens.Source)) || !utf8.Valid(tokens.Source[start:end]) {
			t.Fatalf("token %d has invalid span [%d,%d)", row, start, end)
		}
	}
}

func TestLexRejectsMalformedOrMismatchedInputWithExactSpan(t *testing.T) {
	tests := []struct {
		name    string
		dialect Dialect
		source  []byte
		code    public.DiagnosticCode
		span    public.Span
	}{
		{name: "dialect", dialect: DialectInvalid, source: []byte("true"), code: public.CodeInvalidPolicy, span: public.Span{}},
		{name: "invalid utf8", dialect: DialectPostgreSQL, source: []byte{0xff}, code: public.CodeSyntax, span: public.Span{Start: 0, End: 1}},
		{name: "unterminated string", dialect: DialectPostgreSQL, source: []byte("'x"), code: public.CodeSyntax, span: public.Span{Start: 0, End: 2}},
		{name: "unterminated quote", dialect: DialectPostgreSQL, source: []byte(`"x`), code: public.CodeSyntax, span: public.Span{Start: 0, End: 2}},
		{name: "unterminated comment", dialect: DialectPostgreSQL, source: []byte("/* x"), code: public.CodeSyntax, span: public.Span{Start: 0, End: 4}},
		{name: "nested comment", dialect: DialectPostgreSQL, source: []byte("/* /* */"), code: public.CodeUnsupported, span: public.Span{Start: 3, End: 5}},
		{name: "wrong quote", dialect: DialectPostgreSQL, source: []byte("`x`"), code: public.CodeUnsupported, span: public.Span{Start: 0, End: 1}},
		{name: "wrong parameter", dialect: DialectPostgreSQL, source: []byte(":x"), code: public.CodeUnsupported, span: public.Span{Start: 0, End: 1}},
		{name: "integer overflow", dialect: DialectPostgreSQL, source: []byte("9223372036854775808"), code: public.CodeType, span: public.Span{Start: 0, End: 19}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tokens, diagnostics := Lex(test.source, test.dialect, public.DefaultLimits())
			if tokens != nil {
				t.Fatalf("Lex() tokens = %#v, want nil", tokens)
			}
			if len(diagnostics) != 1 || diagnostics[0].Code != test.code || diagnostics[0].Span != test.span || diagnostics[0].Dialect != test.dialect {
				t.Fatalf("diagnostics = %#v, want code %v span %#v", diagnostics, test.code, test.span)
			}
		})
	}
}

func TestLexLimitsAreAtomic(t *testing.T) {
	limits := public.DefaultLimits()
	limits.MaxSourceBytes = 3
	if tokens, diagnostics := Lex([]byte("true"), DialectPostgreSQL, limits); tokens != nil || len(diagnostics) != 1 || diagnostics[0].Code != public.CodeLimit {
		t.Fatalf("source limit = tokens %#v diagnostics %#v", tokens, diagnostics)
	}
	limits = public.DefaultLimits()
	limits.MaxNodes = 2
	if tokens, diagnostics := Lex([]byte("a = 1"), DialectPostgreSQL, limits); tokens != nil || len(diagnostics) != 1 || diagnostics[0].Code != public.CodeLimit {
		t.Fatalf("token limit = tokens %#v diagnostics %#v", tokens, diagnostics)
	}
}

func tokenTexts(tokens *Tokens) []string {
	texts := make([]string, len(tokens.Kinds))
	for row := range tokens.Kinds {
		texts[row] = string(tokens.Source[tokens.Starts[row]:tokens.Ends[row]])
	}
	return texts
}
