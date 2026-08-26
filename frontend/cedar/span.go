package cedar

import (
	"bytes"
	"unicode"
	"unicode/utf8"

	public "github.com/sebishogun/verifoxx/frontend"
)

type token struct {
	text       string
	start, end uint32
}

type conditionSyntax struct {
	span       public.Span
	tokenStart int
	tokenEnd   int
}

type policySyntax struct {
	span       public.Span
	scopeSpans [3]public.Span
	conditions []conditionSyntax
}

type syntaxKind uint8

const maxSafeParserDepth = 4096

const (
	syntaxInvalid syntaxKind = iota
	syntaxPath
	syntaxLiteral
	syntaxNot
	syntaxAnd
	syntaxOr
	syntaxCompare
)

type syntaxExpr struct {
	left, right *syntaxExpr
	path        string
	op          string
	span        public.Span
	kind        syntaxKind
}

func lex(source []byte, maxDepth uint32) ([]token, public.Diagnostic) {
	tokens := make([]token, 0, len(source)/3+1)
	sourceText := string(source)
	depth := uint64(0)
	semanticDepth := min(uint64(maxDepth), uint64(maxSafeParserDepth))
	depthLimit := semanticDepth + 1 // one policy scope or condition delimiter
	recursiveTokens := uint32(0)
	for offset := 0; offset < len(source); {
		switch source[offset] {
		case ' ', '\t', '\r', '\n':
			offset++
			continue
		case '/':
			if offset+1 < len(source) && source[offset+1] == '/' {
				offset += 2
				for offset < len(source) && source[offset] != '\n' {
					offset++
				}
				continue
			}
			if offset+1 < len(source) && source[offset+1] == '*' {
				start := offset
				offset += 2
				for offset+1 < len(source) && (source[offset] != '*' || source[offset+1] != '/') {
					offset++
				}
				if offset+1 >= len(source) {
					return nil, public.Diagnostic{Span: public.Span{Start: uint32(start), End: uint32(len(source))}, Code: public.CodeSyntax}
				}
				offset += 2
				continue
			}
		case '"':
			start := offset
			offset++
			escaped := false
			for offset < len(source) {
				if source[offset] == '"' && !escaped {
					offset++
					tokens = append(tokens, token{text: sourceText[start:offset], start: uint32(start), end: uint32(offset)})
					break
				}
				if source[offset] == '\\' && !escaped {
					escaped = true
				} else {
					escaped = false
				}
				offset++
			}
			if len(tokens) == 0 || tokens[len(tokens)-1].start != uint32(start) {
				return nil, public.Diagnostic{Span: public.Span{Start: uint32(start), End: uint32(len(source))}, Code: public.CodeSyntax}
			}
			continue
		}

		start := offset
		r, size := utf8.DecodeRune(source[offset:])
		if unicode.IsLetter(r) || r == '_' {
			offset += size
			for offset < len(source) {
				r, size = utf8.DecodeRune(source[offset:])
				if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
					break
				}
				offset += size
			}
		} else if r >= '0' && r <= '9' {
			offset += size
			for offset < len(source) && source[offset] >= '0' && source[offset] <= '9' {
				offset++
			}
		} else {
			offset += size
			if offset < len(source) {
				pair := source[start : offset+1]
				if bytes.Equal(pair, []byte("==")) || bytes.Equal(pair, []byte("!=")) ||
					bytes.Equal(pair, []byte("<=")) || bytes.Equal(pair, []byte(">=")) ||
					bytes.Equal(pair, []byte("&&")) || bytes.Equal(pair, []byte("||")) ||
					bytes.Equal(pair, []byte("::")) {
					offset++
				}
			}
		}
		text := sourceText[start:offset]
		switch text {
		case "(", "{", "[":
			depth++
			if depth > depthLimit {
				return nil, public.Diagnostic{Span: public.Span{Start: uint32(start), End: uint32(offset)}, Code: public.CodeLimit}
			}
		case ")", "}", "]":
			if depth > 0 {
				depth--
			}
		case "if":
			recursiveTokens++
			if recursiveTokens > maxSafeParserDepth {
				return nil, public.Diagnostic{Span: public.Span{Start: uint32(start), End: uint32(offset)}, Code: public.CodeLimit}
			}
		}
		tokens = append(tokens, token{text: text, start: uint32(start), end: uint32(offset)})
	}
	return tokens, public.Diagnostic{}
}

func scanPolicySyntax(tokens []token, offsets []uint32) ([]policySyntax, bool) {
	result := make([]policySyntax, 0, len(offsets))
	cursor := 0
	for _, offset := range offsets {
		for cursor < len(tokens) && tokens[cursor].start < offset {
			cursor++
		}
		start := cursor
		for cursor < len(tokens) && tokens[cursor].text != "permit" && tokens[cursor].text != "forbid" {
			cursor++
		}
		if cursor >= len(tokens) || cursor+1 >= len(tokens) || tokens[cursor+1].text != "(" {
			return nil, false
		}
		var policy policySyntax
		if start < len(tokens) {
			policy.span.Start = tokens[start].start
		}
		cursor += 2
		for scope := 0; scope < 3; scope++ {
			scopeStart := cursor
			paren, brace, bracket := 0, 0, 0
			for cursor < len(tokens) {
				text := tokens[cursor].text
				if paren == 0 && brace == 0 && bracket == 0 && (text == "," || text == ")") {
					break
				}
				switch text {
				case "(":
					paren++
				case ")":
					paren--
				case "{":
					brace++
				case "}":
					brace--
				case "[":
					bracket++
				case "]":
					bracket--
				}
				cursor++
			}
			if scopeStart == cursor || cursor >= len(tokens) {
				return nil, false
			}
			policy.scopeSpans[scope] = public.Span{Start: tokens[scopeStart].start, End: tokens[cursor-1].end}
			if scope < 2 && tokens[cursor].text != "," {
				return nil, false
			}
			if scope == 2 && tokens[cursor].text == "," {
				cursor++
			}
			if scope == 2 && (cursor >= len(tokens) || tokens[cursor].text != ")") {
				return nil, false
			}
			cursor++
		}
		for cursor < len(tokens) && tokens[cursor].text != ";" {
			unless := tokens[cursor].text == "unless"
			if !unless && tokens[cursor].text != "when" {
				return nil, false
			}
			cursor++
			if cursor >= len(tokens) || tokens[cursor].text != "{" {
				return nil, false
			}
			cursor++
			bodyStart := cursor
			depth := 1
			for cursor < len(tokens) && depth != 0 {
				switch tokens[cursor].text {
				case "{":
					depth++
				case "}":
					depth--
				}
				if depth != 0 {
					cursor++
				}
			}
			if cursor >= len(tokens) || bodyStart == cursor {
				return nil, false
			}
			policy.conditions = append(policy.conditions, conditionSyntax{
				span:       public.Span{Start: tokens[bodyStart].start, End: tokens[cursor-1].end},
				tokenStart: bodyStart, tokenEnd: cursor,
			})
			cursor++
		}
		if cursor >= len(tokens) || tokens[cursor].text != ";" {
			return nil, false
		}
		policy.span.End = tokens[cursor].end
		cursor++
		result = append(result, policy)
	}
	return result, len(result) == len(offsets)
}

type expressionParser struct {
	tokens []token
	row    int
	end    int
}

func parseExpression(tokens []token, start, end int) (*syntaxExpr, bool) {
	parser := expressionParser{tokens: tokens, row: start, end: end}
	expr, ok := parser.parseOr()
	return expr, ok && parser.row == end
}

func (parser *expressionParser) parseOr() (*syntaxExpr, bool) {
	return parser.parseBinary(parser.parseAnd, "||", syntaxOr)
}

func (parser *expressionParser) parseAnd() (*syntaxExpr, bool) {
	return parser.parseBinary(parser.parseCompare, "&&", syntaxAnd)
}

func (parser *expressionParser) parseBinary(next func() (*syntaxExpr, bool), operator string, kind syntaxKind) (*syntaxExpr, bool) {
	left, ok := next()
	if !ok {
		return nil, false
	}
	for parser.row < parser.end && parser.tokens[parser.row].text == operator {
		parser.row++
		right, rightOK := next()
		if !rightOK {
			return nil, false
		}
		left = &syntaxExpr{left: left, right: right, op: operator, kind: kind, span: public.Span{Start: left.span.Start, End: right.span.End}}
	}
	return left, true
}

func (parser *expressionParser) parseCompare() (*syntaxExpr, bool) {
	left, ok := parser.parseUnary()
	if !ok || parser.row >= parser.end {
		return left, ok
	}
	operator := parser.tokens[parser.row].text
	switch operator {
	case "==", "!=", "<", "<=", ">", ">=":
	default:
		return left, true
	}
	parser.row++
	right, ok := parser.parseUnary()
	if !ok {
		return nil, false
	}
	return &syntaxExpr{left: left, right: right, op: operator, kind: syntaxCompare, span: public.Span{Start: left.span.Start, End: right.span.End}}, true
}

func (parser *expressionParser) parseUnary() (*syntaxExpr, bool) {
	if parser.row < parser.end && parser.tokens[parser.row].text == "!" {
		start := parser.tokens[parser.row].start
		parser.row++
		child, ok := parser.parseUnary()
		if !ok {
			return nil, false
		}
		return &syntaxExpr{left: child, op: "!", kind: syntaxNot, span: public.Span{Start: start, End: child.span.End}}, true
	}
	return parser.parsePrimary()
}

func (parser *expressionParser) parsePrimary() (*syntaxExpr, bool) {
	if parser.row >= parser.end {
		return nil, false
	}
	current := parser.tokens[parser.row]
	if current.text == "(" {
		start := current.start
		parser.row++
		expr, ok := parser.parseOr()
		if !ok || parser.row >= parser.end || parser.tokens[parser.row].text != ")" {
			return nil, false
		}
		expr.span = public.Span{Start: start, End: parser.tokens[parser.row].end}
		parser.row++
		return expr, true
	}
	if current.text == "-" && parser.row+1 < parser.end {
		next := parser.tokens[parser.row+1]
		if len(next.text) != 0 && next.text[0] >= '0' && next.text[0] <= '9' {
			parser.row += 2
			return &syntaxExpr{kind: syntaxLiteral, span: public.Span{Start: current.start, End: next.end}}, true
		}
	}
	if current.text == "true" || current.text == "false" || (len(current.text) != 0 && (current.text[0] == '"' || current.text[0] >= '0' && current.text[0] <= '9')) {
		parser.row++
		return &syntaxExpr{kind: syntaxLiteral, span: public.Span{Start: current.start, End: current.end}}, true
	}
	if len(current.text) == 0 || !identifierStart(current.text) {
		return nil, false
	}
	start := current.start
	path := current.text
	end := current.end
	parser.row++
	for parser.row+1 < parser.end && parser.tokens[parser.row].text == "." && identifierStart(parser.tokens[parser.row+1].text) {
		path += "." + parser.tokens[parser.row+1].text
		end = parser.tokens[parser.row+1].end
		parser.row += 2
	}
	return &syntaxExpr{path: path, kind: syntaxPath, span: public.Span{Start: start, End: end}}, true
}

func identifierStart(text string) bool {
	if text == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(text)
	return unicode.IsLetter(r) || r == '_'
}

func syntaxSpan(expr *syntaxExpr) public.Span {
	if expr == nil {
		return public.Span{}
	}
	return expr.span
}

func parserErrorSpan(source []byte, message string) public.Span {
	line, column := parseErrorPosition(message)
	if line == 0 || column == 0 {
		return public.Span{End: uint32(min(len(source), 1))}
	}
	offset := 0
	currentLine := 1
	for offset < len(source) && currentLine < line {
		if source[offset] == '\n' {
			currentLine++
		}
		offset++
	}
	currentColumn := 1
	for offset < len(source) && currentColumn < column && source[offset] != '\n' {
		_, size := utf8.DecodeRune(source[offset:])
		offset += size
		currentColumn++
	}
	end := offset
	if end < len(source) {
		_, size := utf8.DecodeRune(source[end:])
		end += size
	}
	return public.Span{Start: uint32(offset), End: uint32(end)}
}

func parseErrorPosition(message string) (int, int) {
	marker := []byte("<input>:")
	index := bytes.Index([]byte(message), marker)
	if index < 0 {
		return 0, 0
	}
	index += len(marker)
	line, next := decimalAt(message, index)
	if next >= len(message) || message[next] != ':' {
		return 0, 0
	}
	column, _ := decimalAt(message, next+1)
	return line, column
}

func decimalAt(value string, offset int) (int, int) {
	result := 0
	start := offset
	for offset < len(value) && value[offset] >= '0' && value[offset] <= '9' {
		result = result*10 + int(value[offset]-'0')
		offset++
	}
	if offset == start {
		return 0, offset
	}
	return result, offset
}
