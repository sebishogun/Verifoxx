package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sebishogun/verifoxx/internal/debug"
	"github.com/sebishogun/verifoxx/internal/schema"
)

func TestBrowserServesLoopbackHTMLAndBoundedState(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	browser, err := StartBrowser(ctx, DefaultBrowserConfig(), testData())
	if err != nil {
		t.Fatalf("StartBrowser() error = %v", err)
	}
	t.Cleanup(func() { _ = browser.Close() })

	parsed, err := url.Parse(browser.URL())
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" {
		t.Fatalf("browser URL = %q, error=%v", browser.URL(), err)
	}
	response, err := browserClient().Get(browser.URL() + "/")
	if err != nil {
		t.Fatalf("GET / error = %v", err)
	}
	document, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read browser HTML: read=%v close=%v", readErr, closeErr)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Security-Policy") == "" ||
		response.Header.Get("X-Content-Type-Options") != "nosniff" ||
		response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("browser response = status %d, headers=%v", response.StatusCode, response.Header)
	}
	for _, required := range []string{
		`id="ast-graph"`, `id="program-graph"`, `id="live-state-label"`,
		`fetch('/state'`, "AST", "Program",
	} {
		if !bytes.Contains(document, []byte(required)) {
			t.Errorf("browser HTML lacks %q", required)
		}
	}
	if bytes.Contains(document, []byte("local review")) || bytes.Contains(document, []byte("remote disclosure")) {
		t.Fatal("browser HTML contains protected request display text")
	}

	model := newTestModel(t, &stubTarget{}, nil)
	model.selectedRequest = 1
	model.graphMode = graphProgram
	model.state = debug.State{
		Positive: []uint64{0}, Negative: []uint64{0b10},
		Instruction: 3, Node: 2, Rows: 2,
	}
	model.breakpoints = append(model.breakpoints, breakpointBinding{node: 2, id: 7})
	model.watches = append(model.watches, watchBinding{instruction: 3, id: 9, row: 1})
	if err := model.AttachBrowser(browser, "Browser: "+browser.URL()); err != nil {
		t.Fatalf("AttachBrowser() error = %v", err)
	}

	state := readBrowserState(t, browser.URL()+"/state")
	if state.Mode != "program" || state.CurrentNode != 2 || state.CurrentInstruction != 3 ||
		state.SelectedRow != 1 || state.RequestID != 2 || state.Truth != "false" || state.Generation == 0 {
		t.Fatalf("browser state = %+v", state)
	}
	if len(state.Breakpoints) != 1 || state.Breakpoints[0].ID != 7 || state.Breakpoints[0].Node != 2 {
		t.Fatalf("browser breakpoints = %+v", state.Breakpoints)
	}
	if len(state.Watches) != 1 || state.Watches[0].ID != 9 ||
		state.Watches[0].Instruction != 3 || state.Watches[0].Row != 1 {
		t.Fatalf("browser watches = %+v", state.Watches)
	}
}

func TestBrowserModelPublishesTransitionsWithoutRebuildingGraphs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	browser, err := StartBrowser(ctx, DefaultBrowserConfig(), testData())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = browser.Close() })
	document := append([]byte(nil), browser.document...)
	documentAddress := &browser.document[0]

	model := newTestModel(t, &stubTarget{}, nil)
	if err := model.AttachBrowser(browser, "Browser: "+browser.URL()); err != nil {
		t.Fatal(err)
	}
	model = updateModel(t, model, runeKey('p'))
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	model = updateModel(t, model, stateMessage{
		state:    debug.State{Instruction: 3, Node: 2, Rows: 2, Positive: []uint64{0b10}},
		sequence: 1,
		action:   actionStepInstruction,
	})
	model = updateModel(t, model, breakpointMessage{node: 2, id: 7})
	model = updateModel(t, model, watchMessage{instruction: 3, id: 9, row: 1})

	state := readBrowserState(t, browser.URL()+"/state")
	if state.Mode != "program" || state.SelectedRow != 1 || state.Truth != "true" ||
		len(state.Breakpoints) != 1 || len(state.Watches) != 1 {
		t.Fatalf("published transition state = %+v", state)
	}
	if &browser.document[0] != documentAddress || !bytes.Equal(browser.document, document) {
		t.Fatal("state publication rebuilt the static graph document")
	}
	if allocations := testing.AllocsPerRun(100, func() { model.publishBrowserState() }); allocations != 0 {
		t.Fatalf("warmed browser publication = %.2f allocs/run, want 0", allocations)
	}
}

func TestBrowserRejectsNonIPv4LoopbackAndStopsOnCancellation(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", "localhost:0", "[::1]:0", "192.0.2.1:0"} {
		if browser, err := StartBrowser(context.Background(), BrowserConfig{Address: address}, testData()); err == nil {
			_ = browser.Close()
			t.Errorf("StartBrowser(%q) succeeded", address)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	browser, err := StartBrowser(ctx, DefaultBrowserConfig(), testData())
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-browser.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("browser did not stop after cancellation")
	}
	if _, err := browserClient().Get(browser.URL() + "/state"); err == nil {
		t.Fatal("browser accepted a request after cancellation")
	}
}

func browserClient() *http.Client {
	return &http.Client{Timeout: time.Second}
}

type browserStateJSON struct {
	Mode               string                  `json:"mode"`
	Truth              string                  `json:"truth"`
	CurrentNode        schema.NodeID           `json:"current_node"`
	CurrentInstruction schema.InstructionID    `json:"current_instruction"`
	SelectedRow        uint32                  `json:"selected_row"`
	RequestID          schema.RequestID        `json:"request_id"`
	Generation         uint64                  `json:"generation"`
	Breakpoints        []browserBreakpointJSON `json:"breakpoints"`
	Watches            []browserWatchJSON      `json:"watches"`
}

type browserBreakpointJSON struct {
	ID   debug.BreakpointID `json:"id"`
	Node schema.NodeID      `json:"node"`
}

type browserWatchJSON struct {
	ID          debug.WatchID        `json:"id"`
	Instruction schema.InstructionID `json:"instruction"`
	Row         uint32               `json:"row"`
}

func readBrowserState(t *testing.T, address string) browserStateJSON {
	t.Helper()
	response, err := browserClient().Get(address)
	if err != nil {
		t.Fatalf("GET state error = %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxBrowserStateBytes+1))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || len(body) > MaxBrowserStateBytes ||
		response.Header.Get("Content-Type") != "application/json" ||
		response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("state response = status %d bytes=%d headers=%v", response.StatusCode, len(body), response.Header)
	}
	var state browserStateJSON
	if err := json.Unmarshal(body, &state); err != nil {
		t.Fatalf("decode state: %v; body=%s", err, strings.TrimSpace(string(body)))
	}
	return state
}
