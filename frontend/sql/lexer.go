package sql

import (
	"math"
	"unicode"
	"unicode/utf8"

	public "github.com/sebishogun/nornrune/frontend"
)

// Lex scans one SQL source into owned parallel token columns.
func Lex(source []byte, dialect Dialect, limits public.Limits) (*Tokens, []Diagnostic) {
	if !dialect.Valid() {
		return nil, oneDiagnostic(dialect, public.CodeInvalidPolicy, public.Span{})
	}
	if !limits.Valid() || uint64(len(source)) > uint64(limits.MaxSourceBytes) {
		return nil, oneDiagnostic(dialect, public.CodeLimit, boundedSourceSpan(source))
	}
	if !utf8.Valid(source) {
		start := firstInvalidByte(source)
		return nil, oneDiagnostic(dialect, public.CodeSyntax, public.Span{Start: uint32(start), End: uint32(start + 1)})
	}

	hint := len(source)/2 + 1
	if hint > 256 {
		hint = 256
	}
	if uint64(hint) > uint64(limits.MaxNodes) {
		hint = int(limits.MaxNodes)
	}
	tokens := &Tokens{
		Source:   append([]byte(nil), source...),
		Kinds:    make([]TokenKind, 0, hint),
		Starts:   make([]uint32, 0, hint),
		Ends:     make([]uint32, 0, hint),
		Integers: make([]int64, 0, hint),
	}

	for offset := 0; offset < len(source); {
		if sqlSpace(source[offset]) {
			offset++
			continue
		}
		if offset+1 < len(source) && source[offset] == '-' && source[offset+1] == '-' {
			offset += 2
			for offset < len(source) && source[offset] != '\n' {
				offset++
			}
			continue
		}
		if offset+1 < len(source) && source[offset] == '/' && source[offset+1] == '*' {
			start := offset
			offset += 2
			closed := false
			for offset+1 < len(source) {
				if source[offset] == '/' && source[offset+1] == '*' {
					return nil, oneDiagnostic(dialect, public.CodeUnsupported, span(offset, offset+2))
				}
				if source[offset] == '*' && source[offset+1] == '/' {
					offset += 2
					closed = true
					break
				}
				offset++
			}
			if !closed {
				return nil, oneDiagnostic(dialect, public.CodeSyntax, span(start, len(source)))
			}
			continue
		}

		start := offset
		value := int64(0)
		kind := TokenInvalid
		switch source[offset] {
		case '\'':
			offset = scanQuoted(source, offset, '\'')
			if offset < 0 {
				return nil, oneDiagnostic(dialect, public.CodeSyntax, span(start, len(source)))
			}
			kind = TokenString
		case '"':
			offset = scanQuoted(source, offset, '"')
			if offset < 0 {
				return nil, oneDiagnostic(dialect, public.CodeSyntax, span(start, len(source)))
			}
			if dialect == DialectPostgreSQL && offset == start+2 {
				return nil, oneDiagnostic(dialect, public.CodeSyntax, span(start, offset))
			}
			kind = TokenIdentifier
		case '`':
			if dialect != DialectDatabricks {
				return nil, oneDiagnostic(dialect, public.CodeUnsupported, span(start, start+1))
			}
			offset = scanQuoted(source, offset, '`')
			if offset < 0 {
				return nil, oneDiagnostic(dialect, public.CodeSyntax, span(start, len(source)))
			}
			kind = TokenIdentifier
		case '$':
			if dialect != DialectPostgreSQL {
				return nil, oneDiagnostic(dialect, public.CodeUnsupported, span(start, start+1))
			}
			offset++
			if offset == len(source) || source[offset] < '1' || source[offset] > '9' {
				return nil, oneDiagnostic(dialect, public.CodeSyntax, span(start, offset))
			}
			for offset < len(source) && source[offset] >= '0' && source[offset] <= '9' {
				offset++
			}
			kind = TokenParameter
		case ':':
			if dialect == DialectPostgreSQL {
				return nil, oneDiagnostic(dialect, public.CodeUnsupported, span(start, start+1))
			}
			offset++
			if offset == len(source) || !asciiIdentifierStart(source[offset]) {
				return nil, oneDiagnostic(dialect, public.CodeSyntax, span(start, offset))
			}
			for offset < len(source) && asciiIdentifierContinue(source[offset]) {
				offset++
			}
			kind = TokenParameter
		case '?':
			if dialect == DialectPostgreSQL {
				return nil, oneDiagnostic(dialect, public.CodeUnsupported, span(start, start+1))
			}
			offset++
			kind = TokenParameter
		case '=':
			offset++
			kind = TokenEqual
		case '<':
			offset++
			kind = TokenLess
			if offset < len(source) && source[offset] == '=' {
				offset++
				kind = TokenLessEqual
			} else if offset < len(source) && source[offset] == '>' {
				offset++
				kind = TokenNotEqual
			}
		case '>':
			offset++
			kind = TokenGreater
			if offset < len(source) && source[offset] == '=' {
				offset++
				kind = TokenGreaterEqual
			}
		case '!':
			offset++
			if offset == len(source) || source[offset] != '=' {
				return nil, oneDiagnostic(dialect, public.CodeUnsupported, span(start, offset))
			}
			offset++
			kind = TokenNotEqual
		case '(':
			offset++
			kind = TokenLParen
		case ')':
			offset++
			kind = TokenRParen
		case ',':
			offset++
			kind = TokenComma
		case ';':
			offset++
			kind = TokenSemicolon
		case '.':
			offset++
			kind = TokenDot
		case '-':
			offset++
			kind = TokenMinus
		default:
			if source[offset] >= '0' && source[offset] <= '9' {
				var overflow bool
				maximum := uint64(math.MaxInt64)
				if len(tokens.Kinds) != 0 && tokens.Kinds[len(tokens.Kinds)-1] == TokenMinus {
					maximum++
				}
				var magnitude uint64
				offset, magnitude, overflow = scanInteger(source, offset, maximum)
				if overflow {
					return nil, oneDiagnostic(dialect, public.CodeType, span(start, offset))
				}
				value = int64(magnitude)
				kind = TokenInteger
				break
			}
			if !identifierRuneStart(source[offset:]) {
				_, size := utf8.DecodeRune(source[offset:])
				return nil, oneDiagnostic(dialect, public.CodeSyntax, span(start, start+size))
			}
			offset = scanIdentifier(source, offset)
			kind = keywordKind(source[start:offset])
		}
		if !appendToken(tokens, kind, start, offset, value, limits.MaxNodes) {
			return nil, oneDiagnostic(dialect, public.CodeLimit, span(start, offset))
		}
	}
	if !appendToken(tokens, TokenEOF, len(source), len(source), 0, limits.MaxNodes) {
		return nil, oneDiagnostic(dialect, public.CodeLimit, span(len(source), len(source)))
	}
	return tokens, nil
}

func appendToken(tokens *Tokens, kind TokenKind, start, end int, integer int64, maximum uint32) bool {
	if uint64(len(tokens.Kinds))+1 > uint64(maximum) {
		return false
	}
	tokens.Kinds = append(tokens.Kinds, kind)
	tokens.Starts = append(tokens.Starts, uint32(start))
	tokens.Ends = append(tokens.Ends, uint32(end))
	tokens.Integers = append(tokens.Integers, integer)
	return true
}

func scanQuoted(source []byte, start int, quote byte) int {
	for offset := start + 1; offset < len(source); offset++ {
		if source[offset] != quote {
			continue
		}
		if offset+1 < len(source) && source[offset+1] == quote {
			offset++
			continue
		}
		return offset + 1
	}
	return -1
}

func scanInteger(source []byte, start int, maximum uint64) (int, uint64, bool) {
	offset := start
	value := uint64(0)
	for offset < len(source) && source[offset] >= '0' && source[offset] <= '9' {
		digit := uint64(source[offset] - '0')
		if value > (maximum-digit)/10 {
			for offset < len(source) && source[offset] >= '0' && source[offset] <= '9' {
				offset++
			}
			return offset, 0, true
		}
		value = value*10 + digit
		offset++
	}
	return offset, value, false
}

func scanIdentifier(source []byte, start int) int {
	offset := start
	for offset < len(source) {
		r, size := utf8.DecodeRune(source[offset:])
		if offset == start {
			if r != '_' && !unicode.IsLetter(r) {
				return start
			}
		} else if r != '_' && r != '$' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			break
		}
		offset += size
	}
	return offset
}

func identifierRuneStart(source []byte) bool {
	r, _ := utf8.DecodeRune(source)
	return r == '_' || unicode.IsLetter(r)
}

func keywordKind(value []byte) TokenKind {
	switch {
	case equalFoldASCII(value, "true"):
		return TokenTrue
	case equalFoldASCII(value, "false"):
		return TokenFalse
	case equalFoldASCII(value, "null"):
		return TokenNull
	case equalFoldASCII(value, "and"):
		return TokenAnd
	case equalFoldASCII(value, "or"):
		return TokenOr
	case equalFoldASCII(value, "not"):
		return TokenNot
	case equalFoldASCII(value, "in"):
		return TokenIn
	case equalFoldASCII(value, "is"):
		return TokenIs
	case equalFoldASCII(value, "as"):
		return TokenAs
	case equalFoldASCII(value, "for"):
		return TokenFor
	case equalFoldASCII(value, "to"):
		return TokenTo
	case equalFoldASCII(value, "using"):
		return TokenUsing
	case equalFoldASCII(value, "with"):
		return TokenWith
	case equalFoldASCII(value, "check"):
		return TokenCheck
	case equalFoldASCII(value, "create"):
		return TokenCreate
	case equalFoldASCII(value, "policy"):
		return TokenPolicy
	case equalFoldASCII(value, "on"):
		return TokenOn
	case equalFoldASCII(value, "permissive"):
		return TokenPermissive
	case equalFoldASCII(value, "restrictive"):
		return TokenRestrictive
	case equalFoldASCII(value, "all"):
		return TokenAll
	case equalFoldASCII(value, "select"):
		return TokenSelect
	case equalFoldASCII(value, "insert"):
		return TokenInsert
	case equalFoldASCII(value, "update"):
		return TokenUpdate
	case equalFoldASCII(value, "delete"):
		return TokenDelete
	case equalFoldASCII(value, "public"):
		return TokenPublic
	default:
		return TokenIdentifier
	}
}

func equalFoldASCII(value []byte, word string) bool {
	if len(value) != len(word) {
		return false
	}
	for row, current := range value {
		if current >= 'A' && current <= 'Z' {
			current += 'a' - 'A'
		}
		if current != word[row] {
			return false
		}
	}
	return true
}

func firstInvalidByte(source []byte) int {
	for offset := 0; offset < len(source); {
		_, size := utf8.DecodeRune(source[offset:])
		if size == 1 && source[offset] >= utf8.RuneSelf {
			return offset
		}
		offset += size
	}
	return 0
}

func oneDiagnostic(dialect Dialect, code public.DiagnosticCode, sourceSpan public.Span) []Diagnostic {
	return []Diagnostic{{Span: sourceSpan, Dialect: dialect, Code: code}}
}

func boundedSourceSpan(source []byte) public.Span {
	if uint64(len(source)) > uint64(^uint32(0)) {
		return public.Span{End: ^uint32(0)}
	}
	return public.Span{End: uint32(len(source))}
}

func span(start, end int) public.Span {
	return public.Span{Start: uint32(start), End: uint32(end)}
}

func sqlSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n' || value == '\f'
}

func asciiIdentifierStart(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value == '_'
}

func asciiIdentifierContinue(value byte) bool {
	return asciiIdentifierStart(value) || value >= '0' && value <= '9'
}
