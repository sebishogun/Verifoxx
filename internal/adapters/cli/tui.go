package cli

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"

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

func newTUICommand(deps dependencies) *cobra.Command {
	var flags sourceFlags
	var socketPath string
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
			return operationalError(deps.runTUI(cmd.Context(), socketPath, inputs, cmd.InOrStdin(), cmd.OutOrStdout()))
		},
	}
	cmd.Flags().StringVar(&socketPath, "socket", ".verifoxx/debug.sock", "semantic debug Unix socket path")
	bindSourceFlags(cmd, &flags, sourceAll)
	return cmd
}

func runSemanticTUI(ctx context.Context, socketPath string, inputs sources, stdin io.Reader, stdout io.Writer) error {
	if ctx == nil || socketPath == "" || stdin == nil || stdout == nil {
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
	client, err := debug.DialClient(ctx, socketPath, debug.DefaultTransportConfig())
	if err != nil {
		return pipelineFailure("connect semantic debugger", err)
	}
	defer client.Close()
	model, err := tuiadapter.NewModel(client, nil, data)
	if err != nil {
		return pipelineFailure("prepare tui", err)
	}
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(stdin), tea.WithOutput(stdout))
	if _, err := program.Run(); err != nil {
		return pipelineFailure("run tui", err)
	}
	return nil
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

	astGraph, err := tuiASTGraph(document)
	if err != nil {
		return tuiadapter.Data{}, err
	}
	programGraph, err := tuiProgramGraph(compiled)
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

func tuiASTGraph(document *ast.Document) (tuiadapter.Graph, error) {
	nodes := document.Len()
	if nodes == 0 {
		return tuiadapter.Graph{}, errInvalidTUIData
	}
	graph := tuiadapter.Graph{
		Labels:      make([]string, nodes),
		ChildStarts: make([]uint32, nodes),
		ChildCounts: make([]uint16, nodes),
		Children:    make([]uint32, 0, len(document.ChildNodeIDs)+len(document.NotChildren)),
	}
	for row, kind := range document.NodeKinds {
		id := schema.NodeID(row + 1)
		graph.ChildStarts[row] = uint32(len(graph.Children))
		graph.Labels[row] = astNodeLabel(document, id, kind)
		switch kind {
		case ast.NodeKindAll, ast.NodeKindAny:
			children, ok := document.GroupChildren(id)
			if !ok {
				return tuiadapter.Graph{}, errInvalidTUIData
			}
			graph.ChildCounts[row] = uint16(len(children))
			for _, child := range children {
				graph.Children = append(graph.Children, uint32(child))
			}
		case ast.NodeKindNot:
			child, ok := document.NotChild(id)
			if !ok {
				return tuiadapter.Graph{}, errInvalidTUIData
			}
			graph.ChildCounts[row] = 1
			graph.Children = append(graph.Children, uint32(child))
		}
	}
	seen := make([]bool, nodes)
	for _, id := range document.RequirementApplicabilityRoots {
		graph.Roots = appendTUIRoot(graph.Roots, seen, uint32(id))
	}
	for _, id := range document.ClauseAssertionRoots {
		graph.Roots = appendTUIRoot(graph.Roots, seen, uint32(id))
	}
	for _, id := range document.ClauseEvidenceNodeIDs {
		graph.Roots = appendTUIRoot(graph.Roots, seen, uint32(id))
	}
	if len(graph.Roots) == 0 {
		return tuiadapter.Graph{}, errInvalidTUIData
	}
	return graph, nil
}

func tuiProgramGraph(compiled *program.Program) (tuiadapter.Graph, error) {
	nodes := compiled.InstructionCount()
	if nodes == 0 || len(compiled.OperandStarts) != nodes || len(compiled.OperandCounts) != nodes {
		return tuiadapter.Graph{}, errInvalidTUIData
	}
	graph := tuiadapter.Graph{
		Labels:      make([]string, nodes),
		ChildStarts: make([]uint32, nodes),
		ChildCounts: make([]uint16, nodes),
		Children:    make([]uint32, 0, len(compiled.Operands)),
	}
	for row, opcode := range compiled.Opcodes {
		start := uint64(compiled.OperandStarts[row])
		count := uint64(compiled.OperandCounts[row])
		if start+count > uint64(len(compiled.Operands)) {
			return tuiadapter.Graph{}, errInvalidTUIData
		}
		graph.Labels[row] = programOpcodeName(opcode)
		graph.ChildStarts[row] = uint32(len(graph.Children))
		graph.ChildCounts[row] = uint16(count)
		for _, child := range compiled.Operands[int(start):int(start+count)] {
			graph.Children = append(graph.Children, uint32(child))
		}
	}
	seen := make([]bool, nodes)
	for _, id := range compiled.RequirementRoots {
		graph.Roots = appendTUIRoot(graph.Roots, seen, uint32(id))
	}
	for _, id := range compiled.ClauseAssertionRoots {
		graph.Roots = appendTUIRoot(graph.Roots, seen, uint32(id))
	}
	for _, id := range compiled.ClauseEvidenceIDs {
		graph.Roots = appendTUIRoot(graph.Roots, seen, uint32(id))
	}
	if len(graph.Roots) == 0 {
		return tuiadapter.Graph{}, errInvalidTUIData
	}
	return graph, nil
}

func appendTUIRoot(roots []uint32, seen []bool, id uint32) []uint32 {
	if id == 0 || uint64(id) > uint64(len(seen)) || seen[id-1] {
		return roots
	}
	seen[id-1] = true
	return append(roots, id)
}

func astNodeLabel(document *ast.Document, id schema.NodeID, kind ast.NodeKind) string {
	switch kind {
	case ast.NodeKindCompare:
		_, op, _, ok := document.Compare(id)
		if !ok {
			return "invalid"
		}
		return "compare:" + compareOpName(op)
	case ast.NodeKindAll:
		return "all"
	case ast.NodeKindAny:
		return "any"
	case ast.NodeKindNot:
		return "not"
	case ast.NodeKindEvidence:
		return "evidence"
	default:
		return "invalid"
	}
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
