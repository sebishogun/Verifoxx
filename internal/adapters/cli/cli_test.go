package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/buildinfo"
	"github.com/sebishogun/verifoxx/internal/compile"
	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/fixtures"
	policyindex "github.com/sebishogun/verifoxx/internal/index"
	"github.com/sebishogun/verifoxx/internal/schema"
	verifoxx "github.com/sebishogun/verifoxx/policies/verifoxx"
)

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Execute(args, bytes.NewReader(nil), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func runCLIWithDependencies(t *testing.T, deps dependencies, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := executeWithDependencies(args, bytes.NewReader(nil), &stdout, &stderr, deps)
	return code, stdout.String(), stderr.String()
}

func TestRootNoArgumentsPrintsHelp(t *testing.T) {
	code, stdout, stderr := runCLI(t)
	if code != 0 {
		t.Fatalf("Execute() = %d, want 0", code)
	}
	if !strings.Contains(stdout, "Usage:") {
		t.Fatalf("stdout = %q, want usage", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRootHelpForms(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}} {
		code, stdout, stderr := runCLI(t, args...)
		if code != 0 {
			t.Errorf("Execute(%q) = %d, want 0", args, code)
		}
		if !strings.Contains(stdout, "Usage:") {
			t.Errorf("Execute(%q) stdout = %q, want usage", args, stdout)
		}
		if stderr != "" {
			t.Errorf("Execute(%q) stderr = %q, want empty", args, stderr)
		}
	}
}

func TestRootVersion(t *testing.T) {
	code, stdout, stderr := runCLI(t, "--version")
	if code != 0 {
		t.Fatalf("Execute(--version) = %d, want 0", code)
	}
	if want := buildinfo.Version() + "\n"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRootVersionUsesInjectedDependency(t *testing.T) {
	code, stdout, stderr := runCLIWithDependencies(t, productTestDependencies(), "--version")
	if code != 0 || stdout != "test-engine\n" || stderr != "" {
		t.Fatalf("injected --version = (%d, %q, %q), want (0, %q, empty)", code, stdout, stderr, "test-engine\n")
	}
}

func TestRootDoesNotExposeUnplannedCompletionCommand(t *testing.T) {
	code, stdout, stderr := runCLI(t)
	if code != 0 || stderr != "" {
		t.Fatalf("root help = (%d, %q), want success", code, stderr)
	}
	if strings.Contains(stdout, "completion") {
		t.Fatalf("root help exposes completion command: %q", stdout)
	}

	code, stdout, stderr = runCLI(t, "completion", "bash")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "unknown command") {
		t.Fatalf("completion command = (%d, %q, %q), want unknown-command usage", code, stdout, stderr)
	}
}

func TestRootRejectsUnknownCommand(t *testing.T) {
	code, stdout, stderr := runCLI(t, "bogus")
	if code != 2 {
		t.Fatalf("Execute(bogus) = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "unknown command") || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("stderr = %q, want error and usage", stderr)
	}
}

func TestRootRejectsTrailingArguments(t *testing.T) {
	code, stdout, stderr := runCLI(t, "--version", "extra")
	if code != 2 {
		t.Fatalf("Execute(--version extra) = %d, want 2", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Fatalf("stderr = %q, want usage", stderr)
	}
}

func TestRootReturnsOneForStdoutFailure(t *testing.T) {
	for _, args := range [][]string{nil, {"--version"}} {
		var stderr bytes.Buffer
		if code := Execute(args, bytes.NewReader(nil), errorWriter{}, &stderr); code != 1 {
			t.Errorf("Execute(%q) with failing stdout = %d, want 1", args, code)
		}
	}
}

func TestRootReturnsOneForStderrFailure(t *testing.T) {
	var stdout bytes.Buffer
	if code := Execute([]string{"bogus"}, bytes.NewReader(nil), &stdout, errorWriter{}); code != 1 {
		t.Fatalf("Execute(bogus) with failing stderr = %d, want 1", code)
	}
}

func sourceTestDependencies() dependencies {
	return dependencies{
		readFile: func(path string) ([]byte, error) {
			switch path {
			case "policy.json":
				return []byte("external-policy"), nil
			case "requests.json":
				return []byte("external-requests"), nil
			case "evidence.json":
				return []byte("external-evidence"), nil
			default:
				return nil, errors.New("missing file")
			}
		},
		policy:   "embedded-policy",
		requests: "embedded-requests",
		evidence: "embedded-evidence",
		version:  "test",
	}
}

func TestSourcesUseEmbeddedDefaults(t *testing.T) {
	deps := sourceTestDependencies()
	got, err := loadSources(sourceFlags{}, strings.NewReader("unused"), deps, sourceAll)
	if err != nil {
		t.Fatalf("loadSources: %v", err)
	}
	if string(got.policy) != deps.policy || string(got.requests) != deps.requests || string(got.evidence) != deps.evidence {
		t.Fatalf("sources = (%q, %q, %q), want embedded inputs", got.policy, got.requests, got.evidence)
	}

	got.policy[0] = 'X'
	again, err := loadSources(sourceFlags{}, strings.NewReader("unused"), deps, sourcePolicy)
	if err != nil {
		t.Fatalf("second loadSources: %v", err)
	}
	if string(again.policy) != deps.policy {
		t.Fatalf("mutating returned source changed embedded policy: %q", again.policy)
	}
}

func TestSourcesLoadExternalFiles(t *testing.T) {
	deps := sourceTestDependencies()
	flags := sourceFlags{policyPath: "policy.json", requestPath: "requests.json", evidencePath: "evidence.json"}
	got, err := loadSources(flags, strings.NewReader("unused"), deps, sourceAll)
	if err != nil {
		t.Fatalf("loadSources: %v", err)
	}
	if string(got.policy) != "external-policy" || string(got.requests) != "external-requests" || string(got.evidence) != "external-evidence" {
		t.Fatalf("external sources = (%q, %q, %q)", got.policy, got.requests, got.evidence)
	}
}

func TestSourcesReadOneInputFromStdin(t *testing.T) {
	deps := sourceTestDependencies()
	got, err := loadSources(sourceFlags{requestPath: "-"}, strings.NewReader("stdin-requests"), deps, sourceRequests)
	if err != nil {
		t.Fatalf("loadSources: %v", err)
	}
	if string(got.requests) != "stdin-requests" {
		t.Fatalf("requests = %q, want stdin input", got.requests)
	}
}

func TestSourcesRejectMultipleStdinInputs(t *testing.T) {
	deps := sourceTestDependencies()
	_, err := loadSources(sourceFlags{policyPath: "-", requestPath: "-"}, strings.NewReader("input"), deps, sourceAll)
	if err == nil {
		t.Fatal("loadSources accepted multiple stdin inputs")
	}
	var status *commandError
	if !errors.As(err, &status) || status.code != 2 {
		t.Fatalf("multiple stdin error = %v, want usage status", err)
	}
}

func TestSourcesReturnFileErrors(t *testing.T) {
	deps := sourceTestDependencies()
	_, err := loadSources(sourceFlags{policyPath: "missing.json"}, strings.NewReader("unused"), deps, sourcePolicy)
	if err == nil || !strings.Contains(err.Error(), "missing file") {
		t.Fatalf("loadSources missing file error = %v", err)
	}
}

func TestPipelineCompilesAndEvaluatesEmbeddedInputs(t *testing.T) {
	var engine engine
	compiled, err := engine.compilePolicy([]byte(verifoxx.Source()))
	if err != nil {
		t.Fatalf("compilePolicy: %v", err)
	}
	if compiled.InstructionCount() == 0 || len(compiled.RequirementIDs) != 3 {
		t.Fatalf("compiled shape = %d instructions, %d requirements", compiled.InstructionCount(), len(compiled.RequirementIDs))
	}

	batch, err := engine.decodeBatch(compiled, []byte(fixtures.RequestsJSON()), []byte(fixtures.EvidenceJSON()))
	if err != nil {
		t.Fatalf("decodeBatch: %v", err)
	}
	if batch.Rows != 5 {
		t.Fatalf("batch rows = %d, want 5", batch.Rows)
	}
	for row, id := range batch.RequestIDs {
		if id != schema.RequestID(row+1) {
			t.Fatalf("request row %d ID = %d, want %d", row, id, row+1)
		}
	}

	decisions, err := engine.evaluate(compiled, batch)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if decisions.Rows != batch.Rows || len(decisions.OutcomeIDs) != int(batch.Rows) {
		t.Fatalf("result shape = %d rows, %d outcomes", decisions.Rows, len(decisions.OutcomeIDs))
	}
}

func TestPipelineScheduledEvaluation(t *testing.T) {
	var direct engine
	compiled, err := direct.compilePolicy([]byte(verifoxx.Source()))
	if err != nil {
		t.Fatalf("compilePolicy: %v", err)
	}
	batch, err := direct.decodeBatch(compiled, []byte(fixtures.RequestsJSON()), []byte(fixtures.EvidenceJSON()))
	if err != nil {
		t.Fatalf("decodeBatch: %v", err)
	}
	want, err := direct.evaluate(compiled, batch)
	if err != nil {
		t.Fatalf("direct evaluate: %v", err)
	}

	var scheduled engine
	got, err := scheduled.evaluateScheduled(context.Background(), compiled, batch)
	if err != nil {
		t.Fatalf("evaluateScheduled: %v", err)
	}
	if !reflect.DeepEqual(*got, *want) {
		t.Fatalf("scheduled result differs\ngot:  %+v\nwant: %+v", *got, *want)
	}
	stats := scheduled.scheduler.Stats()
	if stats.Executions != 1 || stats.Serial != 1 || stats.Parallel != 0 {
		t.Fatalf("scheduler stats = %+v, want one serial execution", stats)
	}
	if err := scheduled.closeScheduler(); err != nil {
		t.Fatalf("closeScheduler: %v", err)
	}
	if err := scheduled.closeScheduler(); err != nil {
		t.Fatalf("second closeScheduler: %v", err)
	}
}

func TestPipelineScheduledParallelEvaluation(t *testing.T) {
	previousProcs := runtime.GOMAXPROCS(2)
	t.Cleanup(func() { runtime.GOMAXPROCS(previousProcs) })
	var fixture engine
	compiled, err := fixture.compilePolicy([]byte(verifoxx.Source()))
	if err != nil {
		t.Fatalf("compilePolicy: %v", err)
	}
	base, err := fixture.decodeBatch(compiled, []byte(fixtures.RequestsJSON()), []byte(fixtures.EvidenceJSON()))
	if err != nil {
		t.Fatalf("decodeBatch: %v", err)
	}
	var builder eval.Builder
	batch, err := repeatBenchmarkBatch(&builder, compiled, base, 256)
	if err != nil {
		t.Fatalf("repeatBenchmarkBatch: %v", err)
	}
	var direct engine
	want, err := direct.evaluate(compiled, batch)
	if err != nil {
		t.Fatalf("direct evaluate: %v", err)
	}
	var scheduled engine
	got, err := scheduled.evaluateScheduled(context.Background(), compiled, batch)
	if err != nil {
		t.Fatalf("evaluateScheduled: %v", err)
	}
	t.Cleanup(func() {
		if err := scheduled.closeScheduler(); err != nil {
			t.Errorf("closeScheduler: %v", err)
		}
	})
	if !reflect.DeepEqual(*got, *want) {
		t.Fatalf("parallel scheduled result differs\ngot:  %+v\nwant: %+v", *got, *want)
	}
	stats := scheduled.scheduler.Stats()
	if stats.Executions != 1 || stats.Serial != 0 || stats.Parallel != 1 {
		t.Fatalf("scheduler stats = %+v, want one parallel execution", stats)
	}
}

func TestPipelineScheduledCancellation(t *testing.T) {
	var pipeline engine
	compiled, err := pipeline.compilePolicy([]byte(verifoxx.Source()))
	if err != nil {
		t.Fatalf("compilePolicy: %v", err)
	}
	batch, err := pipeline.decodeBatch(compiled, []byte(fixtures.RequestsJSON()), []byte(fixtures.EvidenceJSON()))
	if err != nil {
		t.Fatalf("decodeBatch: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := pipeline.evaluateScheduled(ctx, compiled, batch); !errors.Is(err, context.Canceled) {
		t.Fatalf("evaluateScheduled cancellation error = %v", err)
	}
	if pipeline.scheduler != nil {
		t.Fatal("pre-canceled evaluation constructed a scheduler")
	}
}

func TestCLISchedulerWorkers(t *testing.T) {
	for _, test := range []struct {
		rows      uint32
		available int
		want      int
	}{
		{0, 8, 1},
		{255, 8, 1},
		{256, 8, 4},
		{257, 2, 2},
		{65536, 300, 256},
	} {
		if got := cliSchedulerWorkers(test.rows, test.available); got != test.want {
			t.Fatalf("cliSchedulerWorkers(%d, %d) = %d, want %d", test.rows, test.available, got, test.want)
		}
	}
}

func TestPipelineSeparatesDecodeAndSemanticFailures(t *testing.T) {
	var engine engine
	if _, err := engine.decodePolicy([]byte("{")); err == nil {
		t.Fatal("decodePolicy accepted malformed JSON")
	}

	decoded, err := engine.decodePolicy([]byte(verifoxx.Source()))
	if err != nil {
		t.Fatalf("decode valid policy: %v", err)
	}
	decoded.document.NodeKinds[0] = ast.NodeKindInvalid
	diagnostics := engine.validatePolicy(decoded)
	if len(diagnostics) == 0 {
		t.Fatal("validatePolicy accepted an invalid node kind")
	}
}

func productTestDependencies() dependencies {
	validPolicy := verifoxx.Source()
	return dependencies{
		readFile: func(path string) ([]byte, error) {
			switch path {
			case "valid-policy.json":
				return []byte(validPolicy), nil
			case "malformed-policy.json":
				return []byte("{"), nil
			case "requests.json":
				return []byte(fixtures.RequestsJSON()), nil
			case "evidence.json":
				return []byte(fixtures.EvidenceJSON()), nil
			case "malformed-requests.json", "malformed-evidence.json":
				return []byte("{"), nil
			default:
				return nil, errors.New("missing file")
			}
		},
		policy:   validPolicy,
		requests: fixtures.RequestsJSON(),
		evidence: fixtures.EvidenceJSON(),
		version:  "test-engine",
	}
}

func TestValidateEmbeddedPolicy(t *testing.T) {
	code, stdout, stderr := runCLIWithDependencies(t, productTestDependencies(), "validate")
	if code != 0 {
		t.Fatalf("validate = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != "{\"valid\":true,\"diagnostics\":[]}\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestValidateWritesDeterministicSemanticDiagnostics(t *testing.T) {
	diagnostics := []compile.Diagnostic{{
		Code:   compile.CodeTypeMismatch,
		Table:  compile.TableCompare,
		Member: compile.MemberValue,
		Row:    2,
		Span:   ast.SourceSpan{Start: 12, End: 34},
		Node:   3,
		Field:  4,
		Value:  5,
	}}
	var stdout bytes.Buffer
	err := writeValidationResult(&stdout, diagnostics)
	var status *commandError
	if !errors.As(err, &status) || status.code != 1 || !status.quiet {
		t.Fatalf("writeValidationResult error = %v, want quiet status 1", err)
	}
	want := "{\"valid\":false,\"diagnostics\":[{\"code\":\"type_mismatch\",\"table\":\"compare\",\"row\":2," +
		"\"member\":\"value\",\"span\":{\"start\":12,\"end\":34},\"ids\":{\"node\":3,\"field\":4,\"value\":5}}]}\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}

	var second bytes.Buffer
	_ = writeValidationResult(&second, diagnostics)
	if second.String() != stdout.String() {
		t.Fatalf("second diagnostics = %q, want %q", second.String(), stdout.String())
	}
}

func TestValidateReportsMalformedPolicyOnStderr(t *testing.T) {
	code, stdout, stderr := runCLIWithDependencies(t, productTestDependencies(), "validate", "--policy", "malformed-policy.json")
	if code != 1 {
		t.Fatalf("validate malformed policy = %d, want 1", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "decode policy") || !strings.HasSuffix(stderr, "\n") {
		t.Fatalf("stderr = %q, want one decode error", stderr)
	}
}

func TestValidateRejectsArguments(t *testing.T) {
	code, stdout, stderr := runCLIWithDependencies(t, productTestDependencies(), "validate", "extra")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("validate extra = (%d, %q, %q), want usage failure", code, stdout, stderr)
	}
}

func TestCompileEmbeddedPolicy(t *testing.T) {
	code, stdout, stderr := runCLIWithDependencies(t, productTestDependencies(), "compile")
	if code != 0 {
		t.Fatalf("compile = %d, want 0; stderr=%q", code, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	for _, fragment := range []string{
		`{"name":"verifoxx","version":"1.0.0","sha256":"a92ffd1c00e823652bed47acf3955f5559543eeba4f02ebf16965bc2966d0a22"`,
		`"instructions":`,
		`"requirements":3`,
		`"clauses":`,
		`"truth_slots":`,
		`"reason_slots":`,
	} {
		if !strings.Contains(stdout, fragment) {
			t.Errorf("stdout = %q, want fragment %q", stdout, fragment)
		}
	}
	if !strings.HasSuffix(stdout, "}\n") {
		t.Fatalf("stdout = %q, want one JSON object", stdout)
	}
	order := []string{`"name"`, `"version"`, `"sha256"`, `"instructions"`, `"requirements"`, `"clauses"`, `"truth_slots"`, `"reason_slots"`}
	position := -1
	for _, field := range order {
		next := strings.Index(stdout, field)
		if next <= position {
			t.Fatalf("field %s position = %d after %d in %q", field, next, position, stdout)
		}
		position = next
	}
}

func TestCompileUsesExternalPolicyAndRejectsArguments(t *testing.T) {
	deps := productTestDependencies()
	code, stdout, stderr := runCLIWithDependencies(t, deps, "compile", "--policy", "valid-policy.json")
	if code != 0 || !strings.Contains(stdout, `"name":"verifoxx"`) || stderr != "" {
		t.Fatalf("compile external = (%d, %q, %q)", code, stdout, stderr)
	}

	code, stdout, stderr = runCLIWithDependencies(t, deps, "compile", "extra")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("compile extra = (%d, %q, %q), want usage failure", code, stdout, stderr)
	}
}

func TestPolicyCommandsReturnOneForStdoutFailure(t *testing.T) {
	deps := productTestDependencies()
	for _, command := range []string{"validate", "compile"} {
		var stderr bytes.Buffer
		if code := executeWithDependencies([]string{command}, bytes.NewReader(nil), errorWriter{}, &stderr, deps); code != 1 {
			t.Errorf("%s with failing stdout = %d, want 1", command, code)
		}
	}
}

func TestEvaluateEmbeddedInputsMatchesGolden(t *testing.T) {
	want, err := os.ReadFile("../../../testdata/golden/requests.json")
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCLI(t, "evaluate")
	if code != 0 {
		t.Fatalf("evaluate = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != string(want) {
		t.Fatalf("evaluate output differs from golden\n--- got ---\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEvaluateUsesExternalInputs(t *testing.T) {
	code, stdout, stderr := runCLIWithDependencies(t, productTestDependencies(),
		"evaluate", "--policy", "valid-policy.json", "--requests", "requests.json", "--evidence", "evidence.json")
	if code != 0 {
		t.Fatalf("evaluate external = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, `"engine_version": "test-engine"`) || !strings.Contains(stdout, `"request_id": "R5"`) {
		t.Fatalf("stdout = %q, want external evaluation results", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestEvaluateRejectsMalformedBatchInputs(t *testing.T) {
	deps := productTestDependencies()
	for _, args := range [][]string{
		{"evaluate", "--requests", "malformed-requests.json"},
		{"evaluate", "--evidence", "malformed-evidence.json"},
	} {
		code, stdout, stderr := runCLIWithDependencies(t, deps, args...)
		if code != 1 || stdout != "" || !strings.Contains(stderr, "decode batch") {
			t.Errorf("Execute(%q) = (%d, %q, %q), want decode failure", args, code, stdout, stderr)
		}
	}
}

func TestEvaluateRejectsArgumentsAndMultipleStdinInputs(t *testing.T) {
	deps := productTestDependencies()
	code, stdout, stderr := runCLIWithDependencies(t, deps, "evaluate", "extra")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("evaluate extra = (%d, %q, %q), want usage failure", code, stdout, stderr)
	}

	var out, errOut bytes.Buffer
	code = executeWithDependencies(
		[]string{"evaluate", "--policy", "-", "--requests", "-"},
		strings.NewReader("input"), &out, &errOut, deps,
	)
	if code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "only one input") {
		t.Fatalf("evaluate multiple stdin = (%d, %q, %q), want usage failure", code, out.String(), errOut.String())
	}
}

func TestEvaluateReturnsOneForStdoutFailure(t *testing.T) {
	var stderr bytes.Buffer
	if code := executeWithDependencies(
		[]string{"evaluate"}, bytes.NewReader(nil), errorWriter{}, &stderr, productTestDependencies(),
	); code != 1 {
		t.Fatalf("evaluate with failing stdout = %d, want 1", code)
	}
}

func TestParseRequestID(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  schema.RequestID
	}{
		{"R1", 1},
		{"R5", 5},
		{"R4294967295", schema.RequestID(^uint32(0))},
	} {
		got, err := parseRequestID(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("parseRequestID(%q) = (%d, %v), want (%d, nil)", tc.input, got, err, tc.want)
		}
	}
	for _, input := range []string{"", "1", "r1", "R0", "R-1", "R+1", "R1x", "R4294967296"} {
		if got, err := parseRequestID(input); err == nil || got != 0 {
			t.Errorf("parseRequestID(%q) = (%d, %v), want (0, error)", input, got, err)
		}
	}
}

func TestCompactRowCopiesTypedFactsAndReferencedEvidence(t *testing.T) {
	var pipeline engine
	compiled, err := pipeline.compilePolicy([]byte(verifoxx.Source()))
	if err != nil {
		t.Fatal(err)
	}
	typed := *compiled
	kinds := []schema.ValueKind{
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
		schema.ValueKindBoolean,
		schema.ValueKindTimestamp,
		schema.ValueKindPresence,
	}
	if err := policyindex.BuildSchema(&typed.FieldIndex, kinds); err != nil {
		t.Fatal(err)
	}
	typed.FieldKinds = kinds
	typed.FieldNames = typed.FieldNames[:len(kinds)]
	typed.FieldGroups = typed.FieldGroups[:len(kinds)]

	var sourceBuilder eval.Builder
	if err := sourceBuilder.Begin(&typed, 2, 2, 2); err != nil {
		t.Fatalf("Begin source: %v", err)
	}
	if err := sourceBuilder.SetRequestID(0, 1); err != nil {
		t.Fatal(err)
	}
	if err := sourceBuilder.SetRequestID(1, 2); err != nil {
		t.Fatal(err)
	}
	extension, err := sourceBuilder.InternSymbol([]byte("extension-value"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceBuilder.SetSymbol(1, 1, extension); err != nil {
		t.Fatal(err)
	}
	if err := sourceBuilder.SetInteger(1, 2, -7); err != nil {
		t.Fatal(err)
	}
	if err := sourceBuilder.SetBoolean(1, 3, true); err != nil {
		t.Fatal(err)
	}
	if err := sourceBuilder.SetTimestamp(1, 4, 42); err != nil {
		t.Fatal(err)
	}
	if err := sourceBuilder.SetPresent(1, 5); err != nil {
		t.Fatal(err)
	}
	for row, record := range []eval.EvidenceRecord{
		{ID: 1, Kind: 1, State: 1},
		{ID: 2, Kind: 2, State: 2, Subject: extension, Reviewer: extension, Timestamp: 99},
	} {
		if err := sourceBuilder.SetEvidence(uint32(row), record); err != nil {
			t.Fatal(err)
		}
	}
	if err := sourceBuilder.SetEvidenceCSR([]uint32{0, 1, 2}, []uint32{0, 1}); err != nil {
		t.Fatal(err)
	}
	source, err := sourceBuilder.Finish()
	if err != nil {
		t.Fatal(err)
	}

	var selector rowSelector
	selected, err := selector.compact(&typed, source, &sourceBuilder, 1)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if selected.Rows != 1 || len(selected.RequestIDs) != 1 || selected.RequestIDs[0] != 2 {
		t.Fatalf("selected IDs = rows %d IDs %v", selected.Rows, selected.RequestIDs)
	}
	for field := schema.FieldID(1); field <= 5; field++ {
		if !selected.Present(field, 0) {
			t.Errorf("field %d is absent", field)
		}
	}
	if got, ok := selector.builder.Symbol(selected.SymbolValues[0]); !ok || string(got) != "extension-value" {
		t.Errorf("selected symbol = %q, %v", got, ok)
	}
	if len(selected.IntegerValues) != 1 || selected.IntegerValues[0] != -7 ||
		len(selected.TimestampValues) != 1 || selected.TimestampValues[0] != 42 || !selected.Boolean(0, 0) {
		t.Fatalf("selected typed values = integers %v timestamps %v booleans %#x",
			selected.IntegerValues, selected.TimestampValues, selected.BooleanValues)
	}
	if selected.Evidence.Len() != 1 || selected.Evidence.IDs[0] != 2 ||
		len(selected.EvidenceRefs) != 1 || selected.EvidenceRefs[0] != 0 ||
		len(selected.EvidenceOffsets) != 2 || selected.EvidenceOffsets[1] != 1 {
		t.Fatalf("selected evidence = IDs %v offsets %v refs %v", selected.Evidence.IDs, selected.EvidenceOffsets, selected.EvidenceRefs)
	}
	if got, ok := selector.builder.Symbol(selected.Evidence.Subjects[0]); !ok || string(got) != "extension-value" {
		t.Errorf("selected evidence subject = %q, %v", got, ok)
	}

	view := source
	view.Rows = 1
	view.RequestIDs = view.RequestIDs[1:]
	view.EvidenceOffsets = view.EvidenceOffsets[1:]
	var rejected rowSelector
	if _, err := rejected.compact(&typed, view, &sourceBuilder, 0); !errors.Is(err, errInvalidBatch) {
		t.Fatalf("compact range-like view error = %v, want %v", err, errInvalidBatch)
	}
}

func TestCompactRowWithoutEvidence(t *testing.T) {
	var pipeline engine
	compiled, err := pipeline.compilePolicy([]byte(verifoxx.Source()))
	if err != nil {
		t.Fatal(err)
	}
	var sourceBuilder eval.Builder
	if err := sourceBuilder.Begin(compiled, 1, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := sourceBuilder.SetRequestID(0, 1); err != nil {
		t.Fatal(err)
	}
	source, err := sourceBuilder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	var selector rowSelector
	selected, err := selector.compact(compiled, source, &sourceBuilder, 0)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if selected.Evidence.Len() != 0 || len(selected.EvidenceRefs) != 0 ||
		len(selected.EvidenceOffsets) != 2 || selected.EvidenceOffsets[0] != 0 || selected.EvidenceOffsets[1] != 0 {
		t.Fatalf("empty evidence = rows %d offsets %v refs %v", selected.Evidence.Len(), selected.EvidenceOffsets, selected.EvidenceRefs)
	}
}

func TestExplainReturnsOneRequestedResult(t *testing.T) {
	tests := []struct {
		id       string
		decision string
	}{
		{"R1", "Approve"},
		{"R2", "Reject"},
		{"R3", "Revise"},
		{"R4", "Escalate"},
		{"R5", "Escalate"},
	}
	for _, tc := range tests {
		t.Run(tc.id, func(t *testing.T) {
			code, stdout, stderr := runCLI(t, "explain", tc.id)
			if code != 0 {
				t.Fatalf("explain %s = %d, want 0; stderr=%q", tc.id, code, stderr)
			}
			if strings.Count(stdout, `"request_id":`) != 1 || !strings.Contains(stdout, `"request_id": "`+tc.id+`"`) ||
				!strings.Contains(stdout, `"decision": "`+tc.decision+`"`) {
				t.Fatalf("stdout = %q, want one %s %s result", stdout, tc.id, tc.decision)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
		})
	}
}

func TestExplainRejectsInvalidOrAbsentRequest(t *testing.T) {
	for _, args := range [][]string{
		{"explain"},
		{"explain", "r1"},
		{"explain", "R0"},
		{"explain", "R9"},
		{"explain", "R1", "extra"},
	} {
		code, stdout, stderr := runCLI(t, args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "Usage:") {
			t.Errorf("Execute(%q) = (%d, %q, %q), want usage failure", args, code, stdout, stderr)
		}
	}
}

func TestExplainUsesExternalInputs(t *testing.T) {
	code, stdout, stderr := runCLIWithDependencies(t, productTestDependencies(),
		"explain", "R2", "--policy", "valid-policy.json", "--requests", "requests.json", "--evidence", "evidence.json")
	if code != 0 || !strings.Contains(stdout, `"request_id": "R2"`) || !strings.Contains(stdout, `"engine_version": "test-engine"`) || stderr != "" {
		t.Fatalf("explain external = (%d, %q, %q)", code, stdout, stderr)
	}
}

func TestExplainReturnsOneForStdoutFailure(t *testing.T) {
	var stderr bytes.Buffer
	if code := executeWithDependencies(
		[]string{"explain", "R1"}, bytes.NewReader(nil), errorWriter{}, &stderr, productTestDependencies(),
	); code != 1 {
		t.Fatalf("explain with failing stdout = %d, want 1", code)
	}
}

func TestParseOverridesResolvesAndTypesFields(t *testing.T) {
	var pipeline engine
	compiled, err := pipeline.compilePolicy([]byte(verifoxx.Source()))
	if err != nil {
		t.Fatal(err)
	}
	typed := *compiled
	typed.FieldKinds = []schema.ValueKind{
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
		schema.ValueKindBoolean,
		schema.ValueKindTimestamp,
		schema.ValueKindPresence,
	}
	typed.FieldNames = typed.FieldNames[:len(typed.FieldKinds)]

	got, err := parseOverrides(nil, &typed, []string{
		"requester.team=partner=alpha",
		"requester.trust=-7",
		"action.type=false",
		"action.output=42",
		"action.dataset=true",
	})
	if err != nil {
		t.Fatalf("parseOverrides: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("overrides = %d, want 5", len(got))
	}
	if got[0].field != 1 || got[0].kind != schema.ValueKindSymbol || got[0].value != "partner=alpha" {
		t.Errorf("symbol override = %+v", got[0])
	}
	if got[1].field != 2 || got[1].kind != schema.ValueKindInteger || got[1].integer != -7 {
		t.Errorf("integer override = %+v", got[1])
	}
	if got[2].field != 3 || got[2].kind != schema.ValueKindBoolean || got[2].boolean {
		t.Errorf("Boolean override = %+v", got[2])
	}
	if got[3].field != 4 || got[3].kind != schema.ValueKindTimestamp || got[3].integer != 42 {
		t.Errorf("timestamp override = %+v", got[3])
	}
	if got[4].field != 5 || got[4].kind != schema.ValueKindPresence || !got[4].boolean {
		t.Errorf("presence override = %+v", got[4])
	}
}

func TestParseOverridesRejectsInvalidAssignments(t *testing.T) {
	var pipeline engine
	compiled, err := pipeline.compilePolicy([]byte(verifoxx.Source()))
	if err != nil {
		t.Fatal(err)
	}
	typed := *compiled
	typed.FieldKinds = []schema.ValueKind{
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
		schema.ValueKindBoolean,
		schema.ValueKindTimestamp,
		schema.ValueKindPresence,
	}
	typed.FieldNames = typed.FieldNames[:len(typed.FieldKinds)]

	tests := [][]string{
		{"=x"},
		{"requester.team="},
		{"requester.team"},
		{"unknown.field=x"},
		{"requester.team=x", "requester.team=y"},
		{"requester.trust=1x"},
		{"requester.trust=9223372036854775808"},
		{"action.type=True"},
		{"action.output=-9223372036854775809"},
		{"action.dataset=1"},
	}
	for _, assignments := range tests {
		if got, err := parseOverrides(nil, &typed, assignments); err == nil || len(got) != 0 {
			t.Errorf("parseOverrides(%q) = (%+v, %v), want nil, error", assignments, got, err)
		}
	}
}

func TestCompactRowAppliesTypedOverrides(t *testing.T) {
	var pipeline engine
	compiled, err := pipeline.compilePolicy([]byte(verifoxx.Source()))
	if err != nil {
		t.Fatal(err)
	}
	typed := *compiled
	kinds := []schema.ValueKind{
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
		schema.ValueKindBoolean,
		schema.ValueKindTimestamp,
		schema.ValueKindPresence,
	}
	if err := policyindex.BuildSchema(&typed.FieldIndex, kinds); err != nil {
		t.Fatal(err)
	}
	typed.FieldKinds = kinds
	typed.FieldNames = typed.FieldNames[:len(kinds)]
	typed.FieldGroups = typed.FieldGroups[:len(kinds)]

	var sourceBuilder eval.Builder
	if err := sourceBuilder.Begin(&typed, 1, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := sourceBuilder.SetRequestID(0, 1); err != nil {
		t.Fatal(err)
	}
	symbol, err := sourceBuilder.InternSymbol([]byte("before"))
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceBuilder.SetSymbol(0, 1, symbol); err != nil {
		t.Fatal(err)
	}
	if err := sourceBuilder.SetInteger(0, 2, 1); err != nil {
		t.Fatal(err)
	}
	if err := sourceBuilder.SetBoolean(0, 3, true); err != nil {
		t.Fatal(err)
	}
	if err := sourceBuilder.SetTimestamp(0, 4, 2); err != nil {
		t.Fatal(err)
	}
	if err := sourceBuilder.SetPresent(0, 5); err != nil {
		t.Fatal(err)
	}
	source, err := sourceBuilder.Finish()
	if err != nil {
		t.Fatal(err)
	}
	overrides, err := parseOverrides(nil, &typed, []string{
		"requester.team=after",
		"requester.trust=-7",
		"action.type=false",
		"action.output=42",
		"action.dataset=false",
	})
	if err != nil {
		t.Fatal(err)
	}
	var selector rowSelector
	selected, err := selector.compactWithOverrides(&typed, source, &sourceBuilder, 0, overrides)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := selector.builder.Symbol(selected.SymbolValues[0]); !ok || string(got) != "after" {
		t.Errorf("symbol = %q, %v", got, ok)
	}
	if selected.IntegerValues[0] != -7 || selected.Boolean(0, 0) || selected.TimestampValues[0] != 42 {
		t.Fatalf("typed values = %v %#x %v", selected.IntegerValues, selected.BooleanValues, selected.TimestampValues)
	}
	if !selected.Present(3, 0) {
		t.Error("false Boolean override did not mark field present")
	}
	if selected.Present(5, 0) {
		t.Error("false presence override left field present")
	}
}

func TestSimulateAppliesPolicyDerivedChanges(t *testing.T) {
	tests := []struct {
		args     []string
		request  string
		decision string
	}{
		{[]string{"simulate", "R3", "--set", "environment.usage=standard"}, "R3", "Approve"},
		{[]string{"simulate", "R2", "--set", "action.type=aggregate_analysis", "--set", "action.output=aggregate_counts"}, "R2", "Approve"},
	}
	for _, tc := range tests {
		code, stdout, stderr := runCLI(t, tc.args...)
		if code != 0 {
			t.Fatalf("Execute(%q) = %d, want 0; stderr=%q", tc.args, code, stderr)
		}
		if strings.Count(stdout, `"request_id":`) != 1 || !strings.Contains(stdout, `"request_id": "`+tc.request+`"`) ||
			!strings.Contains(stdout, `"decision": "`+tc.decision+`"`) {
			t.Fatalf("stdout = %q, want one %s %s result", stdout, tc.request, tc.decision)
		}
		if stderr != "" {
			t.Fatalf("stderr = %q, want empty", stderr)
		}
	}
}

func TestSimulateRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"simulate", "R1"},
		{"simulate", "r1", "--set", "action.output=x"},
		{"simulate", "R9", "--set", "action.output=x"},
		{"simulate", "R1", "--set", "missing"},
		{"simulate", "R1", "--set", "unknown.field=x"},
		{"simulate", "R1", "--set", "action.output=x", "--set", "action.output=y"},
		{"simulate", "R1", "extra", "--set", "action.output=x"},
	} {
		code, stdout, stderr := runCLI(t, args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "Usage:") {
			t.Errorf("Execute(%q) = (%d, %q, %q), want usage failure", args, code, stdout, stderr)
		}
	}
}

func TestSimulateDoesNotMutateEmbeddedInputs(t *testing.T) {
	beforeCode, before, beforeErr := runCLI(t, "evaluate")
	if beforeCode != 0 || beforeErr != "" {
		t.Fatalf("evaluate before = (%d, %q)", beforeCode, beforeErr)
	}
	code, _, stderr := runCLI(t, "simulate", "R3", "--set", "environment.usage=standard")
	if code != 0 || stderr != "" {
		t.Fatalf("simulate = (%d, %q)", code, stderr)
	}
	afterCode, after, afterErr := runCLI(t, "evaluate")
	if afterCode != 0 || afterErr != "" || after != before {
		t.Fatalf("evaluate after = (%d, %q), output changed=%v", afterCode, afterErr, after != before)
	}
}

func TestSimulateUsesExternalInputsAndHandlesWriterFailure(t *testing.T) {
	deps := productTestDependencies()
	code, stdout, stderr := runCLIWithDependencies(t, deps,
		"simulate", "R3", "--set", "environment.usage=standard",
		"--policy", "valid-policy.json", "--requests", "requests.json", "--evidence", "evidence.json")
	if code != 0 || !strings.Contains(stdout, `"engine_version": "test-engine"`) || stderr != "" {
		t.Fatalf("simulate external = (%d, %q, %q)", code, stdout, stderr)
	}

	var errOut bytes.Buffer
	code = executeWithDependencies(
		[]string{"simulate", "R3", "--set", "environment.usage=standard"},
		bytes.NewReader(nil), errorWriter{}, &errOut, deps,
	)
	if code != 1 {
		t.Fatalf("simulate with failing stdout = %d, want 1", code)
	}
}
