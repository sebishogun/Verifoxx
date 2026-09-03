package sql

import public "github.com/sebishogun/nornrune/frontend"

// CompileExpression parses and lowers one bounded scalar SQL expression into
// the shared semantic Policy.
func CompileExpression(source []byte, dialect Dialect, schema Schema, limits public.Limits) (*public.Policy, []Diagnostic) {
	if dialect != schema.Dialect || schema.Validate(limits) != nil {
		return nil, oneDiagnostic(dialect, public.CodeInvalidBinding, public.Span{})
	}
	tokens, diagnostics := Lex(source, dialect, limits)
	if len(diagnostics) != 0 {
		return nil, diagnostics
	}
	builder, err := public.NewBuilder(tokens.Source, schema.Bindings, limits)
	if err != nil {
		return nil, oneDiagnostic(dialect, expressionBuilderCode(err), public.Span{})
	}
	parser := expressionParser{
		tokens:          tokens,
		schema:          &schema,
		limits:          limits,
		builder:         builder,
		literalBytes:    make([]byte, 0, min(len(source), int(limits.MaxStringBytes))),
		identifierBytes: make([]byte, 0, min(len(source), 256)),
		nodeSpans:       make([]public.Span, 0, min(len(tokens.Kinds), 256)),
		list:            make([]public.Literal, 0, min(len(tokens.Kinds), 32)),
	}
	root := parser.parseOr()
	if !parser.failed() && parser.current() != TokenEOF {
		parser.fail(public.CodeSyntax, parser.currentSpan())
	}
	if parser.failed() {
		return nil, parser.diagnostics
	}
	if root == 0 {
		return nil, oneDiagnostic(dialect, public.CodeInvalidPolicy, public.Span{})
	}
	policy, err := builder.Finish(root, public.DefaultEscalate)
	if err != nil {
		return nil, oneDiagnostic(dialect, expressionBuilderCode(err), parser.nodeSpan(root))
	}
	return policy, nil
}
