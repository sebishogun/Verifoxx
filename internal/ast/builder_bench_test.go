package ast

import (
	"fmt"
	"testing"

	"github.com/sebishogun/nornrune/internal/schema"
)

var astBenchmarkSizes = [...]int{16, 128, 1024, 8192}

var (
	semanticBenchmarkSource        = []byte(`{"policy":"benchmark"}`)
	semanticBenchmarkPack          = []byte("benchmark")
	semanticBenchmarkVersion       = []byte("1.0.0")
	semanticBenchmarkEvidenceKind  = []byte("approval")
	semanticBenchmarkEvidenceState = []byte("current")
	semanticBenchmarkOutcome       = []byte("Approve")
)

func benchmarkHints(nodes int) Hints {
	return Hints{
		Nodes: nodes, CompareNodes: nodes - 1,
		GroupNodes: 1, ChildEdges: nodes - 1,
		Values: 1, IntegerValues: 1,
	}
}

func buildBenchmarkPolicy(builder *Builder, children []schema.NodeID) error {
	value, err := builder.AddIntegerValue(1)
	if err != nil {
		return err
	}
	for i := range children {
		id, err := builder.AddCompare(1, CompareOpEqual, value, SourceSpan{})
		if err != nil {
			return err
		}
		children[i] = id
	}
	_, err = builder.AddGroup(NodeKindAll, children, SourceSpan{})
	return err
}

func reportNodeTime(b *testing.B, nodes int) {
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/(float64(b.N)*float64(nodes)), "ns/node")
}

func BenchmarkASTBuildReuse(b *testing.B) {
	for _, nodes := range astBenchmarkSizes {
		b.Run(fmt.Sprintf("Nodes%d", nodes), func(b *testing.B) {
			builder := NewBuilder(benchmarkHints(nodes))
			children := make([]schema.NodeID, nodes-1)
			if err := buildBenchmarkPolicy(builder, children); err != nil {
				b.Fatal(err)
			}
			builder.Reset()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				builder.Reset()
				if err := buildBenchmarkPolicy(builder, children); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if builder.Len() != nodes {
				b.Fatalf("built %d nodes, want %d", builder.Len(), nodes)
			}
			reportNodeTime(b, nodes)
		})
	}
}

func BenchmarkASTBuildCold(b *testing.B) {
	for _, nodes := range astBenchmarkSizes {
		b.Run(fmt.Sprintf("Nodes%d", nodes), func(b *testing.B) {
			children := make([]schema.NodeID, nodes-1)
			var sink int
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				builder := NewBuilder(Hints{})
				if err := buildBenchmarkPolicy(builder, children); err != nil {
					b.Fatal(err)
				}
				sink += builder.Len()
			}
			b.StopTimer()
			if sink == 0 {
				b.Fatal("sink")
			}
			reportNodeTime(b, nodes)
		})
	}
}

type semanticBenchmarkScratch struct {
	children     []schema.NodeID
	inValues     []schema.ValueID
	evidence     []schema.NodeID
	remediations []schema.RemediationID
	clauses      []schema.ClauseID
}

func newSemanticBenchmarkScratch(nodes int) semanticBenchmarkScratch {
	return semanticBenchmarkScratch{
		children:     make([]schema.NodeID, nodes-2),
		inValues:     make([]schema.ValueID, 2),
		evidence:     make([]schema.NodeID, 1),
		remediations: make([]schema.RemediationID, 1),
		clauses:      make([]schema.ClauseID, 1),
	}
}

func semanticBenchmarkHints(nodes int) Hints {
	symbolBytes := len(semanticBenchmarkPack) + len(semanticBenchmarkVersion) +
		len(semanticBenchmarkEvidenceKind) + len(semanticBenchmarkEvidenceState) + len(semanticBenchmarkOutcome)
	return Hints{
		Nodes: nodes, CompareNodes: nodes - 3, CompareListValues: 2,
		GroupNodes: 1, ChildEdges: nodes - 2, NotNodes: 1, EvidenceNodes: 1,
		Values: 6, SymbolValues: 5, SymbolBytes: symbolBytes, IntegerValues: 1,
		EvidenceKinds: 1, EvidenceStates: 1, Outcomes: 1, Remediations: 1,
		Clauses: 1, ClauseEvidenceEdges: 1, ClauseRemediationEdges: 1,
		Requirements: 1, RequirementClauseEdges: 1, SourceBytes: len(semanticBenchmarkSource),
	}
}

func buildSemanticBenchmarkPolicy(builder *Builder, scratch *semanticBenchmarkScratch) error {
	if err := builder.SetSource(semanticBenchmarkSource); err != nil {
		return err
	}
	pack, err := builder.AddSymbolValue(semanticBenchmarkPack)
	if err != nil {
		return err
	}
	version, err := builder.AddSymbolValue(semanticBenchmarkVersion)
	if err != nil {
		return err
	}
	evidenceKindName, err := builder.AddSymbolValue(semanticBenchmarkEvidenceKind)
	if err != nil {
		return err
	}
	evidenceStateName, err := builder.AddSymbolValue(semanticBenchmarkEvidenceState)
	if err != nil {
		return err
	}
	outcomeName, err := builder.AddSymbolValue(semanticBenchmarkOutcome)
	if err != nil {
		return err
	}
	literal, err := builder.AddIntegerValue(1)
	if err != nil {
		return err
	}
	if err := builder.SetMetadata(pack, version); err != nil {
		return err
	}
	evidenceKind, err := builder.AddEvidenceKind(evidenceKindName, SourceSpan{})
	if err != nil {
		return err
	}
	evidenceState, err := builder.AddEvidenceState(evidenceStateName, SourceSpan{})
	if err != nil {
		return err
	}
	outcome, err := builder.AddOutcome(outcomeName, 1, true, SourceSpan{})
	if err != nil {
		return err
	}
	remediation, err := builder.AddSetFieldRemediation(1, literal, SourceSpan{})
	if err != nil {
		return err
	}
	scratch.inValues[0], scratch.inValues[1] = literal, literal
	first, err := builder.AddIn(1, scratch.inValues, SourceSpan{})
	if err != nil {
		return err
	}
	scratch.children[0] = first
	for i := 1; i < len(scratch.children)-1; i++ {
		id, err := builder.AddCompare(1, CompareOpEqual, literal, SourceSpan{})
		if err != nil {
			return err
		}
		scratch.children[i] = id
	}
	evidenceNode, err := builder.AddEvidence(evidenceKind, evidenceState, SourceSpan{})
	if err != nil {
		return err
	}
	scratch.children[len(scratch.children)-1] = evidenceNode
	group, err := builder.AddGroup(NodeKindAll, scratch.children, SourceSpan{})
	if err != nil {
		return err
	}
	assertion, err := builder.AddNot(group, SourceSpan{})
	if err != nil {
		return err
	}
	scratch.evidence[0] = evidenceNode
	scratch.remediations[0] = remediation
	resolution := Resolution{
		OnSatisfied: outcome, OnFalse: outcome, OnMissing: outcome, OnStale: outcome,
		OnUnclear: outcome, OnUnverifiable: outcome, OnConflict: outcome,
	}
	clause, err := builder.AddClause(assertion, scratch.evidence, resolution, scratch.remediations, SourceSpan{})
	if err != nil {
		return err
	}
	scratch.clauses[0] = clause
	return builder.AddRequirement(1, group, scratch.clauses, SourceSpan{})
}

func BenchmarkASTBuildSemanticReuse(b *testing.B) {
	for _, nodes := range astBenchmarkSizes {
		b.Run(fmt.Sprintf("Nodes%d", nodes), func(b *testing.B) {
			builder := NewBuilder(semanticBenchmarkHints(nodes))
			scratch := newSemanticBenchmarkScratch(nodes)
			if err := buildSemanticBenchmarkPolicy(builder, &scratch); err != nil {
				b.Fatal(err)
			}
			builder.Reset()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				builder.Reset()
				if err := buildSemanticBenchmarkPolicy(builder, &scratch); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			if builder.Len() != nodes {
				b.Fatalf("built %d nodes, want %d", builder.Len(), nodes)
			}
			reportNodeTime(b, nodes)
		})
	}
}

func BenchmarkASTBuildSemanticCold(b *testing.B) {
	for _, nodes := range astBenchmarkSizes {
		b.Run(fmt.Sprintf("Nodes%d", nodes), func(b *testing.B) {
			scratch := newSemanticBenchmarkScratch(nodes)
			var sink int
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				builder := NewBuilder(Hints{})
				if err := buildSemanticBenchmarkPolicy(builder, &scratch); err != nil {
					b.Fatal(err)
				}
				sink += builder.Len()
			}
			b.StopTimer()
			if sink == 0 {
				b.Fatal("sink")
			}
			reportNodeTime(b, nodes)
		})
	}
}
