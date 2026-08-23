package cli

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/sebishogun/verifoxx/internal/fixtures"
	"github.com/sebishogun/verifoxx/internal/simdops"
	verifoxx "github.com/sebishogun/verifoxx/policies/verifoxx"
)

var (
	benchmarkDemoOutput []byte
	benchmarkExitCode   int
)

func BenchmarkDemoPipeline(b *testing.B) {
	inputs := sources{
		policy:   []byte(verifoxx.Source()),
		requests: []byte(fixtures.RequestsJSON()),
		evidence: []byte(fixtures.EvidenceJSON()),
	}
	runtime := simdops.RuntimeInfo{Tier: "benchmark", Description: "fixed backend"}
	now := func() time.Time { return time.Time{} }
	bytesPerRun := len(inputs.policy) + len(inputs.requests) + len(inputs.evidence)
	b.ReportAllocs()
	b.SetBytes(int64(bytesPerRun))
	b.ResetTimer()
	for b.Loop() {
		var err error
		benchmarkDemoOutput, err = runDemo(inputs, "benchmark", runtime, now)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkEvaluateCommand(b *testing.B) {
	deps := productionDependencies()
	args := []string{"evaluate"}
	stdin := bytes.NewReader(nil)
	bytesPerRun := len(deps.policy) + len(deps.requests) + len(deps.evidence)
	b.ReportAllocs()
	b.SetBytes(int64(bytesPerRun))
	b.ResetTimer()
	for b.Loop() {
		benchmarkExitCode = executeWithDependencies(args, stdin, io.Discard, io.Discard, deps)
		if benchmarkExitCode != 0 {
			b.Fatalf("evaluate exit code = %d", benchmarkExitCode)
		}
	}
}

func BenchmarkDemoCommand(b *testing.B) {
	deps := productionDependencies()
	args := []string{"demo"}
	stdin := bytes.NewReader(nil)
	bytesPerRun := len(deps.policy) + len(deps.requests) + len(deps.evidence)
	b.ReportAllocs()
	b.SetBytes(int64(bytesPerRun))
	b.ResetTimer()
	for b.Loop() {
		benchmarkExitCode = executeWithDependencies(args, stdin, io.Discard, io.Discard, deps)
		if benchmarkExitCode != 0 {
			b.Fatalf("demo exit code = %d", benchmarkExitCode)
		}
	}
}
