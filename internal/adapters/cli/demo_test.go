package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sebishogun/nornrune/internal/fixtures"
	"github.com/sebishogun/nornrune/internal/simdops"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

func TestDemoRunsCompleteEmbeddedWorkflow(t *testing.T) {
	code, stdout, stderr := runCLIWithDependencies(t, productTestDependencies(), "demo")
	if code != 0 {
		t.Fatalf("demo = %d, want 0; stderr=%q", code, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	wantText := []string{
		"NORNRUNE POLICY ENGINE DEMO\n",
		"Policy: nornrune 1.0.0\n",
		"SHA-256: 2b26fdb9304cb045f4490039061090da01e10e0140e7e16a22e3b71816fc8245\n",
		"Engine: test-engine\n",
		"SIMD: ",
		"Program: 14 instructions | 3 requirements | 5 clauses\n",
		"BASELINE DECISIONS\n",
		"R1  Approve\n    The external aggregate request has valid pre-execution approval and verified approved-local-environment evidence.\n",
		"R2  Reject\n    The requested individual-record export violates R1's non-negotiable disclosure restriction.\n",
		"R3  Revise\n    The above-standard usage request can be corrected by providing the required scoped usage-adjustment approval.\n",
		"R4  Escalate\n    The approved local execution environment cannot be verified because the required attestation is missing.\n",
		"R5  Escalate\n    The pre-execution approval record is conflicting, so the request cannot be decided automatically.\n",
		"SCENARIO SIMULATIONS\n",
		"R3  environment.usage=standard\n    Revise -> Approve\n    The approved local execution environment is present and verified.\n",
		"R2  action.type=aggregate_analysis, action.output=aggregate_counts\n    Reject -> Approve\n    The external aggregate request has valid pre-execution approval and verified approved-local-environment evidence.\n",
		"PIPELINE TIMINGS\n",
		"Compile: ",
		"Decode: ",
		"Evaluate: ",
		"Simulate R3: ",
		"Simulate R2: ",
		"Total: ",
	}
	for _, want := range wantText {
		if !strings.Contains(stdout, want) {
			t.Errorf("demo output missing %q:\n%s", want, stdout)
		}
	}
	if count := strings.Count(stdout, "SCENARIO SIMULATIONS\n"); count != 1 {
		t.Errorf("scenario section count = %d, want 1:\n%s", count, stdout)
	}
}

func TestDemoUsesExternalInputs(t *testing.T) {
	code, stdout, stderr := runCLIWithDependencies(t, productTestDependencies(),
		"demo", "--policy", "valid-policy.json", "--requests", "requests.json", "--evidence", "evidence.json")
	if code != 0 || stderr != "" {
		t.Fatalf("external demo = (%d, %q), want success", code, stderr)
	}
	if !strings.Contains(stdout, "Engine: test-engine\n") || !strings.Contains(stdout, "R2  Reject\n") {
		t.Fatalf("external demo output = %q", stdout)
	}
}

func TestDemoRejectsArguments(t *testing.T) {
	code, stdout, stderr := runCLIWithDependencies(t, productTestDependencies(), "demo", "extra")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("demo extra = (%d, %q, %q), want usage failure", code, stdout, stderr)
	}
}

func TestDemoDoesNotWritePartialOutputOnPipelineFailure(t *testing.T) {
	code, stdout, stderr := runCLIWithDependencies(t, productTestDependencies(), "demo", "--policy", "malformed-policy.json")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "decode policy") {
		t.Fatalf("malformed demo = (%d, %q, %q), want operational failure without stdout", code, stdout, stderr)
	}
}

func TestDemoRejectsMalformedBatchInputsWithoutOutput(t *testing.T) {
	tests := [][]string{
		{"demo", "--requests", "malformed-requests.json"},
		{"demo", "--evidence", "malformed-evidence.json"},
	}
	for _, args := range tests {
		code, stdout, stderr := runCLIWithDependencies(t, productTestDependencies(), args...)
		if code != 1 || stdout != "" || !strings.Contains(stderr, "decode batch") {
			t.Errorf("Execute(%q) = (%d, %q, %q), want batch failure without stdout", args, code, stdout, stderr)
		}
	}
}

func TestDemoRequiresScenarioRequestsWithoutOutput(t *testing.T) {
	deps := productTestDependencies()
	deps.requests = strings.Replace(deps.requests, `"id": "R3"`, `"id": "R30"`, 1)
	if deps.requests == fixtures.RequestsJSON() {
		t.Fatal("test fixture did not replace R3")
	}
	code, stdout, stderr := runCLIWithDependencies(t, deps, "demo")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "requires requests R2 and R3") {
		t.Fatalf("demo without R3 = (%d, %q, %q), want scenario-input failure without stdout", code, stdout, stderr)
	}
}

func TestDemoReturnsOneForStdoutFailure(t *testing.T) {
	var stderr bytes.Buffer
	code := executeWithDependencies(
		[]string{"demo"}, bytes.NewReader(nil), errorWriter{}, &stderr, productTestDependencies(),
	)
	if code != 1 {
		t.Fatalf("demo with failing stdout = %d, want 1", code)
	}
}

func TestDemoUsesInjectedRuntimeAndClock(t *testing.T) {
	base := time.Unix(0, 0)
	calls := 0
	now := func() time.Time {
		current := base.Add(time.Duration(calls) * 10 * time.Microsecond)
		calls++
		return current
	}
	output, err := runDemo(sources{
		policy:   []byte(nornrune.Source()),
		requests: []byte(fixtures.RequestsJSON()),
		evidence: []byte(fixtures.EvidenceJSON()),
	}, "test-engine", simdops.RuntimeInfo{Tier: "test-simd", Description: "test vector backend"}, now)
	if err != nil {
		t.Fatalf("runDemo: %v", err)
	}
	text := string(output)
	if !strings.Contains(text, "SIMD: test-simd - test vector backend\n") {
		t.Fatalf("runtime output = %q", text)
	}
	wantTimings := "Compile: 10 us\n" +
		"Decode: 10 us\n" +
		"Evaluate: 10 us\n" +
		"Render baseline: 10 us\n" +
		"Simulate R3: 10 us\n" +
		"Simulate R2: 10 us\n" +
		"Total: 60 us\n"
	if !strings.Contains(text, wantTimings) {
		t.Fatalf("timing output = %q, want block %q", text, wantTimings)
	}
}

func TestRunDemoRejectsInvalidRuntimeMetadata(t *testing.T) {
	now := func() time.Time { return time.Time{} }
	tests := []struct {
		name    string
		version string
		runtime simdops.RuntimeInfo
		now     func() time.Time
	}{
		{"empty engine version", "", simdops.RuntimeInfo{Tier: "test"}, now},
		{"empty SIMD tier", "test", simdops.RuntimeInfo{}, now},
		{"nil clock", "test", simdops.RuntimeInfo{Tier: "test"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			output, err := runDemo(sources{}, tc.version, tc.runtime, tc.now)
			if !errors.Is(err, errInvalidDemoMetadata) || output != nil {
				t.Fatalf("runDemo = (%q, %v), want nil, %v", output, err, errInvalidDemoMetadata)
			}
		})
	}
}
