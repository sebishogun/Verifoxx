package diff

import (
	"context"
	"strconv"
	"strings"
	"testing"

	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

func BenchmarkCandidateBatch(b *testing.B) {
	var analyzer Analyzer
	oldProgram, newProgram, err := analyzer.compilePair([]byte(nornrune.Source()), []byte(nornrune.Source()), nativeFieldSchema())
	if err != nil {
		b.Fatal(err)
	}
	domain := nativeDomain(128)
	plan, err := buildSearchPlan(nil, oldProgram, newProgram, domain)
	if err != nil {
		b.Fatal(err)
	}
	var materializer candidateMaterializer
	var batches candidateBatches
	if err := materializer.materialize(&batches, oldProgram, newProgram, plan, domain, 0, 64); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := materializer.materialize(&batches, oldProgram, newProgram, plan, domain, 0, 64); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDiffIdentity(b *testing.B) {
	fields := nativeFieldSchema()
	domain := comparisonDomain()
	matrix := uniformRiskMatrix(Changed, true)
	source := []byte(nornrune.Source())
	var analyzer Analyzer
	var result Result
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := analyzer.Compare(context.Background(), &result, source, source, fields, domain, matrix, nil); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDiffSearch(b *testing.B) {
	for _, outputOptions := range []int{2, 16, 1024} {
		domain := benchmarkDiffDomain(outputOptions)
		b.Run(strconv.FormatUint(domain.MaxCandidates, 10), func(b *testing.B) {
			fields := nativeFieldSchema()
			matrix := uniformRiskMatrix(Changed, true)
			oldSource := []byte(nornrune.Source())
			newSource := []byte(strings.ReplaceAll(nornrune.Source(), `"aggregate_counts"`, `"aggregate_totals"`))
			var analyzer Analyzer
			var result Result
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := analyzer.Compare(context.Background(), &result, oldSource, newSource, fields, domain, matrix, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkDiffDomain(outputOptions int) Domain {
	domain := comparisonDomain()
	for row := range domain.Fields {
		if domain.Fields[row].Name != "action.output" {
			continue
		}
		domain.Fields[row].Values = make([]Value, outputOptions)
		domain.Fields[row].Values[0] = Value{Kind: FieldKindString, State: ValueMissing}
		for option := 1; option < outputOptions; option++ {
			value := "output-" + strconv.Itoa(option)
			if option == 1 {
				value = "aggregate_counts"
			}
			domain.Fields[row].Values[option] = Value{Kind: FieldKindString, State: ValuePresent, String: value}
		}
		break
	}
	domain.MaxCandidates = uint64(outputOptions) * 64
	return domain
}
