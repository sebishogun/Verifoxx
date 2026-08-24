// Package graphview provides bounded semantic graph layout and rendering.
package graphview

import (
	"errors"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalidGraph = errors.New("graphview: invalid graph")
	ErrGraphLimit   = errors.New("graphview: graph exceeds limits")
	ErrGraphCycle   = errors.New("graphview: graph contains a cycle")
)

// NodeKind identifies one display-level semantic node.
type NodeKind uint8

const (
	NodeInvalid NodeKind = iota
	NodePolicy
	NodeRequirement
	NodeClause
	NodeCompare
	NodeAll
	NodeAny
	NodeNot
	NodeEvidence
	NodeRemediation
	NodeOutcome
	NodeInstruction
)

// Valid reports whether kind is a supported graph node kind.
func (kind NodeKind) Valid() bool { return kind >= NodePolicy && kind <= NodeInstruction }

func (kind NodeKind) String() string {
	switch kind {
	case NodePolicy:
		return "policy"
	case NodeRequirement:
		return "requirement"
	case NodeClause:
		return "clause"
	case NodeCompare:
		return "compare"
	case NodeAll:
		return "all"
	case NodeAny:
		return "any"
	case NodeNot:
		return "not"
	case NodeEvidence:
		return "evidence"
	case NodeRemediation:
		return "remediation"
	case NodeOutcome:
		return "outcome"
	case NodeInstruction:
		return "instruction"
	default:
		return "invalid"
	}
}

// EdgeKind identifies the semantic relationship carried by one edge.
type EdgeKind uint8

const (
	EdgeInvalid EdgeKind = iota
	EdgeContains
	EdgeApplies
	EdgeClause
	EdgeAssertion
	EdgeEvidence
	EdgeSatisfied
	EdgeFalse
	EdgeMissing
	EdgeStale
	EdgeUnclear
	EdgeUnverifiable
	EdgeConflict
	EdgeRemediation
	EdgeArgument
	EdgeOperand
)

// Valid reports whether kind is a supported semantic edge kind.
func (kind EdgeKind) Valid() bool { return kind >= EdgeContains && kind <= EdgeOperand }

func (kind EdgeKind) String() string {
	switch kind {
	case EdgeContains:
		return "contains"
	case EdgeApplies:
		return "applies"
	case EdgeClause:
		return "clause"
	case EdgeAssertion:
		return "assert"
	case EdgeEvidence:
		return "evidence"
	case EdgeSatisfied:
		return "satisfied"
	case EdgeFalse:
		return "false"
	case EdgeMissing:
		return "missing"
	case EdgeStale:
		return "stale"
	case EdgeUnclear:
		return "unclear"
	case EdgeUnverifiable:
		return "unverifiable"
	case EdgeConflict:
		return "conflict"
	case EdgeRemediation:
		return "remediation"
	case EdgeArgument:
		return "argument"
	case EdgeOperand:
		return "operand"
	default:
		return "invalid"
	}
}

// Graph stores one immutable semantic DAG in one-based SoA/CSR form.
type Graph struct {
	Labels       []string
	Details      []string
	Kinds        []NodeKind
	SourceStarts []uint32
	SourceEnds   []uint32
	EdgeStarts   []uint32
	EdgeCounts   []uint16
	Edges        []uint32
	EdgeKinds    []EdgeKind
	EdgeLabels   []string
	Roots        []uint32
	SourceLength uint32
}

// Limits bounds graph presentation data before layout or rendering.
type Limits struct {
	MaxNodes          int
	MaxEdges          int
	MaxRoots          int
	MaxLabelBytes     int
	MaxDetailBytes    int
	MaxEdgeLabelBytes int
}

// DefaultLimits returns the production semantic-debugger graph bounds.
func DefaultLimits() Limits {
	return Limits{
		MaxNodes:          16384,
		MaxEdges:          65536,
		MaxRoots:          16384,
		MaxLabelBytes:     256,
		MaxDetailBytes:    4096,
		MaxEdgeLabelBytes: 256,
	}
}

// Validate checks exact columns, display text, source ranges, IDs, and DAG shape.
func Validate(graph *Graph, limits Limits) error {
	var validator Validator
	return validator.Validate(graph, limits)
}

// Validator retains graph-validation scratch across calls.
type Validator struct {
	rootSeen []bool
	state    []uint8
	stack    []visitFrame
}

// Validate checks exact columns, display text, source ranges, IDs, and DAG shape.
func (validator *Validator) Validate(graph *Graph, limits Limits) error {
	if validator == nil || graph == nil {
		return ErrInvalidGraph
	}
	nodes := len(graph.Kinds)
	edges := len(graph.Edges)
	if nodes > limits.MaxNodes || edges > limits.MaxEdges || len(graph.Roots) > limits.MaxRoots {
		return ErrGraphLimit
	}
	if nodes == 0 || len(graph.Roots) == 0 ||
		len(graph.Labels) != nodes || len(graph.Details) != nodes ||
		len(graph.SourceStarts) != nodes || len(graph.SourceEnds) != nodes ||
		len(graph.EdgeStarts) != nodes || len(graph.EdgeCounts) != nodes ||
		len(graph.EdgeKinds) != edges || len(graph.EdgeLabels) != edges {
		return ErrInvalidGraph
	}
	for row := range nodes {
		if len(graph.Labels[row]) > limits.MaxLabelBytes || len(graph.Details[row]) > limits.MaxDetailBytes {
			return ErrGraphLimit
		}
	}
	for _, label := range graph.EdgeLabels {
		if len(label) > limits.MaxEdgeLabelBytes {
			return ErrGraphLimit
		}
	}

	var edge uint64
	for row := range nodes {
		start := uint64(graph.EdgeStarts[row])
		end := start + uint64(graph.EdgeCounts[row])
		if start != edge || end > uint64(edges) || !graph.Kinds[row].Valid() ||
			graph.Labels[row] == "" ||
			!validDisplayText(graph.Labels[row], false) || !validDisplayText(graph.Details[row], true) {
			return ErrInvalidGraph
		}
		startOffset := graph.SourceStarts[row]
		endOffset := graph.SourceEnds[row]
		if startOffset > endOffset || endOffset > graph.SourceLength {
			return ErrInvalidGraph
		}
		edge = end
	}
	if edge != uint64(edges) {
		return ErrInvalidGraph
	}
	for row := range edges {
		if graph.Edges[row] == 0 || uint64(graph.Edges[row]) > uint64(nodes) || graph.EdgeLabels[row] == "" ||
			!graph.EdgeKinds[row].Valid() || !validDisplayText(graph.EdgeLabels[row], false) {
			return ErrInvalidGraph
		}
	}

	validator.rootSeen = resizeClear(validator.rootSeen, nodes)
	for _, root := range graph.Roots {
		if root == 0 || uint64(root) > uint64(nodes) || validator.rootSeen[root-1] {
			return ErrInvalidGraph
		}
		validator.rootSeen[root-1] = true
	}
	if validator.graphHasCycle(graph) {
		return ErrGraphCycle
	}
	return nil
}

func validDisplayText(value string, multiline bool) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) && !(multiline && character == '\n') {
			return false
		}
	}
	return true
}

type visitFrame struct {
	node uint32
	next uint32
}

func (validator *Validator) graphHasCycle(graph *Graph) bool {
	validator.state = resizeClear(validator.state, len(graph.Kinds))
	validator.stack = resizeClear(validator.stack, len(graph.Kinds))
	stackLength := 0
	for start := range graph.Kinds {
		if validator.state[start] != 0 {
			continue
		}
		validator.state[start] = 1
		validator.stack[stackLength] = visitFrame{node: uint32(start + 1)}
		stackLength++
		for stackLength != 0 {
			frame := &validator.stack[stackLength-1]
			row := frame.node - 1
			count := uint32(graph.EdgeCounts[row])
			if frame.next == count {
				validator.state[row] = 2
				stackLength--
				continue
			}
			edge := graph.EdgeStarts[row] + frame.next
			frame.next++
			child := graph.Edges[edge] - 1
			switch validator.state[child] {
			case 1:
				return true
			case 0:
				validator.state[child] = 1
				validator.stack[stackLength] = visitFrame{node: child + 1}
				stackLength++
			}
		}
	}
	return false
}
