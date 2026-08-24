package cli

import (
	"bytes"
	"context"
	"io"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tuiadapter "github.com/sebishogun/verifoxx/internal/adapters/tui"
	"github.com/sebishogun/verifoxx/internal/debug"
)

func TestRootExposesSemanticTUICommand(t *testing.T) {
	code, stdout, stderr := runCLI(t, "tui", "--help")
	if code != 0 {
		t.Fatalf("tui --help = %d, want 0; stderr=%q", code, stderr)
	}
	for _, required := range []string{"--socket", "--policy", "--requests", "--evidence"} {
		if !strings.Contains(stdout, required) {
			t.Errorf("tui help does not contain %q: %q", required, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("tui --help stderr = %q, want empty", stderr)
	}
}

func TestBuildTUIDataRepresentsEmbeddedEvaluation(t *testing.T) {
	deps := productTestDependencies()
	var pipeline engine
	decoded, err := pipeline.decodePolicy([]byte(deps.policy))
	if err != nil {
		t.Fatalf("decodePolicy() error = %v", err)
	}
	compiled, err := pipeline.lowerPolicy(decoded)
	if err != nil {
		t.Fatalf("lowerPolicy() error = %v", err)
	}
	batch, err := pipeline.decodeBatch(compiled, []byte(deps.requests), []byte(deps.evidence))
	if err != nil {
		t.Fatalf("decodeBatch() error = %v", err)
	}
	decisions, err := pipeline.evaluate(compiled, batch)
	if err != nil {
		t.Fatalf("evaluate() error = %v", err)
	}
	for row := uint32(0); row < batch.Rows; row++ {
		if _, ok := tuiRequestText(compiled, batch, row, pipeline.batchBuilder.Symbol); !ok {
			t.Fatalf("tuiRequestText() rejected row %d", row)
		}
	}
	if _, err := tuiASTGraph(decoded.document); err != nil {
		t.Fatalf("tuiASTGraph() error = %v", err)
	}
	if _, err := tuiProgramGraph(compiled); err != nil {
		t.Fatalf("tuiProgramGraph() error = %v; instructions=%d operand starts=%d counts=%d operands=%d", err,
			compiled.InstructionCount(), len(compiled.OperandStarts), len(compiled.OperandCounts), len(compiled.Operands))
	}

	data, err := buildTUIData(decoded.document, compiled, batch, decisions, pipeline.batchBuilder.Symbol)
	if err != nil {
		t.Fatalf("buildTUIData() error = %v", err)
	}
	var names, outcomes []string
	for _, request := range data.Requests {
		names = append(names, request.Name)
		outcomes = append(outcomes, request.Decision)
		if request.Text == "" {
			t.Errorf("request %q has empty display text", request.Name)
		}
	}
	if want := []string{"R1", "R2", "R3", "R4", "R5"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("request names = %v, want %v", names, want)
	}
	if want := []string{"Approve", "Reject", "Revise", "Escalate", "Escalate"}; !reflect.DeepEqual(outcomes, want) {
		t.Fatalf("request decisions = %v, want %v", outcomes, want)
	}
	assertTUIDataGraph(t, "AST", data.AST.Labels, data.AST.ChildStarts, data.AST.ChildCounts, data.AST.Children, data.AST.Roots)
	assertTUIDataGraph(t, "program", data.Program.Labels, data.Program.ChildStarts, data.Program.ChildCounts, data.Program.Children, data.Program.Roots)
	if len(data.AST.Labels) != decoded.document.Len() || len(data.Program.Labels) != compiled.InstructionCount() {
		t.Fatalf("graph nodes = (%d, %d), want (%d, %d)", len(data.AST.Labels), len(data.Program.Labels), decoded.document.Len(), compiled.InstructionCount())
	}
}

func TestBuildTUIDataBoundsAndEscapesRequestText(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "terminal control", value: `\u001bexternal_partner`},
		{name: "long value", value: strings.Repeat("x", tuiadapter.MaxRequestText+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			deps := productTestDependencies()
			requests := strings.Replace(deps.requests, "external_partner", test.value, 1)
			var pipeline engine
			decoded, err := pipeline.decodePolicy([]byte(deps.policy))
			if err != nil {
				t.Fatalf("decodePolicy() error = %v", err)
			}
			compiled, err := pipeline.lowerPolicy(decoded)
			if err != nil {
				t.Fatalf("lowerPolicy() error = %v", err)
			}
			batch, err := pipeline.decodeBatch(compiled, []byte(requests), []byte(deps.evidence))
			if err != nil {
				t.Fatalf("decodeBatch() error = %v", err)
			}
			decisions, err := pipeline.evaluate(compiled, batch)
			if err != nil {
				t.Fatalf("evaluate() error = %v", err)
			}
			data, err := buildTUIData(decoded.document, compiled, batch, decisions, pipeline.batchBuilder.Symbol)
			if err != nil {
				t.Fatalf("buildTUIData() error = %v", err)
			}
			text := data.Requests[0].Text
			if len(text) > tuiadapter.MaxRequestText || strings.ContainsRune(text, '\x1b') {
				t.Fatalf("request text is not display-safe: length=%d text=%q", len(text), text)
			}
		})
	}
}

func assertTUIDataGraph(t *testing.T, name string, labels []string, starts []uint32, counts []uint16, children, roots []uint32) {
	t.Helper()
	if len(labels) == 0 || len(starts) != len(labels) || len(counts) != len(labels) || len(roots) == 0 {
		t.Fatalf("%s graph has invalid column lengths", name)
	}
	for row, label := range labels {
		if label == "" || uint64(starts[row])+uint64(counts[row]) > uint64(len(children)) {
			t.Fatalf("%s graph row %d is invalid", name, row)
		}
	}
}

func TestTUIRunsWithEmbeddedSourcesAndDefaultSocket(t *testing.T) {
	deps := productTestDependencies()
	called := false
	deps.runTUI = func(ctx context.Context, socket string, input sources, stdin io.Reader, stdout io.Writer) error {
		called = true
		if ctx == nil || stdin == nil || stdout == nil {
			t.Fatal("TUI runner received a nil dependency")
		}
		if socket != ".verifoxx/debug.sock" {
			t.Fatalf("socket = %q, want default", socket)
		}
		if string(input.policy) != deps.policy || string(input.requests) != deps.requests || string(input.evidence) != deps.evidence {
			t.Fatal("TUI runner did not receive embedded sources")
		}
		return nil
	}

	var stdout, stderr bytes.Buffer
	code := executeWithDependenciesContext(
		context.Background(), []string{"tui"}, strings.NewReader("q"), &stdout, &stderr, deps,
	)
	if code != 0 || !called || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("tui = (%d, called=%v, stdout=%q, stderr=%q), want success", code, called, stdout.String(), stderr.String())
	}
}

func TestTUIRejectsSourceFromInteractiveStdin(t *testing.T) {
	deps := productTestDependencies()
	called := false
	deps.runTUI = func(context.Context, string, sources, io.Reader, io.Writer) error {
		called = true
		return nil
	}

	var stdout, stderr bytes.Buffer
	code := executeWithDependenciesContext(
		context.Background(), []string{"tui", "--policy", "-"}, strings.NewReader(deps.policy), &stdout, &stderr, deps,
	)
	if code != 2 || called || stdout.Len() != 0 || !strings.Contains(stderr.String(), "stdin") {
		t.Fatalf("tui --policy - = (%d, called=%v, stdout=%q, stderr=%q), want stdin usage error",
			code, called, stdout.String(), stderr.String())
	}
}

func TestSemanticTUIRejectsMalformedPolicyBeforeDial(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := runSemanticTUI(ctx, filepath.Join(t.TempDir(), "missing.sock"), sources{
		policy: []byte("{"), requests: []byte("{}"), evidence: []byte("{}"),
	}, strings.NewReader("q"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "decode policy") {
		t.Fatalf("runSemanticTUI() error = %v, want policy decode failure", err)
	}
}

func TestSemanticTUIRunsAgainstDebugSocket(t *testing.T) {
	deps := productTestDependencies()
	inputs := sources{policy: []byte(deps.policy), requests: []byte(deps.requests), evidence: []byte(deps.evidence)}
	var worker engine
	compiled, err := worker.compilePolicy(inputs.policy)
	if err != nil {
		t.Fatalf("compilePolicy() error = %v", err)
	}
	batch, err := worker.decodeBatch(compiled, inputs.requests, inputs.evidence)
	if err != nil {
		t.Fatalf("decodeBatch() error = %v", err)
	}
	session, err := debug.NewSession(compiled, batch, debugWorkerConfig())
	if err != nil {
		t.Fatalf("debug.NewSession() error = %v", err)
	}
	socket := filepath.Join(t.TempDir(), "debug.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	server, err := debug.NewServer(session, debug.DefaultTransportConfig())
	if err != nil {
		t.Fatalf("debug.NewServer() error = %v", err)
	}
	serveCtx, stopServer := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(serveCtx, listener) }()
	t.Cleanup(func() {
		stopServer()
		if err := <-serveDone; err != nil {
			t.Errorf("debug server error = %v", err)
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := session.Close(closeCtx); err != nil {
			t.Errorf("debug session close error = %v", err)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := runSemanticTUI(ctx, socket, inputs, strings.NewReader("q"), &output); err != nil {
		t.Fatalf("runSemanticTUI() error = %v", err)
	}
	if !strings.Contains(output.String(), "REQUESTS") || !strings.Contains(output.String(), "R1 Approve") {
		t.Fatalf("TUI output does not contain request pane: %q", output.String())
	}
}
