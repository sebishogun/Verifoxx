package result

import (
	"errors"

	"github.com/sebishogun/nornrune/internal/schema"
)

const (
	MaxTemplateOperations    = 32
	MaxRenderedTemplateBytes = 1024
)

var ErrInvalidTemplateTable = errors.New("result: invalid template table")

// TemplateOp is one precompiled append operation. Literal operations consume
// their parallel argument as a byte count; placeholder arguments are zero.
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

// Valid reports whether op is a defined runtime template operation.
func (op TemplateOp) Valid() bool {
	return op >= TemplateOpLiteral && op <= TemplateOpEvidenceID
}

// TemplateTable is a non-owning view over immutable template SoA/CSR columns.
// Literal bytes for each template are contiguous and operations consume the
// operation and literal slabs exactly in TemplateID order.
type TemplateTable struct {
	LiteralBytes  []byte
	OpStarts      []uint32
	OpCounts      []uint16
	LiteralStarts []uint32
	MaxBytes      []uint32
	Ops           []TemplateOp
	Args          []uint32
}

// Template is one borrowed runtime template row.
type Template struct {
	LiteralBytes []byte
	Ops          []TemplateOp
	Args         []uint32
	MaxBytes     uint32
}

// Validate checks every parallel length, operation, exact slab range, and
// rendered-size bound without allocating.
func (table *TemplateTable) Validate() error {
	if table == nil {
		return ErrInvalidTemplateTable
	}
	rows := len(table.OpStarts)
	if len(table.OpCounts) != rows || len(table.LiteralStarts) != rows ||
		len(table.MaxBytes) != rows || len(table.Ops) != len(table.Args) {
		return ErrInvalidTemplateTable
	}
	var opCursor, literalCursor uint64
	for row := 0; row < rows; row++ {
		start := uint64(table.OpStarts[row])
		count := uint64(table.OpCounts[row])
		end := start + count
		if count > MaxTemplateOperations || start != opCursor || end > uint64(len(table.Ops)) {
			return ErrInvalidTemplateTable
		}
		if uint64(table.LiteralStarts[row]) != literalCursor || table.MaxBytes[row] > MaxRenderedTemplateBytes {
			return ErrInvalidTemplateTable
		}
		var literalBytes uint64
		for i := start; i < end; i++ {
			op := table.Ops[int(i)]
			arg := table.Args[int(i)]
			if !op.Valid() {
				return ErrInvalidTemplateTable
			}
			if op == TemplateOpLiteral {
				if arg == 0 {
					return ErrInvalidTemplateTable
				}
				literalBytes += uint64(arg)
			} else if arg != 0 {
				return ErrInvalidTemplateTable
			}
		}
		literalEnd := literalCursor + literalBytes
		if literalEnd > uint64(len(table.LiteralBytes)) || literalBytes > uint64(table.MaxBytes[row]) {
			return ErrInvalidTemplateTable
		}
		opCursor = end
		literalCursor = literalEnd
	}
	if opCursor != uint64(len(table.Ops)) || literalCursor != uint64(len(table.LiteralBytes)) {
		return ErrInvalidTemplateTable
	}
	return nil
}

// Lookup returns a borrowed row after checking only the ranges needed by id.
// It is safe on malformed tables and does not allocate.
func (table *TemplateTable) Lookup(id schema.TemplateID) (Template, bool) {
	if table == nil || id == 0 {
		return Template{}, false
	}
	row := uint64(id - 1)
	if row >= uint64(len(table.OpStarts)) || row >= uint64(len(table.OpCounts)) ||
		row >= uint64(len(table.LiteralStarts)) || row >= uint64(len(table.MaxBytes)) {
		return Template{}, false
	}
	start := uint64(table.OpStarts[row])
	count := uint64(table.OpCounts[row])
	end := start + count
	if count > MaxTemplateOperations || end > uint64(len(table.Ops)) || end > uint64(len(table.Args)) ||
		table.MaxBytes[row] > MaxRenderedTemplateBytes {
		return Template{}, false
	}
	var literalBytes uint64
	for i := start; i < end; i++ {
		op := table.Ops[int(i)]
		arg := table.Args[int(i)]
		if !op.Valid() {
			return Template{}, false
		}
		if op == TemplateOpLiteral {
			if arg == 0 {
				return Template{}, false
			}
			literalBytes += uint64(arg)
		} else if arg != 0 {
			return Template{}, false
		}
	}
	literalStart := uint64(table.LiteralStarts[row])
	literalEnd := literalStart + literalBytes
	if literalEnd > uint64(len(table.LiteralBytes)) || literalBytes > uint64(table.MaxBytes[row]) {
		return Template{}, false
	}
	return Template{
		LiteralBytes: table.LiteralBytes[int(literalStart):int(literalEnd)],
		Ops:          table.Ops[int(start):int(end)],
		Args:         table.Args[int(start):int(end)],
		MaxBytes:     table.MaxBytes[row],
	}, true
}

// validatedTemplate returns one row from a table already accepted by Validate.
// The caller must retain that table immutably and supply a previously validated
// nonzero ID.
func (table *TemplateTable) validatedTemplate(id schema.TemplateID) Template {
	row := int(id - 1)
	opStart := int(table.OpStarts[row])
	opEnd := opStart + int(table.OpCounts[row])
	literalStart := int(table.LiteralStarts[row])
	literalEnd := len(table.LiteralBytes)
	if row+1 < len(table.LiteralStarts) {
		literalEnd = int(table.LiteralStarts[row+1])
	}
	return Template{
		LiteralBytes: table.LiteralBytes[literalStart:literalEnd],
		Ops:          table.Ops[opStart:opEnd],
		Args:         table.Args[opStart:opEnd],
		MaxBytes:     table.MaxBytes[row],
	}
}
