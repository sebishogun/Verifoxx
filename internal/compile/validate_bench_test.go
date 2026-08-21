package compile

import (
	"fmt"
	"testing"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/schema"
)

// validateBenchmarkSizes are the node/row counts every Task 7.5 benchmark
// runs: 16, 128, 1,024, and 8,192. The two scaling fixtures push the O(N^2)
// predecessor scans hard enough at 8,192 to be measurable.
var validateBenchmarkSizes = [...]int{16, 128, 1024, 8192}

// validateBenchSink consumes the returned diagnostic count of every timed
// validation call so the call cannot be eliminated. Valid fixtures append no
// diagnostics, so the sink stays zero; the store to the package-level
// variable is what keeps each iteration observable.
var validateBenchSink int

// Fixtures share one field schema, one short valid source span over the
// single-byte input "s", and one symbol-named outcome.
var (
	benchmarkSource      = []byte("s")
	benchmarkSpan        = ast.SourceSpan{Start: 0, End: 1}
	benchmarkOutcomeName = []byte("Approve")
)

// benchmarkFieldSchema returns a one-field symbol schema shared by every
// Task 7.5 fixture.
func benchmarkFieldSchema(tb testing.TB) *schema.Schema {
	tb.Helper()
	syms := schema.NewSymbolInterner(8)
	fieldSym, err := syms.Intern([]byte("subject.trust"))
	if err != nil {
		tb.Fatal(err)
	}
	fb := schema.NewBuilder()
	if _, err := fb.AddField(fieldSym, schema.ValueKindSymbol, schema.FieldGroupSubject); err != nil {
		tb.Fatal(err)
	}
	return fb.Finish()
}

// buildLinearFixture builds exactly nodes AST nodes: nodes-1 Exists compares
// and one All group containing all of them. The single clause asserts the
// group with a complete seven-slot resolution to the one outcome and carries
// no evidence or remediation; the single requirement uses the group as its
// applicability root and references the clause, so every node is reachable,
// acyclic, and the document validates to zero diagnostics. Hints size every
// column the fixture touches.
func buildLinearFixture(tb testing.TB, nodes int) (*ast.Document, *schema.Schema) {
	tb.Helper()
	fields := benchmarkFieldSchema(tb)
	ab := ast.NewBuilder(ast.Hints{
		Nodes: nodes, CompareNodes: nodes - 1, GroupNodes: 1, ChildEdges: nodes - 1,
		Values: 1, SymbolValues: 1, SymbolBytes: len(benchmarkOutcomeName),
		Outcomes: 1, Clauses: 1, Requirements: 1, RequirementClauseEdges: 1,
		SourceBytes: len(benchmarkSource),
	})
	if err := ab.SetSource(benchmarkSource); err != nil {
		tb.Fatal(err)
	}
	outName, err := ab.AddSymbolValue(benchmarkOutcomeName)
	if err != nil {
		tb.Fatal(err)
	}
	outcome, err := ab.AddOutcome(outName, 1, true, benchmarkSpan)
	if err != nil {
		tb.Fatal(err)
	}
	children := make([]schema.NodeID, nodes-1)
	for i := range children {
		id, err := ab.AddExists(schema.FieldID(1), benchmarkSpan)
		if err != nil {
			tb.Fatal(err)
		}
		children[i] = id
	}
	group, err := ab.AddGroup(ast.NodeKindAll, children, benchmarkSpan)
	if err != nil {
		tb.Fatal(err)
	}
	resolution := ast.Resolution{
		OnSatisfied: outcome, OnFalse: outcome, OnMissing: outcome, OnStale: outcome,
		OnUnclear: outcome, OnUnverifiable: outcome, OnConflict: outcome,
	}
	clause, err := ab.AddClause(group, nil, resolution, nil, benchmarkSpan)
	if err != nil {
		tb.Fatal(err)
	}
	if err := ab.AddRequirement(schema.RequirementID(1), group, []schema.ClauseID{clause}, benchmarkSpan); err != nil {
		tb.Fatal(err)
	}
	return ab.Document(), fields
}

// catalogNameLen fixes every evidence-kind name at 8 bytes; the last two
// bytes encode the row index, so the full predecessor scan compares distinct
// equal-length symbols.
const catalogNameLen = 8

// writeCatalogName fills dst (catalogNameLen bytes) with the i-th distinct
// evidence-kind name.
func writeCatalogName(dst []byte, i int) {
	dst[0] = 'e'
	dst[1] = 'v'
	dst[2] = 'i'
	dst[3] = 'd'
	dst[4] = 'e'
	dst[5] = 'n'
	dst[6] = byte(i >> 8)
	dst[7] = byte(i)
}

// buildCatalogFixture builds a valid document with kinds evidence-kind rows
// only, each named by a distinct fixed-size symbol, so
// checkCatalogNameSemantics runs the full O(kinds^2) predecessor scan and
// emits nothing. No nodes, clauses, or requirements are present.
func buildCatalogFixture(tb testing.TB, kinds int) (*ast.Document, *schema.Schema) {
	tb.Helper()
	fields := benchmarkFieldSchema(tb)
	ab := ast.NewBuilder(ast.Hints{
		Values: kinds, SymbolValues: kinds, SymbolBytes: kinds * catalogNameLen,
		EvidenceKinds: kinds, SourceBytes: len(benchmarkSource),
	})
	if err := ab.SetSource(benchmarkSource); err != nil {
		tb.Fatal(err)
	}
	name := make([]byte, catalogNameLen)
	for i := 0; i < kinds; i++ {
		writeCatalogName(name, i)
		valueID, err := ab.AddSymbolValue(name)
		if err != nil {
			tb.Fatal(err)
		}
		if _, err := ab.AddEvidenceKind(valueID, benchmarkSpan); err != nil {
			tb.Fatal(err)
		}
	}
	return ab.Document(), fields
}

// buildRequirementFixture builds a valid document with reqs requirements,
// each with a unique nonzero ID referencing the same Exists applicability
// root and the same complete clause, so checkRequirementSemantics runs the
// full O(reqs^2) predecessor scan and emits nothing. The requirement rows and
// the shared clause CSR edges are pre-sized.
func buildRequirementFixture(tb testing.TB, reqs int) (*ast.Document, *schema.Schema) {
	tb.Helper()
	fields := benchmarkFieldSchema(tb)
	ab := ast.NewBuilder(ast.Hints{
		Nodes: 1, CompareNodes: 1,
		Values: 1, SymbolValues: 1, SymbolBytes: len(benchmarkOutcomeName),
		Outcomes: 1, Clauses: 1,
		Requirements: reqs, RequirementClauseEdges: reqs,
		SourceBytes: len(benchmarkSource),
	})
	if err := ab.SetSource(benchmarkSource); err != nil {
		tb.Fatal(err)
	}
	outName, err := ab.AddSymbolValue(benchmarkOutcomeName)
	if err != nil {
		tb.Fatal(err)
	}
	outcome, err := ab.AddOutcome(outName, 1, true, benchmarkSpan)
	if err != nil {
		tb.Fatal(err)
	}
	exists, err := ab.AddExists(schema.FieldID(1), benchmarkSpan)
	if err != nil {
		tb.Fatal(err)
	}
	resolution := ast.Resolution{
		OnSatisfied: outcome, OnFalse: outcome, OnMissing: outcome, OnStale: outcome,
		OnUnclear: outcome, OnUnverifiable: outcome, OnConflict: outcome,
	}
	clause, err := ab.AddClause(exists, nil, resolution, nil, benchmarkSpan)
	if err != nil {
		tb.Fatal(err)
	}
	for i := 1; i <= reqs; i++ {
		if err := ab.AddRequirement(schema.RequirementID(i), exists, []schema.ClauseID{clause}, benchmarkSpan); err != nil {
			tb.Fatal(err)
		}
	}
	return ab.Document(), fields
}

// primeValidator validates doc once with v and a pre-sized destination,
// failing the benchmark if the fixture emits any diagnostic, so every timed
// iteration validates a clean document with retained state and destination
// capacity.
func primeValidator(b *testing.B, v *Validator, doc *ast.Document, fields *schema.Schema, dst []Diagnostic) {
	b.Helper()
	if diags := v.Validate(dst[:0], doc, fields); len(diags) != 0 {
		b.Fatalf("prime validation emitted %d diagnostics", len(diags))
	}
}

// reportPerNode reports nanoseconds per AST node for the linear fixture.
func reportPerNode(b *testing.B, nodes int) {
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/(float64(b.N)*float64(nodes)), "ns/node")
}

// reportPerItem reports nanoseconds per row/item for the scaling fixtures.
func reportPerItem(b *testing.B, items int) {
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/(float64(b.N)*float64(items)), "ns/item")
}

// reportPerPair reports nanoseconds per unordered predecessor comparison, the
// dominant cost of the unique-ID/unique-name scans: n*(n-1)/2.
func reportPerPair(b *testing.B, n int) {
	pairs := int64(n) * int64(n-1) / 2
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/(float64(b.N)*float64(pairs)), "ns/pair")
}

// BenchmarkValidateReuse measures warm validation of the linear fixture: the
// Validator and destination are primed once outside the timer, so every
// iteration must allocate 0 B/op with 0 allocs/op.
func BenchmarkValidateReuse(b *testing.B) {
	for _, nodes := range validateBenchmarkSizes {
		b.Run(fmt.Sprintf("Nodes%d", nodes), func(b *testing.B) {
			doc, fields := buildLinearFixture(b, nodes)
			var v Validator
			dst := make([]Diagnostic, 0, 16)
			primeValidator(b, &v, doc, fields, dst)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				validateBenchSink += len(v.Validate(dst[:0], doc, fields))
			}
			b.StopTimer()
			reportPerNode(b, nodes)
		})
	}
}

// BenchmarkValidateFresh measures cold validation of the linear fixture: a
// zero-value Validator is created each iteration, so the reported allocations
// are the first-use node/clause state and traversal stack.
func BenchmarkValidateFresh(b *testing.B) {
	for _, nodes := range validateBenchmarkSizes {
		b.Run(fmt.Sprintf("Nodes%d", nodes), func(b *testing.B) {
			doc, fields := buildLinearFixture(b, nodes)
			if diags := Validate(nil, doc, fields); len(diags) != 0 {
				b.Fatalf("fixture emitted %d diagnostics", len(diags))
			}
			dst := make([]Diagnostic, 0, 16)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var v Validator
				validateBenchSink += len(v.Validate(dst[:0], doc, fields))
			}
			b.StopTimer()
			reportPerNode(b, nodes)
		})
	}
}

// BenchmarkValidateCatalogUniqueScaling measures the full unique-name
// predecessor scan of the evidence-kind catalog. A warmed Validator must
// allocate 0 B/op at every size.
func BenchmarkValidateCatalogUniqueScaling(b *testing.B) {
	for _, kinds := range validateBenchmarkSizes {
		b.Run(fmt.Sprintf("Rows%d", kinds), func(b *testing.B) {
			doc, fields := buildCatalogFixture(b, kinds)
			var v Validator
			dst := make([]Diagnostic, 0, 16)
			primeValidator(b, &v, doc, fields, dst)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				validateBenchSink += len(v.Validate(dst[:0], doc, fields))
			}
			b.StopTimer()
			reportPerItem(b, kinds)
			reportPerPair(b, kinds)
		})
	}
}

// BenchmarkValidateRequirementUniqueScaling measures the full unique-ID
// predecessor scan of the requirement table. A warmed Validator must allocate
// 0 B/op at every size.
func BenchmarkValidateRequirementUniqueScaling(b *testing.B) {
	for _, reqs := range validateBenchmarkSizes {
		b.Run(fmt.Sprintf("Rows%d", reqs), func(b *testing.B) {
			doc, fields := buildRequirementFixture(b, reqs)
			var v Validator
			dst := make([]Diagnostic, 0, 16)
			primeValidator(b, &v, doc, fields, dst)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				validateBenchSink += len(v.Validate(dst[:0], doc, fields))
			}
			b.StopTimer()
			reportPerItem(b, reqs)
			reportPerPair(b, reqs)
		})
	}
}

// TestValidationBenchmarkDocuments builds each Task 7.5 fixture at a small and
// a representative size and asserts the public package-level Validate returns
// zero diagnostics, so fixture correctness runs in the ordinary test suite
// rather than only under -bench.
func TestValidationBenchmarkDocuments(t *testing.T) {
	for _, size := range []int{16, 8192} {
		t.Run(fmt.Sprintf("linear%d", size), func(t *testing.T) {
			doc, fields := buildLinearFixture(t, size)
			if diags := Validate(nil, doc, fields); len(diags) != 0 {
				t.Fatalf("linear size %d emitted %d diagnostics: %+v", size, len(diags), diags)
			}
		})
		t.Run(fmt.Sprintf("catalog%d", size), func(t *testing.T) {
			doc, fields := buildCatalogFixture(t, size)
			if diags := Validate(nil, doc, fields); len(diags) != 0 {
				t.Fatalf("catalog size %d emitted %d diagnostics: %+v", size, len(diags), diags)
			}
		})
		t.Run(fmt.Sprintf("requirement%d", size), func(t *testing.T) {
			doc, fields := buildRequirementFixture(t, size)
			if diags := Validate(nil, doc, fields); len(diags) != 0 {
				t.Fatalf("requirement size %d emitted %d diagnostics: %+v", size, len(diags), diags)
			}
		})
	}
}
