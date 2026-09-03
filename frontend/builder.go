package frontend

import (
	"errors"
	"math"
	"unicode/utf8"
)

var (
	ErrInvalidSource    = errors.New("frontend: invalid UTF-8 source")
	ErrInvalidSpan      = errors.New("frontend: invalid source span")
	ErrInvalidNode      = errors.New("frontend: invalid node")
	ErrInvalidField     = errors.New("frontend: invalid field")
	ErrInvalidLiteral   = errors.New("frontend: invalid literal")
	ErrInvalidOperation = errors.New("frontend: invalid operation")
	ErrInvalidArity     = errors.New("frontend: invalid arity")
	ErrLimitExceeded    = errors.New("frontend: semantic limit exceeded")
)

// Builder owns one mutable semantic table. It is not safe for concurrent use.
type Builder struct {
	depths      []uint32
	policy      Policy
	limits      Limits
	stringBytes uint32
}

// NewBuilder validates and copies the source and field declarations.
func NewBuilder(source []byte, bindings BindingSet, limits Limits) (*Builder, error) {
	if !limits.Valid() {
		return nil, ErrInvalidLimits
	}
	if uint64(len(source)) > uint64(limits.MaxSourceBytes) {
		return nil, ErrLimitExceeded
	}
	if !utf8.Valid(source) {
		return nil, ErrInvalidSource
	}
	if err := bindings.Validate(limits); err != nil {
		return nil, err
	}

	fieldBytes := 0
	for row := range bindings.Fields {
		fieldBytes += len(bindings.Fields[row].Source) + len(bindings.Fields[row].Target)
	}
	nodeHint := boundedHint(limits.MaxNodes, len(source)/4+1)
	literalHint := boundedHint(limits.MaxLiterals, len(source)/8+1)
	childHint := boundedHint(limits.MaxChildren, len(source)/8+1)
	fieldCount := len(bindings.Fields)
	builder := &Builder{
		limits:      limits,
		depths:      make([]uint32, 0, nodeHint),
		stringBytes: uint32(len(bindings.Name) + len(bindings.Version) + fieldBytes),
		policy: Policy{
			Source:             cloneBytes(source),
			Name:               bytesFromString(bindings.Name),
			Version:            bytesFromString(bindings.Version),
			NodeKinds:          make([]NodeKind, 0, nodeHint),
			NodeOps:            make([]CompareOp, 0, nodeHint),
			NodeFields:         make([]FieldID, 0, nodeHint),
			NodeLiterals:       make([]LiteralID, 0, nodeHint),
			NodeChildStarts:    make([]uint32, 0, nodeHint),
			NodeChildCounts:    make([]uint16, 0, nodeHint),
			NodeListStarts:     make([]uint32, 0, nodeHint),
			NodeListCounts:     make([]uint16, 0, nodeHint),
			NodeSourceStarts:   make([]uint32, 0, nodeHint),
			NodeSourceEnds:     make([]uint32, 0, nodeHint),
			ChildNodeIDs:       make([]NodeID, 0, childHint),
			ListLiteralIDs:     make([]LiteralID, 0, childHint),
			FieldNameStarts:    make([]uint32, 0, fieldCount),
			FieldNameLengths:   make([]uint32, 0, fieldCount),
			FieldTargetStarts:  make([]uint32, 0, fieldCount),
			FieldTargetLengths: make([]uint32, 0, fieldCount),
			FieldKinds:         make([]ValueKind, 0, fieldCount),
			FieldGroups:        make([]FieldGroup, 0, fieldCount),
			FieldBytes:         make([]byte, 0, fieldBytes),
			LiteralKinds:       make([]ValueKind, 0, literalHint),
			LiteralRefs:        make([]uint32, 0, literalHint),
			SymbolStarts:       make([]uint32, 0, literalHint),
			SymbolLengths:      make([]uint32, 0, literalHint),
			SymbolBytes:        make([]byte, 0, boundedHint(limits.MaxStringBytes, len(source)/8+1)),
			IntegerValues:      make([]int64, 0, literalHint),
			BooleanValues:      make([]uint8, 0, literalHint),
		},
	}

	for row := range bindings.Fields {
		binding := &bindings.Fields[row]
		builder.policy.FieldNameStarts = append(builder.policy.FieldNameStarts, uint32(len(builder.policy.FieldBytes)))
		builder.policy.FieldNameLengths = append(builder.policy.FieldNameLengths, uint32(len(binding.Source)))
		builder.policy.FieldBytes = append(builder.policy.FieldBytes, binding.Source...)
		builder.policy.FieldTargetStarts = append(builder.policy.FieldTargetStarts, uint32(len(builder.policy.FieldBytes)))
		builder.policy.FieldTargetLengths = append(builder.policy.FieldTargetLengths, uint32(len(binding.Target)))
		builder.policy.FieldBytes = append(builder.policy.FieldBytes, binding.Target...)
		builder.policy.FieldKinds = append(builder.policy.FieldKinds, binding.Kind)
		builder.policy.FieldGroups = append(builder.policy.FieldGroups, binding.Group)
	}
	return builder, nil
}

func boundedHint(limit uint32, observed int) int {
	if observed < 1 {
		observed = 1
	}
	if observed > 256 {
		observed = 256
	}
	if uint64(observed) > uint64(limit) {
		return int(limit)
	}
	return observed
}

// AddBoolean appends one Boolean constant expression.
func (builder *Builder) AddBoolean(value bool, span Span) (NodeID, error) {
	literal := BooleanLiteral(value)
	if err := builder.checkAppend(span, 1, 1, 0, 0); err != nil {
		return 0, err
	}
	literalID := builder.appendLiteral(literal)
	return builder.appendNode(NodeKindBoolean, CompareOpInvalid, 0, literalID, 0, 0, 0, 0, span, 1), nil
}

// AddDefined appends one classical field-definedness expression without a literal payload.
func (builder *Builder) AddDefined(field FieldID, span Span) (NodeID, error) {
	if _, ok := builder.fieldKind(field); !ok {
		return 0, ErrInvalidField
	}
	if err := builder.checkAppend(span, 1, 0, 0, 0); err != nil {
		return 0, err
	}
	return builder.appendNode(NodeKindDefined, CompareOpInvalid, field, 0, 0, 0, 0, 0, span, 1), nil
}

// AddExists appends an unknown-preserving field-existence expression without a literal payload.
func (builder *Builder) AddExists(field FieldID, span Span) (NodeID, error) {
	if _, ok := builder.fieldKind(field); !ok {
		return 0, ErrInvalidField
	}
	if err := builder.checkAppend(span, 1, 0, 0, 0); err != nil {
		return 0, err
	}
	return builder.appendNode(NodeKindExists, CompareOpInvalid, field, 0, 0, 0, 0, 0, span, 1), nil
}

// AddCompare appends one field-to-scalar comparison.
func (builder *Builder) AddCompare(field FieldID, operation CompareOp, literal Literal, span Span) (NodeID, error) {
	fieldKind, ok := builder.fieldKind(field)
	if !ok {
		return 0, ErrInvalidField
	}
	if !operation.Valid() || operation == CompareOpIn {
		return 0, ErrInvalidOperation
	}
	stringBytes, err := validateLiteral(literal)
	if err != nil || literal.Kind != fieldKind {
		return 0, ErrInvalidLiteral
	}
	if orderedOperation(operation) && fieldKind != ValueKindInteger {
		return 0, ErrInvalidOperation
	}
	if err := builder.checkAppend(span, 1, 1, 0, stringBytes); err != nil {
		return 0, err
	}
	literalID := builder.appendLiteral(literal)
	return builder.appendNode(NodeKindCompare, operation, field, literalID, 0, 0, 0, 0, span, 1), nil
}

// AddIn appends membership in a nonempty homogeneous scalar list.
func (builder *Builder) AddIn(field FieldID, literals []Literal, span Span) (NodeID, error) {
	fieldKind, ok := builder.fieldKind(field)
	if !ok {
		return 0, ErrInvalidField
	}
	if len(literals) == 0 || len(literals) > math.MaxUint16 {
		return 0, ErrInvalidArity
	}
	stringBytes := uint64(0)
	for row := range literals {
		cost, err := validateLiteral(literals[row])
		if err != nil || literals[row].Kind != fieldKind {
			return 0, ErrInvalidLiteral
		}
		stringBytes += uint64(cost)
	}
	if err := builder.checkAppend(span, 1, uint32(len(literals)), uint32(len(literals)), stringBytes); err != nil {
		return 0, err
	}
	listStart := uint32(len(builder.policy.ListLiteralIDs))
	for row := range literals {
		builder.policy.ListLiteralIDs = append(builder.policy.ListLiteralIDs, builder.appendLiteral(literals[row]))
	}
	return builder.appendNode(
		NodeKindCompare, CompareOpIn, field, 0, 0, 0, listStart, uint16(len(literals)), span, 1,
	), nil
}

// AddAll appends a conjunction with at least two existing children.
func (builder *Builder) AddAll(children []NodeID, span Span) (NodeID, error) {
	return builder.addGroup(NodeKindAll, children, span)
}

// AddAny appends a disjunction with at least two existing children.
func (builder *Builder) AddAny(children []NodeID, span Span) (NodeID, error) {
	return builder.addGroup(NodeKindAny, children, span)
}

func (builder *Builder) addGroup(kind NodeKind, children []NodeID, span Span) (NodeID, error) {
	if len(children) < 2 || len(children) > math.MaxUint16 {
		return 0, ErrInvalidArity
	}
	depth := uint32(0)
	for _, child := range children {
		childDepth, ok := builder.nodeDepth(child)
		if !ok {
			return 0, ErrInvalidNode
		}
		if childDepth > depth {
			depth = childDepth
		}
	}
	depth++
	if err := builder.checkAppend(span, depth, 0, uint32(len(children)), 0); err != nil {
		return 0, err
	}
	start := uint32(len(builder.policy.ChildNodeIDs))
	builder.policy.ChildNodeIDs = append(builder.policy.ChildNodeIDs, children...)
	return builder.appendNode(kind, CompareOpInvalid, 0, 0, start, uint16(len(children)), 0, 0, span, depth), nil
}

// AddNot appends a negation of one existing child.
func (builder *Builder) AddNot(child NodeID, span Span) (NodeID, error) {
	childDepth, ok := builder.nodeDepth(child)
	if !ok {
		return 0, ErrInvalidNode
	}
	depth := childDepth + 1
	if err := builder.checkAppend(span, depth, 0, 1, 0); err != nil {
		return 0, err
	}
	start := uint32(len(builder.policy.ChildNodeIDs))
	builder.policy.ChildNodeIDs = append(builder.policy.ChildNodeIDs, child)
	return builder.appendNode(NodeKindNot, CompareOpInvalid, 0, 0, start, 1, 0, 0, span, depth), nil
}

func (builder *Builder) fieldKind(field FieldID) (ValueKind, bool) {
	row := uint64(field - 1)
	if field == 0 || row >= uint64(len(builder.policy.FieldKinds)) {
		return ValueKindInvalid, false
	}
	return builder.policy.FieldKinds[row], true
}

func (builder *Builder) nodeDepth(node NodeID) (uint32, bool) {
	row := uint64(node - 1)
	if node == 0 || row >= uint64(len(builder.depths)) {
		return 0, false
	}
	return builder.depths[row], true
}

func orderedOperation(operation CompareOp) bool {
	return operation >= CompareOpLess && operation <= CompareOpGreaterEqual
}

func validateLiteral(literal Literal) (uint64, error) {
	if !literal.Kind.Valid() {
		return 0, ErrInvalidLiteral
	}
	switch literal.Kind {
	case ValueKindString:
		if literal.Integer != 0 || literal.Boolean || !utf8.Valid(literal.String) {
			return 0, ErrInvalidLiteral
		}
		return uint64(len(literal.String)), nil
	case ValueKindInteger:
		if len(literal.String) != 0 || literal.Boolean {
			return 0, ErrInvalidLiteral
		}
	case ValueKindBoolean:
		if len(literal.String) != 0 || literal.Integer != 0 {
			return 0, ErrInvalidLiteral
		}
	}
	return 0, nil
}

func (builder *Builder) checkAppend(span Span, depth, literals, edges uint32, stringBytes uint64) error {
	if !builder.validSpan(span) {
		return ErrInvalidSpan
	}
	if uint64(len(builder.policy.NodeKinds))+1 > uint64(builder.limits.MaxNodes) ||
		uint64(len(builder.policy.LiteralKinds))+uint64(literals) > uint64(builder.limits.MaxLiterals) ||
		uint64(len(builder.policy.ChildNodeIDs))+uint64(len(builder.policy.ListLiteralIDs))+uint64(edges) > uint64(builder.limits.MaxChildren) ||
		depth == 0 || uint64(depth) > uint64(builder.limits.MaxDepth) ||
		uint64(builder.stringBytes)+stringBytes > uint64(builder.limits.MaxStringBytes) {
		return ErrLimitExceeded
	}
	return nil
}

func (builder *Builder) validSpan(span Span) bool {
	if span.Start > span.End || uint64(span.End) > uint64(len(builder.policy.Source)) {
		return false
	}
	return byteBoundary(builder.policy.Source, span.Start) && byteBoundary(builder.policy.Source, span.End)
}

func byteBoundary(source []byte, offset uint32) bool {
	return int(offset) == len(source) || utf8.RuneStart(source[offset])
}

func (builder *Builder) appendLiteral(literal Literal) LiteralID {
	var reference uint32
	switch literal.Kind {
	case ValueKindString:
		reference = uint32(len(builder.policy.SymbolStarts))
		builder.policy.SymbolStarts = append(builder.policy.SymbolStarts, uint32(len(builder.policy.SymbolBytes)))
		builder.policy.SymbolLengths = append(builder.policy.SymbolLengths, uint32(len(literal.String)))
		builder.policy.SymbolBytes = append(builder.policy.SymbolBytes, literal.String...)
		builder.stringBytes += uint32(len(literal.String))
	case ValueKindInteger:
		reference = uint32(len(builder.policy.IntegerValues))
		builder.policy.IntegerValues = append(builder.policy.IntegerValues, literal.Integer)
	case ValueKindBoolean:
		reference = uint32(len(builder.policy.BooleanValues))
		encoded := uint8(0)
		if literal.Boolean {
			encoded = 1
		}
		builder.policy.BooleanValues = append(builder.policy.BooleanValues, encoded)
	}
	builder.policy.LiteralKinds = append(builder.policy.LiteralKinds, literal.Kind)
	builder.policy.LiteralRefs = append(builder.policy.LiteralRefs, reference)
	return LiteralID(len(builder.policy.LiteralKinds))
}

func (builder *Builder) appendNode(
	kind NodeKind,
	operation CompareOp,
	field FieldID,
	literal LiteralID,
	childStart uint32,
	childCount uint16,
	listStart uint32,
	listCount uint16,
	span Span,
	depth uint32,
) NodeID {
	builder.policy.NodeKinds = append(builder.policy.NodeKinds, kind)
	builder.policy.NodeOps = append(builder.policy.NodeOps, operation)
	builder.policy.NodeFields = append(builder.policy.NodeFields, field)
	builder.policy.NodeLiterals = append(builder.policy.NodeLiterals, literal)
	builder.policy.NodeChildStarts = append(builder.policy.NodeChildStarts, childStart)
	builder.policy.NodeChildCounts = append(builder.policy.NodeChildCounts, childCount)
	builder.policy.NodeListStarts = append(builder.policy.NodeListStarts, listStart)
	builder.policy.NodeListCounts = append(builder.policy.NodeListCounts, listCount)
	builder.policy.NodeSourceStarts = append(builder.policy.NodeSourceStarts, span.Start)
	builder.policy.NodeSourceEnds = append(builder.policy.NodeSourceEnds, span.End)
	builder.depths = append(builder.depths, depth)
	return NodeID(len(builder.policy.NodeKinds))
}

// Finish returns an immutable copy with exact-capacity columns.
func (builder *Builder) Finish(root NodeID, defaultDecision DefaultDecision) (*Policy, error) {
	if _, ok := builder.nodeDepth(root); !ok {
		return nil, ErrInvalidNode
	}
	if !defaultDecision.Valid() {
		return nil, ErrInvalidOperation
	}
	policy := &Policy{
		Source:             cloneExact(builder.policy.Source),
		Name:               cloneExact(builder.policy.Name),
		Version:            cloneExact(builder.policy.Version),
		NodeKinds:          cloneExact(builder.policy.NodeKinds),
		NodeOps:            cloneExact(builder.policy.NodeOps),
		NodeFields:         cloneExact(builder.policy.NodeFields),
		NodeLiterals:       cloneExact(builder.policy.NodeLiterals),
		NodeChildStarts:    cloneExact(builder.policy.NodeChildStarts),
		NodeChildCounts:    cloneExact(builder.policy.NodeChildCounts),
		NodeListStarts:     cloneExact(builder.policy.NodeListStarts),
		NodeListCounts:     cloneExact(builder.policy.NodeListCounts),
		NodeSourceStarts:   cloneExact(builder.policy.NodeSourceStarts),
		NodeSourceEnds:     cloneExact(builder.policy.NodeSourceEnds),
		ChildNodeIDs:       cloneExact(builder.policy.ChildNodeIDs),
		ListLiteralIDs:     cloneExact(builder.policy.ListLiteralIDs),
		FieldNameStarts:    cloneExact(builder.policy.FieldNameStarts),
		FieldNameLengths:   cloneExact(builder.policy.FieldNameLengths),
		FieldTargetStarts:  cloneExact(builder.policy.FieldTargetStarts),
		FieldTargetLengths: cloneExact(builder.policy.FieldTargetLengths),
		FieldKinds:         cloneExact(builder.policy.FieldKinds),
		FieldGroups:        cloneExact(builder.policy.FieldGroups),
		FieldBytes:         cloneExact(builder.policy.FieldBytes),
		LiteralKinds:       cloneExact(builder.policy.LiteralKinds),
		LiteralRefs:        cloneExact(builder.policy.LiteralRefs),
		SymbolStarts:       cloneExact(builder.policy.SymbolStarts),
		SymbolLengths:      cloneExact(builder.policy.SymbolLengths),
		SymbolBytes:        cloneExact(builder.policy.SymbolBytes),
		IntegerValues:      cloneExact(builder.policy.IntegerValues),
		BooleanValues:      cloneExact(builder.policy.BooleanValues),
		Root:               root,
		Default:            defaultDecision,
	}
	return policy, nil
}

func cloneExact[T any](source []T) []T {
	if len(source) == 0 {
		return nil
	}
	cloned := make([]T, len(source))
	copy(cloned, source)
	return cloned
}

func cloneBytes(source []byte) []byte {
	return cloneExact(source)
}

func bytesFromString(source string) []byte {
	if source == "" {
		return nil
	}
	cloned := make([]byte, len(source))
	copy(cloned, source)
	return cloned
}
