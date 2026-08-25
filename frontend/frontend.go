// Package frontend defines the bounded semantic contract shared by policy
// language frontends.
package frontend

import (
	"bytes"
	"errors"
	"fmt"
	"unicode/utf8"
)

var (
	ErrInvalidEnum    = errors.New("frontend: invalid enum value")
	ErrInvalidLimits  = errors.New("frontend: invalid limits")
	ErrInvalidBinding = errors.New("frontend: invalid binding set")
)

// Language identifies a source-policy language.
type Language uint8

const (
	LanguageInvalid Language = iota
	LanguageNative
	LanguageCEL
	LanguageRego
	LanguageCedar
	LanguageProtobuf
)

var languageNames = [...]string{"native", "cel", "rego", "cedar", "protobuf"}

func (value Language) Valid() bool { return value >= LanguageNative && value <= LanguageProtobuf }
func (value Language) String() string {
	return enumString(uint8(value), languageNames[:])
}
func (value Language) MarshalText() ([]byte, error) {
	return marshalEnum(uint8(value), languageNames[:])
}
func (value *Language) UnmarshalText(text []byte) error {
	parsed, err := parseEnum(text, languageNames[:])
	if err == nil {
		*value = Language(parsed)
	}
	return err
}

// ValueKind is the type of a declared field or scalar literal.
type ValueKind uint8

const (
	ValueKindInvalid ValueKind = iota
	ValueKindString
	ValueKindInteger
	ValueKindBoolean
)

var valueKindNames = [...]string{"string", "integer", "boolean"}

func (value ValueKind) Valid() bool { return value >= ValueKindString && value <= ValueKindBoolean }
func (value ValueKind) String() string {
	return enumString(uint8(value), valueKindNames[:])
}
func (value ValueKind) MarshalText() ([]byte, error) {
	return marshalEnum(uint8(value), valueKindNames[:])
}
func (value *ValueKind) UnmarshalText(text []byte) error {
	parsed, err := parseEnum(text, valueKindNames[:])
	if err == nil {
		*value = ValueKind(parsed)
	}
	return err
}

// FieldGroup identifies a request-fact storage group.
type FieldGroup uint8

const (
	FieldGroupInvalid FieldGroup = iota
	FieldGroupSubject
	FieldGroupAction
	FieldGroupResource
	FieldGroupOutput
	FieldGroupContext
)

var fieldGroupNames = [...]string{"subject", "action", "resource", "output", "context"}

func (value FieldGroup) Valid() bool { return value >= FieldGroupSubject && value <= FieldGroupContext }
func (value FieldGroup) String() string {
	return enumString(uint8(value), fieldGroupNames[:])
}
func (value FieldGroup) MarshalText() ([]byte, error) {
	return marshalEnum(uint8(value), fieldGroupNames[:])
}
func (value *FieldGroup) UnmarshalText(text []byte) error {
	parsed, err := parseEnum(text, fieldGroupNames[:])
	if err == nil {
		*value = FieldGroup(parsed)
	}
	return err
}

// NodeKind identifies one semantic expression row.
type NodeKind uint8

const (
	NodeKindInvalid NodeKind = iota
	NodeKindBoolean
	NodeKindCompare
	NodeKindAll
	NodeKindAny
	NodeKindNot
	NodeKindDefined
)

var nodeKindNames = [...]string{"boolean", "compare", "all", "any", "not", "defined"}

func (value NodeKind) Valid() bool { return value >= NodeKindBoolean && value <= NodeKindDefined }
func (value NodeKind) String() string {
	return enumString(uint8(value), nodeKindNames[:])
}
func (value NodeKind) MarshalText() ([]byte, error) {
	return marshalEnum(uint8(value), nodeKindNames[:])
}
func (value *NodeKind) UnmarshalText(text []byte) error {
	parsed, err := parseEnum(text, nodeKindNames[:])
	if err == nil {
		*value = NodeKind(parsed)
	}
	return err
}

// CompareOp identifies one bounded scalar comparison.
type CompareOp uint8

const (
	CompareOpInvalid CompareOp = iota
	CompareOpEqual
	CompareOpNotEqual
	CompareOpLess
	CompareOpLessEqual
	CompareOpGreater
	CompareOpGreaterEqual
	CompareOpIn
)

var compareOpNames = [...]string{"equal", "not_equal", "less", "less_equal", "greater", "greater_equal", "in"}

func (value CompareOp) Valid() bool { return value >= CompareOpEqual && value <= CompareOpIn }
func (value CompareOp) String() string {
	return enumString(uint8(value), compareOpNames[:])
}
func (value CompareOp) MarshalText() ([]byte, error) {
	return marshalEnum(uint8(value), compareOpNames[:])
}
func (value *CompareOp) UnmarshalText(text []byte) error {
	parsed, err := parseEnum(text, compareOpNames[:])
	if err == nil {
		*value = CompareOp(parsed)
	}
	return err
}

// DefaultDecision selects the outcome for an unresolved expression.
type DefaultDecision uint8

const (
	DefaultInvalid DefaultDecision = iota
	DefaultEscalate
	DefaultReject
)

var defaultNames = [...]string{"escalate", "reject"}

func (value DefaultDecision) Valid() bool { return value >= DefaultEscalate && value <= DefaultReject }
func (value DefaultDecision) String() string {
	return enumString(uint8(value), defaultNames[:])
}
func (value DefaultDecision) MarshalText() ([]byte, error) {
	return marshalEnum(uint8(value), defaultNames[:])
}
func (value *DefaultDecision) UnmarshalText(text []byte) error {
	parsed, err := parseEnum(text, defaultNames[:])
	if err == nil {
		*value = DefaultDecision(parsed)
	}
	return err
}

// Support classifies one source-language capability.
type Support uint8

const (
	SupportInvalid Support = iota
	SupportSupported
	SupportRestricted
	SupportRejected
)

var supportNames = [...]string{"supported", "restricted", "rejected"}

func (value Support) Valid() bool { return value >= SupportSupported && value <= SupportRejected }
func (value Support) String() string {
	return enumString(uint8(value), supportNames[:])
}
func (value Support) MarshalText() ([]byte, error) {
	return marshalEnum(uint8(value), supportNames[:])
}
func (value *Support) UnmarshalText(text []byte) error {
	parsed, err := parseEnum(text, supportNames[:])
	if err == nil {
		*value = Support(parsed)
	}
	return err
}

// DiagnosticCode identifies a stable frontend failure class.
type DiagnosticCode uint8

const (
	CodeInvalid DiagnosticCode = iota
	CodeSyntax
	CodeType
	CodeUnsupported
	CodeUnknownField
	CodeDuplicate
	CodeLimit
	CodeInvalidBinding
	CodeInvalidPolicy
)

var diagnosticCodeNames = [...]string{
	"syntax", "type", "unsupported", "unknown_field", "duplicate", "limit", "invalid_binding", "invalid_policy",
}

func (value DiagnosticCode) Valid() bool { return value >= CodeSyntax && value <= CodeInvalidPolicy }
func (value DiagnosticCode) String() string {
	return enumString(uint8(value), diagnosticCodeNames[:])
}
func (value DiagnosticCode) MarshalText() ([]byte, error) {
	return marshalEnum(uint8(value), diagnosticCodeNames[:])
}
func (value *DiagnosticCode) UnmarshalText(text []byte) error {
	parsed, err := parseEnum(text, diagnosticCodeNames[:])
	if err == nil {
		*value = DiagnosticCode(parsed)
	}
	return err
}

func enumString(value uint8, names []string) string {
	row := int(value) - 1
	if row < 0 || row >= len(names) {
		return "invalid"
	}
	return names[row]
}

func marshalEnum(value uint8, names []string) ([]byte, error) {
	name := enumString(value, names)
	if name == "invalid" {
		return nil, ErrInvalidEnum
	}
	return []byte(name), nil
}

func parseEnum(text []byte, names []string) (uint8, error) {
	for row := range names {
		if bytes.Equal(text, []byte(names[row])) {
			return uint8(row + 1), nil
		}
	}
	return 0, fmt.Errorf("%w: %q", ErrInvalidEnum, text)
}

// NodeID, FieldID, and LiteralID are one-based semantic table identifiers.
type NodeID uint32
type FieldID uint32
type LiteralID uint32

// Span is a half-open UTF-8 byte range in source.
type Span struct {
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
}

// Capability is one stable language capability declaration.
type Capability struct {
	Name    string  `json:"name"`
	Support Support `json:"support"`
}

// Limits bounds parser input and the semantic table produced from it.
type Limits struct {
	MaxSourceBytes uint32 `json:"max_source_bytes"`
	MaxNodes       uint32 `json:"max_nodes"`
	MaxDepth       uint32 `json:"max_depth"`
	MaxFields      uint32 `json:"max_fields"`
	MaxLiterals    uint32 `json:"max_literals"`
	MaxChildren    uint32 `json:"max_children"`
	MaxStringBytes uint32 `json:"max_string_bytes"`
	MaxDiagnostics uint32 `json:"max_diagnostics"`
}

// DefaultLimits returns the shared compatibility-frontend ceilings.
func DefaultLimits() Limits {
	return Limits{
		MaxSourceBytes: 4 << 20,
		MaxNodes:       65_536,
		MaxDepth:       128,
		MaxFields:      4_096,
		MaxLiterals:    131_072,
		MaxChildren:    65_536,
		MaxStringBytes: 1 << 20,
		MaxDiagnostics: 128,
	}
}

// Valid reports whether every limit is nonzero and representable on all
// supported architectures.
func (limits Limits) Valid() bool {
	const maxInt32 = uint32(1<<31 - 1)
	return limits.MaxSourceBytes > 0 && limits.MaxSourceBytes <= maxInt32 &&
		limits.MaxNodes > 0 && limits.MaxNodes <= maxInt32 &&
		limits.MaxDepth > 0 && limits.MaxDepth <= maxInt32 &&
		limits.MaxFields > 0 && limits.MaxFields <= maxInt32 &&
		limits.MaxLiterals > 0 && limits.MaxLiterals <= maxInt32 &&
		limits.MaxChildren > 0 && limits.MaxChildren <= maxInt32 &&
		limits.MaxStringBytes > 0 && limits.MaxStringBytes <= maxInt32 &&
		limits.MaxDiagnostics > 0 && limits.MaxDiagnostics <= maxInt32
}

// Binding declares one source-language name and its canonical request field.
type Binding struct {
	Source string     `json:"source"`
	Target string     `json:"target"`
	Kind   ValueKind  `json:"kind"`
	Group  FieldGroup `json:"group"`
}

// BindingSet is the versioned declaration environment for one source policy.
type BindingSet struct {
	Name     string    `json:"name"`
	Version  string    `json:"version"`
	Decision string    `json:"decision,omitempty"`
	Fields   []Binding `json:"fields"`
}

// Validate checks a binding set without mutating it.
func (bindings BindingSet) Validate(limits Limits) error {
	if !limits.Valid() {
		return ErrInvalidLimits
	}
	if !validMetadata(bindings.Name) || !validMetadata(bindings.Version) ||
		(bindings.Decision != "" && !validPath(bindings.Decision)) {
		return ErrInvalidBinding
	}
	if uint64(len(bindings.Fields)) > uint64(limits.MaxFields) {
		return ErrInvalidBinding
	}
	stringBytes := uint64(len(bindings.Name) + len(bindings.Version) + len(bindings.Decision))
	for row := range bindings.Fields {
		binding := &bindings.Fields[row]
		if !validPath(binding.Source) || !validPath(binding.Target) || !binding.Kind.Valid() || !binding.Group.Valid() {
			return ErrInvalidBinding
		}
		stringBytes += uint64(len(binding.Source) + len(binding.Target))
		if stringBytes > uint64(limits.MaxStringBytes) {
			return ErrInvalidBinding
		}
		for previous := 0; previous < row; previous++ {
			if bindings.Fields[previous].Source == binding.Source || bindings.Fields[previous].Target == binding.Target {
				return ErrInvalidBinding
			}
		}
	}
	if stringBytes > uint64(limits.MaxStringBytes) {
		return ErrInvalidBinding
	}
	return nil
}

func validMetadata(value string) bool {
	if value == "" || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if r < ' ' || r == 0x7f {
			return false
		}
	}
	return true
}

func validPath(value string) bool {
	if value == "" {
		return false
	}
	segmentStart := true
	for row := 0; row < len(value); row++ {
		character := value[row]
		if character == '.' {
			if segmentStart {
				return false
			}
			segmentStart = true
			continue
		}
		if segmentStart {
			if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && character != '_' {
				return false
			}
			segmentStart = false
			continue
		}
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return !segmentStart
}

// Policy is an owned, pointerless-element semantic expression table. Every
// node-indexed column has the same length; child and list edges use CSR ranges.
type Policy struct {
	Source  []byte
	Name    []byte
	Version []byte

	NodeKinds        []NodeKind
	NodeOps          []CompareOp
	NodeFields       []FieldID
	NodeLiterals     []LiteralID
	NodeChildStarts  []uint32
	NodeChildCounts  []uint16
	NodeListStarts   []uint32
	NodeListCounts   []uint16
	NodeSourceStarts []uint32
	NodeSourceEnds   []uint32

	ChildNodeIDs   []NodeID
	ListLiteralIDs []LiteralID

	FieldNameStarts    []uint32
	FieldNameLengths   []uint32
	FieldTargetStarts  []uint32
	FieldTargetLengths []uint32
	FieldKinds         []ValueKind
	FieldGroups        []FieldGroup
	FieldBytes         []byte

	LiteralKinds  []ValueKind
	LiteralRefs   []uint32
	SymbolStarts  []uint32
	SymbolLengths []uint32
	SymbolBytes   []byte
	IntegerValues []int64
	BooleanValues []uint8

	Root    NodeID
	Default DefaultDecision
}

// Literal is a transient builder input. Constructors keep it canonical; the
// builder copies string bytes into Policy storage.
type Literal struct {
	String  []byte
	Integer int64
	Kind    ValueKind
	Boolean bool
}

func StringLiteral(value []byte) Literal {
	return Literal{String: value, Kind: ValueKindString}
}

func IntegerLiteral(value int64) Literal {
	return Literal{Integer: value, Kind: ValueKindInteger}
}

func BooleanLiteral(value bool) Literal {
	return Literal{Kind: ValueKindBoolean, Boolean: value}
}
