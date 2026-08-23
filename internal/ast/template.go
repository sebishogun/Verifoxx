package ast

import (
	"bytes"
	"errors"
	"math"

	"github.com/sebishogun/verifoxx/internal/schema"
)

const (
	MaxTemplateBytes = 512
	MaxTemplateOps   = 32
)

var (
	ErrInvalidTemplate        = errors.New("ast: invalid template")
	ErrInvalidTemplateContext = errors.New("ast: invalid template context")
	ErrTemplateTooLarge       = errors.New("ast: template exceeds hard limit")
	ErrTooManyTemplates       = errors.New("ast: too many templates")
)

// TemplateContext bounds the values available to a policy-authored template.
type TemplateContext uint8

const (
	TemplateContextInvalid TemplateContext = iota
	TemplateContextAssumption
	TemplateContextDecision
	TemplateContextUnresolved
	TemplateContextEvidenceMissing
	TemplateContextEvidencePresent
)

// Valid reports whether c is a supported template binding context.
func (c TemplateContext) Valid() bool {
	return c >= TemplateContextAssumption && c <= TemplateContextEvidencePresent
}

// TemplateOp is one compiled append operation. Literal operations consume the
// number of bytes in their parallel TemplateArgs entry; placeholders use zero.
type TemplateOp uint8

const (
	TemplateOpInvalid TemplateOp = iota
	TemplateOpLiteral
	TemplateOpPolicyName
	TemplateOpPolicyVersion
	TemplateOpRequestID
	TemplateOpOutcome
	TemplateOpRequirementID
	TemplateOpClauseID
	TemplateOpNodeID
	TemplateOpReason
	TemplateOpEvidenceKind
	TemplateOpEvidenceState
	TemplateOpRequiredEvidenceState
	TemplateOpEvidenceID
)

// Valid reports whether op is a supported compiled template operation.
func (op TemplateOp) Valid() bool {
	return op >= TemplateOpLiteral && op <= TemplateOpEvidenceID
}

// AllowedIn reports whether context provides the binding consumed by op.
func (op TemplateOp) AllowedIn(context TemplateContext) bool {
	if op == TemplateOpLiteral {
		return context.Valid()
	}
	return templateOpAllowed(op, context)
}

func templatePlaceholder(name []byte) (TemplateOp, bool) {
	switch {
	case bytes.Equal(name, []byte("policy_name")):
		return TemplateOpPolicyName, true
	case bytes.Equal(name, []byte("policy_version")):
		return TemplateOpPolicyVersion, true
	case bytes.Equal(name, []byte("request_id")):
		return TemplateOpRequestID, true
	case bytes.Equal(name, []byte("outcome")):
		return TemplateOpOutcome, true
	case bytes.Equal(name, []byte("requirement_id")):
		return TemplateOpRequirementID, true
	case bytes.Equal(name, []byte("clause_id")):
		return TemplateOpClauseID, true
	case bytes.Equal(name, []byte("node_id")):
		return TemplateOpNodeID, true
	case bytes.Equal(name, []byte("reason")):
		return TemplateOpReason, true
	case bytes.Equal(name, []byte("evidence_kind")):
		return TemplateOpEvidenceKind, true
	case bytes.Equal(name, []byte("evidence_state")):
		return TemplateOpEvidenceState, true
	case bytes.Equal(name, []byte("required_evidence_state")):
		return TemplateOpRequiredEvidenceState, true
	case bytes.Equal(name, []byte("evidence_id")):
		return TemplateOpEvidenceID, true
	}
	return TemplateOpInvalid, false
}

func templateOpAllowed(op TemplateOp, context TemplateContext) bool {
	switch op {
	case TemplateOpPolicyName, TemplateOpPolicyVersion, TemplateOpRequestID:
		return true
	case TemplateOpOutcome, TemplateOpRequirementID, TemplateOpClauseID, TemplateOpNodeID:
		return context >= TemplateContextDecision
	case TemplateOpReason:
		return context >= TemplateContextUnresolved
	case TemplateOpEvidenceKind, TemplateOpRequiredEvidenceState:
		return context >= TemplateContextEvidenceMissing
	case TemplateOpEvidenceState, TemplateOpEvidenceID:
		return context == TemplateContextEvidencePresent
	}
	return false
}

type templateRollback struct {
	bytes int
	ops   int
	args  int
}

func (b *Builder) rollbackTemplate(mark templateRollback) {
	b.doc.TemplateBytes = b.doc.TemplateBytes[:mark.bytes]
	b.doc.TemplateOps = b.doc.TemplateOps[:mark.ops]
	b.doc.TemplateArgs = b.doc.TemplateArgs[:mark.args]
}

func (b *Builder) appendTemplateOp(mark templateRollback, op TemplateOp, arg uint32) error {
	if len(b.doc.TemplateOps)-mark.ops >= MaxTemplateOps {
		return ErrTemplateTooLarge
	}
	b.doc.TemplateOps = append(b.doc.TemplateOps, op)
	b.doc.TemplateArgs = append(b.doc.TemplateArgs, arg)
	return nil
}

func (b *Builder) appendTemplateLiteral(mark templateRollback, literal []byte) error {
	if len(literal) == 0 {
		return nil
	}
	d := &b.doc
	if len(d.TemplateOps) > mark.ops && d.TemplateOps[len(d.TemplateOps)-1] == TemplateOpLiteral {
		d.TemplateBytes = append(d.TemplateBytes, literal...)
		d.TemplateArgs[len(d.TemplateArgs)-1] += uint32(len(literal))
		return nil
	}
	if err := b.appendTemplateOp(mark, TemplateOpLiteral, uint32(len(literal))); err != nil {
		return err
	}
	d.TemplateBytes = append(d.TemplateBytes, literal...)
	return nil
}

// AddTemplate parses decoded policy text into flat operation and literal
// columns. Rejected templates leave every column unchanged.
func (b *Builder) AddTemplate(text []byte, context TemplateContext) (schema.TemplateID, error) {
	if !context.Valid() {
		return 0, ErrInvalidTemplateContext
	}
	if len(text) > MaxTemplateBytes {
		return 0, ErrTemplateTooLarge
	}
	d := &b.doc
	if uint64(len(d.TemplateOpStarts)) >= uint64(math.MaxUint32) || uint64(len(d.TemplateBytes))+uint64(len(text)) > uint64(math.MaxUint32) {
		return 0, ErrTooManyTemplates
	}
	mark := templateRollback{bytes: len(d.TemplateBytes), ops: len(d.TemplateOps), args: len(d.TemplateArgs)}
	fail := func(err error) (schema.TemplateID, error) {
		b.rollbackTemplate(mark)
		return 0, err
	}

	for i := 0; i < len(text); {
		literalStart := i
		for i < len(text) && text[i] != '{' && text[i] != '}' {
			i++
		}
		if err := b.appendTemplateLiteral(mark, text[literalStart:i]); err != nil {
			return fail(err)
		}
		if i == len(text) {
			break
		}

		brace := text[i]
		if i+1 < len(text) && text[i+1] == brace {
			if err := b.appendTemplateLiteral(mark, text[i:i+1]); err != nil {
				return fail(err)
			}
			i += 2
			continue
		}
		if brace == '}' {
			return fail(ErrInvalidTemplate)
		}

		nameStart := i + 1
		end := nameStart
		for end < len(text) && text[end] != '}' {
			if text[end] == '{' {
				return fail(ErrInvalidTemplate)
			}
			end++
		}
		if end == len(text) || end == nameStart {
			return fail(ErrInvalidTemplate)
		}
		op, ok := templatePlaceholder(text[nameStart:end])
		if !ok {
			return fail(ErrInvalidTemplate)
		}
		if !templateOpAllowed(op, context) {
			return fail(ErrInvalidTemplateContext)
		}
		if err := b.appendTemplateOp(mark, op, 0); err != nil {
			return fail(err)
		}
		i = end + 1
	}

	d.TemplateOpStarts = append(d.TemplateOpStarts, uint32(mark.ops))
	d.TemplateOpCounts = append(d.TemplateOpCounts, uint16(len(d.TemplateOps)-mark.ops))
	d.TemplateLiteralStarts = append(d.TemplateLiteralStarts, uint32(mark.bytes))
	d.TemplateMaxBytes = append(d.TemplateMaxBytes, uint32(len(d.TemplateBytes)-mark.bytes))
	d.TemplateContexts = append(d.TemplateContexts, context)
	return schema.TemplateID(len(d.TemplateOpStarts)), nil
}
