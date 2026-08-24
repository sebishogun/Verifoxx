package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBenchCommandContract(t *testing.T) {
	code, stdout, stderr := runCLIWithDependencies(t, productTestDependencies())
	if code != 0 || stderr != "" || !strings.Contains(stdout, "bench") {
		t.Fatalf("root help = (%d, %q, %q), want bench command", code, stdout, stderr)
	}

	code, stdout, stderr = runCLIWithDependencies(t, productTestDependencies(), "bench", "--help")
	if code != 0 || stderr != "" {
		t.Fatalf("bench help = (%d, %q, %q)", code, stdout, stderr)
	}
	for _, token := range []string{"--rows", "--iterations", "--workers"} {
		if !strings.Contains(stdout, token) {
			t.Fatalf("bench help omits %s: %q", token, stdout)
		}
	}
	for _, token := range []string{"--policy", "--requests", "--evidence"} {
		if strings.Contains(stdout, token) {
			t.Fatalf("bench help exposes input flag %s: %q", token, stdout)
		}
	}
}

func TestBenchRejectsInvalidBoundsAndInputs(t *testing.T) {
	for _, args := range [][]string{
		{"bench", "--rows", "0"},
		{"bench", "--rows", "65537"},
		{"bench", "--iterations", "0"},
		{"bench", "--iterations", "100001"},
		{"bench", "--workers", "0"},
		{"bench", "--workers", "257"},
		{"bench", "payload.json"},
		{"bench", "--policy", "policy.json"},
	} {
		code, stdout, stderr := runCLIWithDependencies(t, productTestDependencies(), args...)
		if code != 2 || stdout != "" || stderr == "" {
			t.Fatalf("bench %q = (%d, %q, %q), want usage failure", args, code, stdout, stderr)
		}
	}
}

func TestBenchReportsDeterministicScheduledRun(t *testing.T) {
	code, stdout, stderr := runCLIWithDependencies(t, productTestDependencies(),
		"bench", "--rows", "256", "--iterations", "3", "--workers", "2")
	if code != 0 || stderr != "" {
		t.Fatalf("bench = (%d, %q, %q), want success", code, stdout, stderr)
	}
	var report struct {
		ExecutionMode  string `json:"execution_mode"`
		SIMDTier       string `json:"simd_tier"`
		Rows           uint32 `json:"rows"`
		PolicyNodes    uint32 `json:"policy_nodes"`
		Evidence       uint32 `json:"evidence_records"`
		EvidenceRefs   uint32 `json:"evidence_refs"`
		Iterations     uint32 `json:"iterations"`
		Workers        int    `json:"workers"`
		ElapsedNS      uint64 `json:"elapsed_ns"`
		RowsPerSecond  uint64 `json:"rows_per_second"`
		AllocatedBytes uint64 `json:"allocated_bytes"`
		Allocations    uint64 `json:"allocations"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("decode bench output: %v\n%s", err, stdout)
	}
	if report.Rows != 256 || report.Iterations != 3 || report.Workers != 2 ||
		report.PolicyNodes == 0 || report.Evidence == 0 || report.EvidenceRefs == 0 ||
		report.ExecutionMode != "parallel" || report.SIMDTier == "" ||
		report.ElapsedNS == 0 || report.RowsPerSecond == 0 {
		t.Fatalf("bench report = %+v", report)
	}
}

func TestBenchPrimesEverySerialWorkerOutsideMeasurement(t *testing.T) {
	report, err := runProductBenchmark(context.Background(), benchmarkOptions{
		rows: 64, iterations: 1, workers: 4,
	}, productTestDependencies())
	if err != nil {
		t.Fatalf("runProductBenchmark: %v", err)
	}
	if report.executionMode != "serial" || report.allocatedBytes != 0 || report.allocations != 0 {
		t.Fatalf("serial benchmark report = %+v, want warmed zero-allocation execution", report)
	}
}

func TestBenchPropagatesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	code := executeWithDependenciesContext(ctx, []string{"bench"}, bytes.NewReader(nil), &stdout, &stderr, productTestDependencies())
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), context.Canceled.Error()) {
		t.Fatalf("canceled bench = (%d, %q, %q)", code, stdout.String(), stderr.String())
	}
}
