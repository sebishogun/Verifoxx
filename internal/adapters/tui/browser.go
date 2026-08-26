package tui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/sebishogun/nornrune/internal/debug"
	"github.com/sebishogun/nornrune/internal/graphview"
	"github.com/sebishogun/nornrune/internal/schema"
)

const (
	MaxBrowserBindings   = 4096
	MaxBrowserStateBytes = 512 << 10
)

var ErrInvalidBrowser = errors.New("tui: invalid browser configuration")

// BrowserConfig bounds the loopback live-view server.
type BrowserConfig struct {
	Address string
}

// DefaultBrowserConfig selects an ephemeral IPv4-loopback port.
func DefaultBrowserConfig() BrowserConfig {
	return BrowserConfig{Address: "127.0.0.1:0"}
}

type browserBreakpointState struct {
	id   debug.BreakpointID
	node schema.NodeID
}

type browserWatchState struct {
	id          debug.WatchID
	instruction schema.InstructionID
	row         uint32
}

type browserState struct {
	generation         uint64
	currentNode        schema.NodeID
	currentInstruction schema.InstructionID
	requestID          schema.RequestID
	selectedRow        uint32
	breakpointCount    uint32
	watchCount         uint32
	mode               graphKind
	truth              debug.TruthState
	breakpoints        [MaxBrowserBindings]browserBreakpointState
	watches            [MaxBrowserBindings]browserWatchState
}

// Browser serves one immutable graph document and mutable bounded state from an
// ephemeral IPv4-loopback listener.
type Browser struct {
	server   *http.Server
	done     chan struct{}
	serveErr error
	url      string
	document []byte
	mu       sync.RWMutex
	state    browserState
}

// StartBrowser validates and pre-renders both graphs before binding a listener.
func StartBrowser(ctx context.Context, config BrowserConfig, data Data) (*Browser, error) {
	if ctx == nil {
		return nil, ErrInvalidBrowser
	}
	address, err := netip.ParseAddrPort(config.Address)
	if err != nil || !address.Addr().Is4() || !address.Addr().IsLoopback() {
		return nil, ErrInvalidBrowser
	}
	var renderer graphview.Renderer
	document, err := renderer.AppendLiveHTML(nil, &data.AST, &data.Program)
	if err != nil {
		return nil, fmt.Errorf("render browser graph: %w", err)
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", config.Address)
	if err != nil {
		return nil, fmt.Errorf("listen for browser graph: %w", err)
	}
	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok || !tcpAddress.IP.IsLoopback() || tcpAddress.IP.To4() == nil {
		_ = listener.Close()
		return nil, ErrInvalidBrowser
	}

	browser := &Browser{
		done:     make(chan struct{}),
		document: document,
		url:      "http://" + listener.Addr().String(),
	}
	browser.server = &http.Server{
		Handler:           browser,
		ReadHeaderTimeout: 2 * time.Second,
		IdleTimeout:       5 * time.Second,
		MaxHeaderBytes:    8 << 10,
	}
	go browser.serve(listener)
	go func() {
		select {
		case <-ctx.Done():
			_ = browser.server.Close()
		case <-browser.done:
		}
	}()
	return browser, nil
}

func (browser *Browser) serve(listener net.Listener) {
	err := browser.server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	browser.mu.Lock()
	browser.serveErr = err
	browser.mu.Unlock()
	close(browser.done)
}

// URL returns the IPv4-loopback viewer address.
func (browser *Browser) URL() string {
	if browser == nil {
		return ""
	}
	return browser.url
}

// Done closes when the loopback HTTP server has stopped.
func (browser *Browser) Done() <-chan struct{} {
	if browser == nil {
		return nil
	}
	return browser.done
}

// Close stops the loopback server and waits for its serve loop.
func (browser *Browser) Close() error {
	if browser == nil || browser.server == nil {
		return ErrInvalidBrowser
	}
	closeErr := browser.server.Close()
	<-browser.done
	browser.mu.RLock()
	serveErr := browser.serveErr
	browser.mu.RUnlock()
	if closeErr != nil {
		return closeErr
	}
	return serveErr
}

// ServeHTTP serves only the static graph and its bounded live state.
func (browser *Browser) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	setBrowserHeaders(response.Header())
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		response.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch request.URL.Path {
	case "/":
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		response.Header().Set("Content-Length", strconv.Itoa(len(browser.document)))
		if request.Method == http.MethodGet {
			_, _ = response.Write(browser.document)
		}
	case "/state":
		response.Header().Set("Content-Type", "application/json")
		body := browser.appendState(nil)
		if len(body) > MaxBrowserStateBytes {
			http.Error(response, "browser state exceeds limit", http.StatusInternalServerError)
			return
		}
		response.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if request.Method == http.MethodGet {
			_, _ = response.Write(body)
		}
	default:
		http.NotFound(response, request)
	}
}

func setBrowserHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'")
	header.Set("Cross-Origin-Opener-Policy", "same-origin")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

func (browser *Browser) publish(model *Model) {
	if browser == nil || model == nil {
		return
	}
	browser.mu.Lock()
	state := &browser.state
	state.generation++
	state.currentNode = model.state.Node
	state.currentInstruction = model.state.Instruction
	state.selectedRow = uint32(model.selectedRequest)
	state.requestID = 0
	if model.selectedRequest >= 0 && model.selectedRequest < len(model.data.Requests) {
		state.requestID = model.data.Requests[model.selectedRequest].ID
	}
	state.mode = model.graphMode
	state.truth = selectedTruth(&model.state, state.selectedRow)
	state.breakpointCount = uint32(min(len(model.breakpoints), MaxBrowserBindings))
	for row := uint32(0); row < state.breakpointCount; row++ {
		binding := model.breakpoints[row]
		state.breakpoints[row] = browserBreakpointState{id: binding.id, node: binding.node}
	}
	state.watchCount = uint32(min(len(model.watches), MaxBrowserBindings))
	for row := uint32(0); row < state.watchCount; row++ {
		binding := model.watches[row]
		state.watches[row] = browserWatchState{id: binding.id, instruction: binding.instruction, row: binding.row}
	}
	browser.mu.Unlock()
}

func selectedTruth(state *debug.State, row uint32) debug.TruthState {
	positive := rowMaskBit(state.Positive, row)
	negative := rowMaskBit(state.Negative, row)
	switch {
	case positive && negative:
		return debug.TruthBoth
	case positive:
		return debug.TruthTrue
	case negative:
		return debug.TruthFalse
	default:
		return debug.TruthNeither
	}
}

func (browser *Browser) appendState(dst []byte) []byte {
	browser.mu.RLock()
	state := &browser.state
	dst = append(dst, `{"mode":"`...)
	if state.mode == graphProgram {
		dst = append(dst, "program"...)
	} else {
		dst = append(dst, "ast"...)
	}
	dst = append(dst, `","truth":"`...)
	dst = append(dst, browserTruthName(state.truth)...)
	dst = append(dst, `","current_node":`...)
	dst = strconv.AppendUint(dst, uint64(state.currentNode), 10)
	dst = append(dst, `,"current_instruction":`...)
	dst = strconv.AppendUint(dst, uint64(state.currentInstruction), 10)
	dst = append(dst, `,"selected_row":`...)
	dst = strconv.AppendUint(dst, uint64(state.selectedRow), 10)
	dst = append(dst, `,"request_id":`...)
	dst = strconv.AppendUint(dst, uint64(state.requestID), 10)
	dst = append(dst, `,"generation":`...)
	dst = strconv.AppendUint(dst, state.generation, 10)
	dst = append(dst, `,"breakpoints":[`...)
	for row := uint32(0); row < state.breakpointCount; row++ {
		if row != 0 {
			dst = append(dst, ',')
		}
		breakpoint := state.breakpoints[row]
		dst = append(dst, `{"id":`...)
		dst = strconv.AppendUint(dst, uint64(breakpoint.id), 10)
		dst = append(dst, `,"node":`...)
		dst = strconv.AppendUint(dst, uint64(breakpoint.node), 10)
		dst = append(dst, '}')
	}
	dst = append(dst, `],"watches":[`...)
	for row := uint32(0); row < state.watchCount; row++ {
		if row != 0 {
			dst = append(dst, ',')
		}
		watch := state.watches[row]
		dst = append(dst, `{"id":`...)
		dst = strconv.AppendUint(dst, uint64(watch.id), 10)
		dst = append(dst, `,"instruction":`...)
		dst = strconv.AppendUint(dst, uint64(watch.instruction), 10)
		dst = append(dst, `,"row":`...)
		dst = strconv.AppendUint(dst, uint64(watch.row), 10)
		dst = append(dst, '}')
	}
	dst = append(dst, ']', '}', '\n')
	browser.mu.RUnlock()
	return dst
}

func browserTruthName(value debug.TruthState) string {
	switch value {
	case debug.TruthTrue:
		return "true"
	case debug.TruthFalse:
		return "false"
	case debug.TruthBoth:
		return "both"
	default:
		return "neither"
	}
}
