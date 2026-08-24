package cli

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	tuiadapter "github.com/sebishogun/verifoxx/internal/adapters/tui"
	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/debug"
	"github.com/sebishogun/verifoxx/internal/eval"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/result"
	"github.com/sebishogun/verifoxx/internal/schema"
)

var errInvalidTUIData = errors.New("tui: invalid semantic display data")

type tuiRunOptions struct {
	openBrowser func(context.Context, string) error
	socketPath  string
	browser     bool
}

func newTUICommand(deps dependencies) *cobra.Command {
	var flags sourceFlags
	var socketPath string
	var browser bool
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Open the semantic debugger TUI",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flags.policyPath == "-" || flags.requestPath == "-" || flags.evidencePath == "-" {
				return usageError(errors.New("tui source inputs cannot read from interactive stdin"))
			}
			inputs, err := loadSources(flags, cmd.InOrStdin(), deps, sourceAll)
			if err != nil {
				return classifyCommandError(err)
			}
			if deps.runTUI == nil {
				return operationalError(errors.New("semantic TUI runner unavailable"))
			}
			return operationalError(deps.runTUI(cmd.Context(), tuiRunOptions{
				openBrowser: deps.openBrowser,
				socketPath:  socketPath,
				browser:     browser,
			}, inputs, cmd.InOrStdin(), cmd.OutOrStdout()))
		},
	}
	cmd.Flags().StringVar(&socketPath, "socket", ".verifoxx/debug.sock", "semantic debug Unix socket path")
	cmd.Flags().BoolVar(&browser, "browser", false, "open a synchronized IPv4-loopback graph viewer")
	bindSourceFlags(cmd, &flags, sourceAll)
	return cmd
}

func runSemanticTUI(ctx context.Context, options tuiRunOptions, inputs sources, stdin io.Reader, stdout io.Writer) error {
	if ctx == nil || options.socketPath == "" || stdin == nil || stdout == nil {
		return errInvalidTUIData
	}
	var pipeline engine
	decoded, err := pipeline.decodePolicy(inputs.policy)
	if err != nil {
		return err
	}
	compiled, err := pipeline.lowerPolicy(decoded)
	if err != nil {
		return err
	}
	batch, err := pipeline.decodeBatch(compiled, inputs.requests, inputs.evidence)
	if err != nil {
		return err
	}
	decisions, err := pipeline.evaluate(compiled, batch)
	if err != nil {
		return err
	}
	data, err := buildTUIData(decoded.document, compiled, batch, decisions, pipeline.batchBuilder.Symbol)
	if err != nil {
		return pipelineFailure("prepare tui", err)
	}
	client, err := debug.DialClient(ctx, options.socketPath, debug.DefaultTransportConfig())
	if err != nil {
		return pipelineFailure("connect semantic debugger", err)
	}
	defer client.Close()
	model, err := tuiadapter.NewModel(client, nil, data)
	if err != nil {
		return pipelineFailure("prepare tui", err)
	}
	var browser *tuiadapter.Browser
	if options.browser {
		browser, err = tuiadapter.StartBrowser(ctx, tuiadapter.DefaultBrowserConfig(), data)
		if err != nil {
			return pipelineFailure("start browser viewer", err)
		}
		status := "Browser: " + browser.URL()
		if options.openBrowser == nil || options.openBrowser(ctx, browser.URL()) != nil {
			status = "Browser open failed; URL: " + browser.URL()
		}
		if err := model.AttachBrowser(browser, status); err != nil {
			_ = browser.Close()
			return pipelineFailure("prepare browser viewer", err)
		}
	}
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(stdin), tea.WithOutput(stdout))
	_, runErr := program.Run()
	if browser != nil {
		if closeErr := browser.Close(); runErr == nil && closeErr != nil {
			return pipelineFailure("stop browser viewer", closeErr)
		}
	}
	if runErr != nil {
		return pipelineFailure("run tui", runErr)
	}
	return nil
}

func openBrowserURL(ctx context.Context, address string) error {
	if ctx == nil || address == "" {
		return errInvalidTUIData
	}
	var executable string
	switch runtime.GOOS {
	case "linux":
		executable = "xdg-open"
	case "darwin":
		executable = "open"
	default:
		return errors.New("browser launch is unsupported on this platform")
	}
	openCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(openCtx, executable, address)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run()
}

func buildTUIData(
	document *ast.Document,
	compiled *program.Program,
	batch eval.Batch,
	decisions *result.Batch,
	resolveSymbol func(schema.SymbolID) ([]byte, bool),
) (tuiadapter.Data, error) {
	if document == nil || compiled == nil || decisions == nil || resolveSymbol == nil || batch.Rows == 0 ||
		uint64(batch.Rows) != uint64(len(batch.RequestIDs)) ||
		uint64(batch.Rows) != uint64(len(decisions.OutcomeIDs)) {
		return tuiadapter.Data{}, errInvalidTUIData
	}

	requests := make([]tuiadapter.RequestItem, len(batch.RequestIDs))
	for row, requestID := range batch.RequestIDs {
		outcome, ok := compiled.Outcomes.Lookup(decisions.OutcomeIDs[row])
		if requestID == 0 || !ok {
			return tuiadapter.Data{}, errInvalidTUIData
		}
		outcomeName, ok := compiled.Symbol(outcome.Name)
		if !ok {
			return tuiadapter.Data{}, errInvalidTUIData
		}
		text, ok := tuiRequestText(compiled, batch, uint32(row), resolveSymbol)
		if !ok {
			return tuiadapter.Data{}, errInvalidTUIData
		}
		requests[row] = tuiadapter.RequestItem{
			ID:       requestID,
			Name:     "R" + strconv.FormatUint(uint64(requestID), 10),
			Decision: string(outcomeName),
			Text:     text,
		}
	}

	astGraph, err := buildASTGraph(document, compiled)
	if err != nil {
		return tuiadapter.Data{}, err
	}
	programGraph, err := buildProgramGraph(compiled)
	if err != nil {
		return tuiadapter.Data{}, err
	}
	return tuiadapter.Data{Requests: requests, AST: astGraph, Program: programGraph}, nil
}

func tuiRequestText(
	compiled *program.Program,
	batch eval.Batch,
	row uint32,
	resolveSymbol func(schema.SymbolID) ([]byte, bool),
) (string, bool) {
	var text strings.Builder
	for index, nameID := range compiled.FieldNames {
		field := schema.FieldID(index + 1)
		if !batch.Present(field, row) {
			continue
		}
		name, ok := compiled.Symbol(nameID)
		if !ok {
			return "", false
		}
		kind, column, ok := compiled.FieldIndex.Lookup(field)
		if !ok {
			return "", false
		}
		if text.Len() != 0 {
			text.WriteByte('\n')
		}
		text.Write(name)
		text.WriteByte('=')
		switch kind {
		case schema.ValueKindSymbol:
			valueID, exists := batch.Symbol(column, row)
			value, found := resolveSymbol(valueID)
			if !exists || !found {
				return "", false
			}
			text.WriteString(strconv.Quote(string(value)))
		case schema.ValueKindInteger:
			value, exists := batch.Integer(column, row)
			if !exists {
				return "", false
			}
			text.WriteString(strconv.FormatInt(value, 10))
		case schema.ValueKindBoolean:
			text.WriteString(strconv.FormatBool(batch.Boolean(column, row)))
		case schema.ValueKindTimestamp:
			value, exists := batch.Timestamp(column, row)
			if !exists {
				return "", false
			}
			text.WriteString(strconv.FormatInt(value, 10))
		case schema.ValueKindPresence:
			text.WriteString("present")
		default:
			return "", false
		}
	}
	if text.Len() == 0 {
		return "no materialized fields", true
	}
	value := text.String()
	if len(value) > tuiadapter.MaxRequestText {
		value = value[:tuiadapter.MaxRequestText-3] + "..."
	}
	return value, true
}

func compareOpName(op ast.CompareOp) string {
	switch op {
	case ast.CompareOpEqual:
		return "equal"
	case ast.CompareOpNotEqual:
		return "not_equal"
	case ast.CompareOpIn:
		return "in"
	case ast.CompareOpExists:
		return "exists"
	case ast.CompareOpLess:
		return "less"
	case ast.CompareOpLessEqual:
		return "less_equal"
	case ast.CompareOpGreater:
		return "greater"
	case ast.CompareOpGreaterEqual:
		return "greater_equal"
	default:
		return "invalid"
	}
}

func programOpcodeName(opcode program.Opcode) string {
	switch opcode {
	case program.OpcodeEqual:
		return "equal"
	case program.OpcodeNotEqual:
		return "not_equal"
	case program.OpcodeIn:
		return "in"
	case program.OpcodeExists:
		return "exists"
	case program.OpcodeLess:
		return "less"
	case program.OpcodeLessEqual:
		return "less_equal"
	case program.OpcodeGreater:
		return "greater"
	case program.OpcodeGreaterEqual:
		return "greater_equal"
	case program.OpcodeEvidence:
		return "evidence"
	case program.OpcodeAll:
		return "all"
	case program.OpcodeAny:
		return "any"
	case program.OpcodeNot:
		return "not"
	default:
		return "invalid"
	}
}
