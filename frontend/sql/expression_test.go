package sql

import (
	"bytes"
	"math"
	"reflect"
	"testing"

	public "github.com/sebishogun/nornrune/frontend"
	internalfrontend "github.com/sebishogun/nornrune/internal/frontend"
)

func TestCompileExpressionLowersSupportedSQLWithPrecedence(t *testing.T) {
	source := []byte(`team = 'blue' OR count >= -2 AND enabled`)
	policy, diagnostics := CompileExpression(source, DialectPostgreSQL, expressionSchema(t, DialectPostgreSQL, nil), public.DefaultLimits())
	if len(diagnostics) != 0 {
		t.Fatalf("CompileExpression() diagnostics = %#v", diagnostics)
	}
	if policy == nil || !bytes.Equal(policy.Source, source) || policy.Default != public.DefaultEscalate {
		t.Fatalf("policy = %#v", policy)
	}
	root := policy.Root - 1
	if policy.NodeKinds[root] != public.NodeKindAny || policy.NodeChildCounts[root] != 2 {
		t.Fatalf("root = kind %v count %d, want two-child OR", policy.NodeKinds[root], policy.NodeChildCounts[root])
	}
	right := policy.ChildNodeIDs[policy.NodeChildStarts[root]+1] - 1
	if policy.NodeKinds[right] != public.NodeKindAll {
		t.Fatalf("right child kind = %v, want AND", policy.NodeKinds[right])
	}
	for _, want := range []struct {
		field public.FieldID
		op    public.CompareOp
	}{
		{field: 1, op: public.CompareOpEqual},
		{field: 2, op: public.CompareOpGreaterEqual},
		{field: 3, op: public.CompareOpEqual},
	} {
		if !sqlHasComparison(policy, want.field, want.op) {
			t.Errorf("missing field %d op %v", want.field, want.op)
		}
	}
	compiled, sharedDiagnostics, err := internalfrontend.Compile(policy)
	if err != nil || len(sharedDiagnostics) != 0 || compiled == nil {
		t.Fatalf("shared Compile() = program %v diagnostics %#v error %v", compiled, sharedDiagnostics, err)
	}
	source[0] = 'X'
	if policy.Source[0] != 't' {
		t.Fatal("policy borrowed source")
	}
}

func TestCompileExpressionAcceptsMinimumIntegerLiteral(t *testing.T) {
	policy, diagnostics := CompileExpression([]byte(`count = -9223372036854775808`), DialectPostgreSQL, expressionSchema(t, DialectPostgreSQL, nil), public.DefaultLimits())
	if len(diagnostics) != 0 {
		t.Fatalf("CompileExpression() diagnostics = %#v", diagnostics)
	}
	if len(policy.IntegerValues) != 1 || policy.IntegerValues[0] != math.MinInt64 {
		t.Fatalf("integer values = %v, want [%d]", policy.IntegerValues, int64(math.MinInt64))
	}
}

func TestCompileExpressionLowersNullDefinednessMembershipAndParameters(t *testing.T) {
	parameters := []Parameter{
		{Name: "$1", Value: public.StringLiteral([]byte("blue"))},
		{Name: "$2", Value: public.IntegerLiteral(7)},
	}
	tests := []struct {
		name     string
		source   string
		rootKind public.NodeKind
		field    public.FieldID
		op       public.CompareOp
	}{
		{name: "is null", source: `team IS NULL`, rootKind: public.NodeKindNot},
		{name: "is not null", source: `team IS NOT NULL`, rootKind: public.NodeKindDefined},
		{name: "in", source: `team IN ('blue', 'green')`, rootKind: public.NodeKindCompare, field: 1, op: public.CompareOpIn},
		{name: "parameter string", source: `team = $1`, rootKind: public.NodeKindCompare, field: 1, op: public.CompareOpEqual},
		{name: "parameter integer", source: `count < $2`, rootKind: public.NodeKindCompare, field: 2, op: public.CompareOpLess},
		{name: "reversed", source: `2 < count`, rootKind: public.NodeKindCompare, field: 2, op: public.CompareOpGreater},
		{name: "boolean", source: `enabled`, rootKind: public.NodeKindCompare, field: 3, op: public.CompareOpEqual},
		{name: "not", source: `NOT enabled`, rootKind: public.NodeKindNot},
		{name: "parentheses", source: `(enabled OR false) AND true`, rootKind: public.NodeKindAll},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, diagnostics := CompileExpression([]byte(test.source), DialectPostgreSQL, expressionSchema(t, DialectPostgreSQL, parameters), public.DefaultLimits())
			if len(diagnostics) != 0 {
				t.Fatalf("CompileExpression() diagnostics = %#v", diagnostics)
			}
			row := policy.Root - 1
			if policy.NodeKinds[row] != test.rootKind {
				t.Fatalf("root kind = %v, want %v", policy.NodeKinds[row], test.rootKind)
			}
			if test.field != 0 && (policy.NodeFields[row] != test.field || policy.NodeOps[row] != test.op) {
				t.Fatalf("comparison = field %d op %v, want field %d op %v", policy.NodeFields[row], policy.NodeOps[row], test.field, test.op)
			}
		})
	}
}

func TestCompileExpressionConsumesQuestionParametersInOrder(t *testing.T) {
	parameters := []Parameter{
		{Name: "?", Value: public.IntegerLiteral(7)},
		{Name: "?", Value: public.IntegerLiteral(9)},
	}
	for _, dialect := range []Dialect{DialectSnowflake, DialectDatabricks} {
		t.Run(dialect.String(), func(t *testing.T) {
			policy, diagnostics := CompileExpression(
				[]byte(`count IN (?, ?)`), dialect, expressionSchema(t, dialect, parameters), public.DefaultLimits(),
			)
			if len(diagnostics) != 0 || policy == nil {
				t.Fatalf("CompileExpression() = policy %#v diagnostics %#v", policy, diagnostics)
			}
			if got, want := policy.IntegerValues, []int64{7, 9}; !reflect.DeepEqual(got, want) {
				t.Fatalf("integer values = %v, want %v", got, want)
			}
		})
	}
}

func TestCompileExpressionRejectsMissingQuestionParameterByOccurrence(t *testing.T) {
	parameters := []Parameter{{Name: "?", Value: public.IntegerLiteral(7)}}
	policy, diagnostics := CompileExpression(
		[]byte(`count IN (?, ?)`), DialectSnowflake,
		expressionSchema(t, DialectSnowflake, parameters), public.DefaultLimits(),
	)
	if policy != nil {
		t.Fatalf("CompileExpression() policy = %#v, want nil", policy)
	}
	wantSpan := public.Span{Start: 13, End: 14}
	if len(diagnostics) != 1 || diagnostics[0].Code != public.CodeInvalidBinding || diagnostics[0].Span != wantSpan {
		t.Fatalf("diagnostics = %#v, want invalid binding at %#v", diagnostics, wantSpan)
	}
}

func TestCompileExpressionLowersBooleanKeywordLiteralsAsScalars(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		operation  public.CompareOp
		booleans   []uint8
		listLength uint16
	}{
		{name: "right literal", source: `enabled = TRUE`, operation: public.CompareOpEqual, booleans: []uint8{1}},
		{name: "left literal", source: `FALSE <> enabled`, operation: public.CompareOpNotEqual, booleans: []uint8{0}},
		{name: "membership", source: `enabled IN (TRUE, FALSE)`, operation: public.CompareOpIn, booleans: []uint8{1, 0}, listLength: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, diagnostics := CompileExpression(
				[]byte(test.source), DialectPostgreSQL,
				expressionSchema(t, DialectPostgreSQL, nil), public.DefaultLimits(),
			)
			if len(diagnostics) != 0 {
				t.Fatalf("CompileExpression() diagnostics = %#v", diagnostics)
			}
			root := policy.Root - 1
			if policy.NodeKinds[root] != public.NodeKindCompare || policy.NodeFields[root] != 3 || policy.NodeOps[root] != test.operation {
				t.Fatalf("root comparison = kind %v field %d op %v", policy.NodeKinds[root], policy.NodeFields[root], policy.NodeOps[root])
			}
			if !bytes.Equal(policy.BooleanValues, test.booleans) || policy.NodeListCounts[root] != test.listLength {
				t.Fatalf("Boolean values = %v list count = %d, want %v/%d", policy.BooleanValues, policy.NodeListCounts[root], test.booleans, test.listLength)
			}
			for _, kind := range policy.LiteralKinds {
				if kind != public.ValueKindBoolean {
					t.Fatalf("literal kind = %v, want Boolean", kind)
				}
			}
		})
	}
}

func TestCompileExpressionReturnsExactDiagnostics(t *testing.T) {
	tests := []struct {
		name   string
		source string
		code   public.DiagnosticCode
		start  uint32
		end    uint32
	}{
		{name: "unknown", source: `missing = 1`, code: public.CodeUnknownField, start: 0, end: 7},
		{name: "type", source: `team = 1`, code: public.CodeType, start: 0, end: 8},
		{name: "field comparison", source: `team = sql_role`, code: public.CodeUnsupported, start: 0, end: 15},
		{name: "null comparison", source: `team = NULL`, code: public.CodeUnsupported, start: 7, end: 11},
		{name: "not in", source: `team NOT IN ('x')`, code: public.CodeUnsupported, start: 5, end: 8},
		{name: "empty in", source: `team IN ()`, code: public.CodeSyntax, start: 9, end: 10},
		{name: "function", source: `lower(team) = 'x'`, code: public.CodeUnsupported, start: 0, end: 5},
		{name: "cast", source: `team::text = 'x'`, code: public.CodeUnsupported, start: 4, end: 5},
		{name: "unterminated after backslash data", source: `team = 'a\''`, code: public.CodeSyntax, start: 7, end: 12},
		{name: "statement terminator", source: `enabled;`, code: public.CodeSyntax, start: 7, end: 8},
		{name: "trailing", source: `enabled junk`, code: public.CodeSyntax, start: 8, end: 12},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, diagnostics := CompileExpression([]byte(test.source), DialectPostgreSQL, expressionSchema(t, DialectPostgreSQL, nil), public.DefaultLimits())
			if policy != nil {
				t.Fatalf("CompileExpression() policy = %#v", policy)
			}
			wantSpan := public.Span{Start: test.start, End: test.end}
			if len(diagnostics) != 1 || diagnostics[0].Code != test.code || diagnostics[0].Span != wantSpan || diagnostics[0].Dialect != DialectPostgreSQL {
				t.Fatalf("diagnostics = %#v, want code %v span %#v", diagnostics, test.code, wantSpan)
			}
		})
	}
}

func TestCompileExpressionRejectsMismatchedProfileAndLimits(t *testing.T) {
	schema := expressionSchema(t, DialectSnowflake, nil)
	if policy, diagnostics := CompileExpression([]byte("TRUE"), DialectPostgreSQL, schema, public.DefaultLimits()); policy != nil || len(diagnostics) != 1 || diagnostics[0].Code != public.CodeInvalidBinding {
		t.Fatalf("profile mismatch = policy %#v diagnostics %#v", policy, diagnostics)
	}
	limits := public.DefaultLimits()
	limits.MaxDepth = 2
	if policy, diagnostics := CompileExpression([]byte("NOT NOT NOT enabled"), DialectPostgreSQL, expressionSchema(t, DialectPostgreSQL, nil), limits); policy != nil || len(diagnostics) != 1 || diagnostics[0].Code != public.CodeLimit {
		t.Fatalf("depth limit = policy %#v diagnostics %#v", policy, diagnostics)
	}
}

func TestCompileExpressionPreservesCompositeSourceSpans(t *testing.T) {
	source := []byte(`NOT (enabled OR count > 2)`)
	policy, diagnostics := CompileExpression(source, DialectPostgreSQL, expressionSchema(t, DialectPostgreSQL, nil), public.DefaultLimits())
	if len(diagnostics) != 0 {
		t.Fatalf("CompileExpression() diagnostics = %#v", diagnostics)
	}
	root := policy.Root - 1
	if got, want := (public.Span{Start: policy.NodeSourceStarts[root], End: policy.NodeSourceEnds[root]}), (public.Span{Start: 0, End: uint32(len(source))}); got != want {
		t.Fatalf("root span = %#v, want %#v", got, want)
	}
}

func TestCompileExpressionKeepsLiteralSeparateFromQuotedIdentifierScratch(t *testing.T) {
	bindings := public.BindingSet{
		Name: "quoted", Version: "v1",
		Fields: []public.Binding{{Source: "Team", Target: "subject.team", Kind: public.ValueKindString, Group: public.FieldGroupSubject}},
	}
	schema, err := NewSchema(DialectPostgreSQL, bindings, nil, "", "", public.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	policy, diagnostics := CompileExpression([]byte(`'blue' = "Team"`), DialectPostgreSQL, schema, public.DefaultLimits())
	if len(diagnostics) != 0 {
		t.Fatalf("CompileExpression() diagnostics = %#v", diagnostics)
	}
	if got := string(policy.SymbolBytes); got != "blue" {
		t.Fatalf("literal bytes = %q, want blue", got)
	}
}

func expressionSchema(t *testing.T, dialect Dialect, parameters []Parameter) Schema {
	t.Helper()
	bindings := public.BindingSet{
		Name: "sql-expression", Version: "v1",
		Fields: []public.Binding{
			{Source: foldedName(dialect, "team"), Target: "subject.team", Kind: public.ValueKindString, Group: public.FieldGroupSubject},
			{Source: foldedName(dialect, "count"), Target: "context.count", Kind: public.ValueKindInteger, Group: public.FieldGroupContext},
			{Source: foldedName(dialect, "enabled"), Target: "context.enabled", Kind: public.ValueKindBoolean, Group: public.FieldGroupContext},
			{Source: foldedName(dialect, "sql_role"), Target: "context.sql_role", Kind: public.ValueKindString, Group: public.FieldGroupContext},
		},
	}
	schema, err := NewSchema(dialect, bindings, parameters, "", foldedName(dialect, "sql_role"), public.DefaultLimits())
	if err != nil {
		t.Fatalf("NewSchema() error = %v", err)
	}
	return schema
}

func foldedName(dialect Dialect, value string) string {
	if dialect == DialectSnowflake {
		result := []byte(value)
		for row := range result {
			if result[row] >= 'a' && result[row] <= 'z' {
				result[row] -= 'a' - 'A'
			}
		}
		return string(result)
	}
	return value
}

func sqlHasComparison(policy *public.Policy, field public.FieldID, operation public.CompareOp) bool {
	for row := range policy.NodeKinds {
		if policy.NodeKinds[row] == public.NodeKindCompare && policy.NodeFields[row] == field && policy.NodeOps[row] == operation {
			return true
		}
	}
	return false
}
