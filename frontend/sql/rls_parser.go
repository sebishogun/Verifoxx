package sql

import (
	"bytes"
	"math"

	public "github.com/sebishogun/nornrune/frontend"
)

// CompilePostgreSQLRLS parses and composes PostgreSQL CREATE POLICY statements
// into one shared semantic Policy with DefaultReject.
func CompilePostgreSQLRLS(source []byte, schema Schema, limits public.Limits) (*RLS, []Diagnostic) {
	if schema.Dialect != DialectPostgreSQL || schema.CommandField == "" || schema.RoleField == "" || schema.Validate(limits) != nil {
		return nil, oneDiagnostic(DialectPostgreSQL, public.CodeInvalidBinding, public.Span{})
	}
	tokens, diagnostics := Lex(source, DialectPostgreSQL, limits)
	if len(diagnostics) != 0 {
		return nil, diagnostics
	}
	builder, err := public.NewBuilder(tokens.Source, schema.Bindings, limits)
	if err != nil {
		return nil, oneDiagnostic(DialectPostgreSQL, expressionBuilderCode(err), public.Span{})
	}
	expressions := expressionParser{
		tokens: tokens, schema: &schema, limits: limits, builder: builder,
		literalBytes:    make([]byte, 0, min(len(source), int(limits.MaxStringBytes))),
		identifierBytes: make([]byte, 0, min(len(source), 256)),
		nodeSpans:       make([]public.Span, 0, min(len(tokens.Kinds), 256)),
		list:            make([]public.Literal, 0, min(len(tokens.Kinds), 32)),
	}
	result := &RLS{
		Modes: make([]PolicyMode, 0, 8), Commands: make([]PolicyCommand, 0, 8),
		UsingRoots: make([]public.NodeID, 0, 8), CheckRoots: make([]public.NodeID, 0, 8),
		RoleStarts: make([]uint32, 0, 8), RoleCounts: make([]uint16, 0, 8),
		PolicySpans: make([]public.Span, 0, 8), NameStarts: make([]uint32, 0, 8),
		NameLengths: make([]uint32, 0, 8), RoleNameStarts: make([]uint32, 0, 16),
		RoleNameLengths: make([]uint32, 0, 16), NameBytes: make([]byte, 0, 128),
		RoleBytes: make([]byte, 0, 128),
	}
	for expressions.current() != TokenEOF && !expressions.failed() {
		parseRLSStatement(&expressions, result)
	}
	if expressions.failed() {
		return nil, expressions.diagnostics
	}
	if len(result.Modes) == 0 {
		return nil, oneDiagnostic(DialectPostgreSQL, public.CodeSyntax, public.Span{})
	}
	root := composeRLS(&expressions, result)
	if expressions.failed() {
		return nil, expressions.diagnostics
	}
	semantic, err := builder.Finish(root, public.DefaultReject)
	if err != nil {
		return nil, oneDiagnostic(DialectPostgreSQL, expressionBuilderCode(err), expressions.nodeSpan(root))
	}
	result.Semantic = semantic
	return result, nil
}

func parseRLSStatement(parser *expressionParser, result *RLS) {
	statementStart := parser.currentSpan().Start
	if parser.current() != TokenCreate {
		parser.fail(public.CodeUnsupported, parser.currentSpan())
		return
	}
	parser.position++
	if !expectToken(parser, TokenPolicy) {
		return
	}
	name, nameSpan, ok := parseRLSIdentifier(parser)
	if !ok {
		return
	}
	if duplicateRLSName(result, name) {
		parser.fail(public.CodeDuplicate, nameSpan)
		return
	}
	if !expectToken(parser, TokenOn) {
		return
	}
	table, tableSpan, ok := parseRLSIdentifier(parser)
	if !ok {
		return
	}
	if parser.current() == TokenDot {
		parser.fail(public.CodeUnsupported, parser.currentSpan())
		return
	}
	if len(result.Table) == 0 {
		result.Table = append(result.Table, table...)
	} else if !bytes.Equal(result.Table, table) {
		parser.fail(public.CodeInvalidPolicy, tableSpan)
		return
	}

	mode := PolicyModePermissive
	command := PolicyCommandAll
	if parser.current() == TokenAs {
		parser.position++
		switch parser.current() {
		case TokenPermissive:
			mode = PolicyModePermissive
		case TokenRestrictive:
			mode = PolicyModeRestrictive
		default:
			parser.fail(public.CodeSyntax, parser.currentSpan())
			return
		}
		parser.position++
	}
	if parser.current() == TokenFor {
		parser.position++
		command = parsePolicyCommand(parser)
		if command == PolicyCommandInvalid {
			return
		}
	}

	roleStart := len(result.RoleNameStarts)
	if parser.current() == TokenTo {
		parser.position++
		for {
			role, _, roleOK := parseRLSRole(parser)
			if !roleOK {
				return
			}
			appendRLSRole(result, role)
			if parser.current() != TokenComma {
				break
			}
			parser.position++
		}
	} else {
		appendRLSRole(result, []byte("public"))
	}
	roleCount := len(result.RoleNameStarts) - roleStart
	if roleCount == 0 || roleCount > math.MaxUint16 || uint64(len(result.RoleNameStarts)) > uint64(parser.limits.MaxChildren) {
		parser.fail(public.CodeLimit, parser.currentSpan())
		return
	}

	var usingRoot, checkRoot public.NodeID
	if parser.current() == TokenUsing {
		usingRoot = parseRLSClauseExpression(parser, TokenUsing)
		if parser.failed() {
			return
		}
	}
	if parser.current() == TokenWith {
		checkRoot = parseRLSClauseExpression(parser, TokenWith)
		if parser.failed() {
			return
		}
	}
	if command == PolicyCommandInsert && usingRoot != 0 || (command == PolicyCommandSelect || command == PolicyCommandDelete) && checkRoot != 0 {
		parser.fail(public.CodeUnsupported, public.Span{Start: statementStart, End: parser.currentSpan().Start})
		return
	}
	end := parser.currentSpan().End
	if parser.current() == TokenSemicolon {
		end = parser.currentSpan().End
		parser.position++
	} else if parser.current() != TokenEOF {
		parser.fail(public.CodeUnsupported, parser.currentSpan())
		return
	}
	statementSpan := public.Span{Start: statementStart, End: end}
	if usingRoot == 0 {
		if command == PolicyCommandInsert && checkRoot != 0 {
			usingRoot = checkRoot
		} else {
			usingRoot = addRLSBoolean(parser, true, statementSpan)
		}
	}
	if checkRoot == 0 {
		checkRoot = usingRoot
	}
	if parser.failed() {
		return
	}
	if uint64(len(result.Modes))+1 > uint64(parser.limits.MaxFields) {
		parser.fail(public.CodeLimit, statementSpan)
		return
	}
	result.NameStarts = append(result.NameStarts, uint32(len(result.NameBytes)))
	result.NameLengths = append(result.NameLengths, uint32(len(name)))
	result.NameBytes = append(result.NameBytes, name...)
	result.Modes = append(result.Modes, mode)
	result.Commands = append(result.Commands, command)
	result.UsingRoots = append(result.UsingRoots, usingRoot)
	result.CheckRoots = append(result.CheckRoots, checkRoot)
	result.RoleStarts = append(result.RoleStarts, uint32(roleStart))
	result.RoleCounts = append(result.RoleCounts, uint16(roleCount))
	result.PolicySpans = append(result.PolicySpans, statementSpan)
	if rlsStringBytes(result) > uint64(parser.limits.MaxStringBytes) {
		parser.fail(public.CodeLimit, statementSpan)
	}
}

func parseRLSClauseExpression(parser *expressionParser, clause TokenKind) public.NodeID {
	parser.position++
	if clause == TokenWith {
		if !expectToken(parser, TokenCheck) {
			return 0
		}
	}
	if parser.current() != TokenLParen {
		parser.fail(public.CodeSyntax, parser.currentSpan())
		return 0
	}
	parser.position++
	if !parser.enter() {
		return 0
	}
	root := parser.parseOr()
	parser.leave()
	if parser.failed() {
		return 0
	}
	if parser.current() != TokenRParen {
		parser.fail(public.CodeSyntax, parser.currentSpan())
		return 0
	}
	parser.position++
	return root
}

func parsePolicyCommand(parser *expressionParser) PolicyCommand {
	var command PolicyCommand
	switch parser.current() {
	case TokenAll:
		command = PolicyCommandAll
	case TokenSelect:
		command = PolicyCommandSelect
	case TokenInsert:
		command = PolicyCommandInsert
	case TokenUpdate:
		command = PolicyCommandUpdate
	case TokenDelete:
		command = PolicyCommandDelete
	default:
		parser.fail(public.CodeUnsupported, parser.currentSpan())
		return PolicyCommandInvalid
	}
	parser.position++
	return command
}

func parseRLSIdentifier(parser *expressionParser) ([]byte, public.Span, bool) {
	if parser.current() != TokenIdentifier {
		parser.fail(public.CodeSyntax, parser.currentSpan())
		return nil, public.Span{}, false
	}
	row := parser.position
	parser.position++
	value := parser.tokenBytes(row)
	span := public.Span{Start: parser.tokens.Starts[row], End: parser.tokens.Ends[row]}
	return normalizedPostgreSQLIdentifier(value), span, true
}

func parseRLSRole(parser *expressionParser) ([]byte, public.Span, bool) {
	if parser.current() == TokenPublic {
		span := parser.currentSpan()
		parser.position++
		return []byte("public"), span, true
	}
	return parseRLSIdentifier(parser)
}

func normalizedPostgreSQLIdentifier(token []byte) []byte {
	if len(token) >= 2 && token[0] == '"' {
		return appendDecodedQuote(nil, token)
	}
	result := append([]byte(nil), token...)
	for row := range result {
		if result[row] >= 'A' && result[row] <= 'Z' {
			result[row] += 'a' - 'A'
		}
	}
	return result
}

func appendRLSRole(result *RLS, role []byte) {
	result.RoleNameStarts = append(result.RoleNameStarts, uint32(len(result.RoleBytes)))
	result.RoleNameLengths = append(result.RoleNameLengths, uint32(len(role)))
	result.RoleBytes = append(result.RoleBytes, role...)
}

func duplicateRLSName(result *RLS, name []byte) bool {
	for row := range result.NameStarts {
		start := result.NameStarts[row]
		end := start + result.NameLengths[row]
		if bytes.Equal(result.NameBytes[start:end], name) {
			return true
		}
	}
	return false
}

func composeRLS(parser *expressionParser, result *RLS) public.NodeID {
	commandField := rlsFieldID(parser.schema.Bindings, parser.schema.CommandField)
	roleField := rlsFieldID(parser.schema.Bindings, parser.schema.RoleField)
	if commandField == 0 || roleField == 0 {
		parser.fail(public.CodeInvalidBinding, public.Span{})
		return 0
	}
	wholeSpan := public.Span{End: uint32(len(parser.tokens.Source))}
	roleRoots := make([]public.NodeID, len(result.Modes))
	for row := range roleRoots {
		roleRoots[row] = composeRLSRole(parser, result, row, roleField, wholeSpan)
		if parser.failed() {
			return 0
		}
	}
	commandRoots := make([]public.NodeID, 0, int(CommandDelete))
	for command := CommandSelect; command <= CommandDelete; command++ {
		permissive := make([]public.NodeID, 0, len(result.Modes))
		restrictive := make([]public.NodeID, 0, len(result.Modes))
		for row, mode := range result.Modes {
			if !policyMatchesRuntimeCommand(result.Commands[row], command) {
				continue
			}
			predicate := result.UsingRoots[row]
			if command == CommandInsert || command == CommandUpdateCheck {
				predicate = result.CheckRoots[row]
			}
			if mode == PolicyModePermissive {
				permissive = append(permissive, addRLSGroup(parser, true, roleRoots[row], predicate, wholeSpan))
			} else {
				notRole := addRLSNot(parser, roleRoots[row], wholeSpan)
				restrictive = append(restrictive, addRLSGroup(parser, false, notRole, predicate, wholeSpan))
			}
		}
		permissiveRoot := foldRLS(parser, permissive, false, false, wholeSpan)
		restrictiveRoot := foldRLS(parser, restrictive, true, true, wholeSpan)
		effective := addRLSGroup(parser, true, permissiveRoot, restrictiveRoot, wholeSpan)
		guard, err := parser.builder.AddCompare(commandField, public.CompareOpEqual, public.StringLiteral([]byte(command.String())), wholeSpan)
		guard = parser.completedNode(guard, err, wholeSpan)
		commandRoots = append(commandRoots, addRLSGroup(parser, true, guard, effective, wholeSpan))
		if parser.failed() {
			return 0
		}
	}
	return foldRLS(parser, commandRoots, false, false, wholeSpan)
}

func composeRLSRole(parser *expressionParser, result *RLS, row int, roleField public.FieldID, sourceSpan public.Span) public.NodeID {
	start := result.RoleStarts[row]
	count := uint32(result.RoleCounts[row])
	roots := make([]public.NodeID, 0, count)
	for offset := uint32(0); offset < count; offset++ {
		roleRow := start + offset
		nameStart := result.RoleNameStarts[roleRow]
		nameEnd := nameStart + result.RoleNameLengths[roleRow]
		name := result.RoleBytes[nameStart:nameEnd]
		if bytes.Equal(name, []byte("public")) {
			return addRLSBoolean(parser, true, sourceSpan)
		}
		node, err := parser.builder.AddCompare(roleField, public.CompareOpEqual, public.StringLiteral(name), sourceSpan)
		roots = append(roots, parser.completedNode(node, err, sourceSpan))
	}
	return foldRLS(parser, roots, false, false, sourceSpan)
}

func foldRLS(parser *expressionParser, roots []public.NodeID, all, emptyValue bool, sourceSpan public.Span) public.NodeID {
	if len(roots) == 0 {
		return addRLSBoolean(parser, emptyValue, sourceSpan)
	}
	root := roots[0]
	for row := 1; row < len(roots); row++ {
		root = addRLSGroup(parser, all, root, roots[row], sourceSpan)
	}
	return root
}

func addRLSGroup(parser *expressionParser, all bool, left, right public.NodeID, sourceSpan public.Span) public.NodeID {
	children := [2]public.NodeID{left, right}
	var (
		node public.NodeID
		err  error
	)
	if all {
		node, err = parser.builder.AddAll(children[:], sourceSpan)
	} else {
		node, err = parser.builder.AddAny(children[:], sourceSpan)
	}
	return parser.completedNode(node, err, sourceSpan)
}

func addRLSNot(parser *expressionParser, child public.NodeID, sourceSpan public.Span) public.NodeID {
	node, err := parser.builder.AddNot(child, sourceSpan)
	return parser.completedNode(node, err, sourceSpan)
}

func addRLSBoolean(parser *expressionParser, value bool, sourceSpan public.Span) public.NodeID {
	node, err := parser.builder.AddBoolean(value, sourceSpan)
	return parser.completedNode(node, err, sourceSpan)
}

func expectToken(parser *expressionParser, kind TokenKind) bool {
	if parser.current() != kind {
		parser.fail(public.CodeSyntax, parser.currentSpan())
		return false
	}
	parser.position++
	return true
}

func policyMatchesRuntimeCommand(policy PolicyCommand, runtime Command) bool {
	if policy == PolicyCommandAll {
		return true
	}
	switch runtime {
	case CommandSelect:
		return policy == PolicyCommandSelect
	case CommandInsert:
		return policy == PolicyCommandInsert
	case CommandUpdateUsing, CommandUpdateCheck:
		return policy == PolicyCommandUpdate
	case CommandDelete:
		return policy == PolicyCommandDelete
	default:
		return false
	}
}

func rlsFieldID(bindings public.BindingSet, name string) public.FieldID {
	for row := range bindings.Fields {
		if bindings.Fields[row].Source == name {
			return public.FieldID(row + 1)
		}
	}
	return 0
}

func rlsStringBytes(result *RLS) uint64 {
	return uint64(len(result.Table)) + uint64(len(result.NameBytes)) + uint64(len(result.RoleBytes))
}
