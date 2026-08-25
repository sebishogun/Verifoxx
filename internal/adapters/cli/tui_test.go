package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	postgresadapter "github.com/sebishogun/verifoxx/internal/adapters/postgres"
	tuiadapter "github.com/sebishogun/verifoxx/internal/adapters/tui"
	"github.com/sebishogun/verifoxx/internal/config"
	"github.com/sebishogun/verifoxx/internal/debug"
	"github.com/sebishogun/verifoxx/internal/graphview"
)

func TestRootExposesSemanticTUICommand(t *testing.T) {
	code, stdout, stderr := runCLI(t, "tui", "--help")
	if code != 0 {
		t.Fatalf("tui --help = %d, want 0; stderr=%q", code, stderr)
	}
	for _, required := range []string{"--socket", "--browser", "--policy", "--requests", "--evidence"} {
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
	if _, err := buildASTGraph(decoded.document, compiled); err != nil {
		t.Fatalf("buildASTGraph() error = %v", err)
	}
	if _, err := buildProgramGraph(compiled); err != nil {
		t.Fatalf("buildProgramGraph() error = %v; instructions=%d operand starts=%d counts=%d operands=%d", err,
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
	if err := graphview.Validate(&data.AST, graphview.DefaultLimits()); err != nil {
		t.Fatalf("AST graph validation error = %v", err)
	}
	if err := graphview.Validate(&data.Program, graphview.DefaultLimits()); err != nil {
		t.Fatalf("program graph validation error = %v", err)
	}
	if len(data.AST.Labels) <= decoded.document.Len() || len(data.Program.Labels) <= compiled.InstructionCount() {
		t.Fatalf("semantic graph nodes = (%d, %d), want more than source rows (%d, %d)",
			len(data.AST.Labels), len(data.Program.Labels), decoded.document.Len(), compiled.InstructionCount())
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

func TestTUIRunsWithEmbeddedSourcesAndDefaultSocket(t *testing.T) {
	deps := productTestDependencies()
	called := false
	deps.runTUI = func(ctx context.Context, options tuiRunOptions, input sources, stdin io.Reader, stdout io.Writer) error {
		called = true
		if ctx == nil || stdin == nil || stdout == nil {
			t.Fatal("TUI runner received a nil dependency")
		}
		if options.socketPath != ".verifoxx/debug.sock" || options.browser {
			t.Fatalf("options = %+v, want default socket without browser", options)
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

func TestTUICommandPassesOptionalPersistedHistoryConfiguration(t *testing.T) {
	deps := productTestDependencies()
	databaseURL := config.SecretURL("postgres://history:secret@database/verifoxx")
	deps.databaseURL = func() config.SecretURL { return databaseURL }
	called := false
	deps.runTUI = func(_ context.Context, options tuiRunOptions, _ sources, _ io.Reader, _ io.Writer) error {
		called = true
		if options.databaseURL.Reveal() != databaseURL.Reveal() {
			t.Fatalf("database URL = %s, want configured secret", options.databaseURL)
		}
		return nil
	}

	code, stdout, stderr := runCLIWithDependencies(t, deps, "tui")
	if code != 0 || !called || stdout != "" || stderr != "" {
		t.Fatalf("tui history config = (%d, called=%v, stdout=%q, stderr=%q)", code, called, stdout, stderr)
	}
	if strings.Contains(stdout+stderr, "secret") {
		t.Fatalf("TUI output exposed database credentials: %q", stdout+stderr)
	}
}

func TestTUIHistoryLoaderMapsPersistedRowsAndSanitizesFailures(t *testing.T) {
	now := time.Date(2026, time.August, 24, 12, 30, 0, 0, time.UTC)
	store := &tuiHistoryStoreStub{entries: []postgresadapter.DecisionHistoryEntry{
		{CompletedAt: now, Policy: "policy", Version: "2.1.0", Decision: "Revise"},
		{CompletedAt: now.Add(-time.Hour), Policy: "policy", Version: "2.0.0", Decision: "Approve"},
	}}
	loader := &tuiHistoryLoader{store: store}
	entries, err := loader.LoadHistory(context.Background(), tuiadapter.RequestItem{ID: 2, Name: "R2"})
	if err != nil {
		t.Fatalf("LoadHistory() error = %v", err)
	}
	if store.requestKey != "R2" || len(entries) != 2 || entries[0].Version != "2.0.0" ||
		entries[1].At != now || entries[1].Policy != "policy" || entries[1].Version != "2.1.0" ||
		entries[1].Decision != "Revise" {
		t.Fatalf("LoadHistory() request=%q entries=%+v", store.requestKey, entries)
	}

	store.err = errors.New("postgres://history:super-secret@database/verifoxx")
	if _, err := loader.LoadHistory(context.Background(), tuiadapter.RequestItem{ID: 2, Name: "R2"}); !errors.Is(err, errPersistedHistoryUnavailable) || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("sanitized history error = %v", err)
	}
}

func TestTUIHistoryLoaderIsLazyAndClosesItsPool(t *testing.T) {
	loader := newTUIHistoryLoader(config.SecretURL("postgres://history:secret@database/verifoxx"))
	if loader == nil || loader.store != nil || loader.pool != nil {
		t.Fatalf("new history loader is not lazy: %+v", loader)
	}
	pool := &tuiHistoryPoolStub{}
	loader.pool = pool
	loader.store = &tuiHistoryStoreStub{}
	loader.Close()
	if !pool.closed || loader.pool != nil || loader.store != nil {
		t.Fatalf("history loader close = pool closed %v, pool %v, store %v", pool.closed, loader.pool, loader.store)
	}

	loader = newTUIHistoryLoader(config.SecretURL("postgres://history:super-secret@%"))
	if _, err := loader.LoadHistory(context.Background(), tuiadapter.RequestItem{ID: 1, Name: "R1"}); !errors.Is(err, errPersistedHistoryUnavailable) || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("history parse error = %v", err)
	}
}

func TestTUIBrowserFlagReachesRunner(t *testing.T) {
	deps := productTestDependencies()
	opened := false
	deps.openBrowser = func(context.Context, string) error {
		opened = true
		return nil
	}
	called := false
	deps.runTUI = func(ctx context.Context, options tuiRunOptions, _ sources, _ io.Reader, _ io.Writer) error {
		called = true
		if !options.browser || options.openBrowser == nil {
			t.Fatalf("browser options = %+v", options)
		}
		return options.openBrowser(ctx, "http://127.0.0.1:1234")
	}
	code, stdout, stderr := runCLIWithDependencies(t, deps, "tui", "--browser")
	if code != 0 || stdout != "" || stderr != "" || !called || !opened {
		t.Fatalf("tui --browser = (%d,%q,%q,called=%v,opened=%v)", code, stdout, stderr, called, opened)
	}
}

func TestTUIRejectsSourceFromInteractiveStdin(t *testing.T) {
	deps := productTestDependencies()
	called := false
	deps.runTUI = func(context.Context, tuiRunOptions, sources, io.Reader, io.Writer) error {
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
	err := runSemanticTUI(ctx, tuiRunOptions{socketPath: filepath.Join(t.TempDir(), "missing.sock")}, sources{
		policy: []byte("{"), requests: []byte("{}"), evidence: []byte("{}"),
	}, strings.NewReader("q"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "decode policy") {
		t.Fatalf("runSemanticTUI() error = %v, want policy decode failure", err)
	}
}

func TestSemanticTUIRunsAgainstDebugSocket(t *testing.T) {
	deps := productTestDependencies()
	inputs := sources{policy: []byte(deps.policy), requests: []byte(deps.requests), evidence: []byte(deps.evidence)}
	socket := startSemanticTestServer(t, inputs)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := runSemanticTUI(ctx, tuiRunOptions{socketPath: socket}, inputs, strings.NewReader("q"), &output); err != nil {
		t.Fatalf("runSemanticTUI() error = %v", err)
	}
	if !strings.Contains(output.String(), "REQUESTS") || !strings.Contains(output.String(), "R1 Approve") {
		t.Fatalf("TUI output does not contain request pane: %q", output.String())
	}
	if !strings.Contains(output.String(), "\x1b[?1049h") || !strings.Contains(output.String(), "\x1b[?1049l") {
		t.Fatalf("TUI output does not enter and leave the alternate screen: %q", output.String())
	}
}

func TestTUIProgramOutputExposesTrackedTerminalDescriptor(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "terminal-output")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	tracked := &trackingWriter{w: file}

	output := tuiProgramOutput(tracked)
	terminal, ok := output.(interface {
		io.ReadWriteCloser
		Fd() uintptr
	})
	if !ok {
		t.Fatalf("tuiProgramOutput(%T) = %T, want descriptor-bearing output", tracked, output)
	}
	if terminal.Fd() != file.Fd() {
		t.Fatalf("terminal descriptor = %d, want %d", terminal.Fd(), file.Fd())
	}
	if _, err := output.Write([]byte("frame")); err != nil || tracked.err != nil {
		t.Fatalf("tracked terminal write error = %v, tracking error = %v", err, tracked.err)
	}
	if err := terminal.Close(); err != nil {
		t.Fatalf("terminal output Close() error = %v", err)
	}
	if _, err := file.Write([]byte(" still open")); err != nil {
		t.Fatalf("terminal output closed caller-owned file: %v", err)
	}

	var buffer bytes.Buffer
	if output := tuiProgramOutput(&buffer); output != &buffer {
		t.Fatalf("non-terminal output = %T, want original *bytes.Buffer", output)
	}
}

func TestSemanticTUIBrowserOpenerFailureKeepsDebuggerRunning(t *testing.T) {
	deps := productTestDependencies()
	inputs := sources{policy: []byte(deps.policy), requests: []byte(deps.requests), evidence: []byte(deps.evidence)}
	socket := startSemanticTestServer(t, inputs)
	var openedURL string
	options := tuiRunOptions{
		socketPath: socket,
		browser:    true,
		openBrowser: func(_ context.Context, address string) error {
			openedURL = address
			response, err := (&http.Client{Timeout: time.Second}).Get(address)
			if err != nil {
				t.Fatalf("browser was not available to opener: %v", err)
			}
			body, readErr := io.ReadAll(response.Body)
			closeErr := response.Body.Close()
			if response.StatusCode != http.StatusOK || readErr != nil || closeErr != nil ||
				!bytes.Contains(body, []byte(`id="ast-graph"`)) {
				t.Fatalf("browser opener response = status %d read=%v close=%v bytes=%d",
					response.StatusCode, readErr, closeErr, len(body))
			}
			return errors.New("desktop unavailable")
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var output bytes.Buffer
	if err := runSemanticTUI(ctx, options, inputs, strings.NewReader("q"), &output); err != nil {
		t.Fatalf("runSemanticTUI(browser) error = %v", err)
	}
	if !strings.HasPrefix(openedURL, "http://127.0.0.1:") ||
		!strings.Contains(output.String(), "Browser open failed; URL: http://127.0.0.1:") ||
		!strings.Contains(output.String(), "R1 Approve") {
		t.Fatalf("browser fallback URL=%q output=%q", openedURL, output.String())
	}
}

func startSemanticTestServer(t *testing.T, inputs sources) string {
	t.Helper()
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
	return socket
}

type tuiHistoryStoreStub struct {
	entries    []postgresadapter.DecisionHistoryEntry
	err        error
	requestKey string
}

type tuiHistoryPoolStub struct {
	closed bool
}

func (pool *tuiHistoryPoolStub) Close() { pool.closed = true }

func (store *tuiHistoryStoreStub) Load(
	_ context.Context,
	requestKey string,
	destination []postgresadapter.DecisionHistoryEntry,
) ([]postgresadapter.DecisionHistoryEntry, error) {
	store.requestKey = requestKey
	if store.err != nil {
		return destination, store.err
	}
	return append(destination, store.entries...), nil
}
