package frontend

import (
	"bytes"
	"unicode/utf8"

	public "github.com/sebishogun/nornrune/frontend"
	"github.com/sebishogun/nornrune/internal/schema"
)

func (compiler *Compiler) validate(policy *public.Policy) []public.Diagnostic {
	compiler.diagnostics = compiler.diagnostics[:0]
	if policy == nil {
		compiler.addDiagnostic(public.CodeInvalidPolicy, 0, 0, public.Span{})
		return compiler.diagnostics
	}

	limits := public.DefaultLimits()
	stringBytes := uint64(len(policy.Name)) + uint64(len(policy.Version)) + uint64(len(policy.FieldBytes)) + uint64(len(policy.SymbolBytes))
	if exceedsLimits(policy, limits, stringBytes) {
		compiler.addDiagnostic(public.CodeLimit, 0, 0, public.Span{})
		public.SortDiagnostics(compiler.diagnostics)
		return compiler.diagnostics
	}
	if !utf8.Valid(policy.Source) || !validMetadata(policy.Name) || !validMetadata(policy.Version) || !policy.Default.Valid() {
		compiler.addDiagnostic(public.CodeInvalidPolicy, 0, 0, public.Span{})
	}

	fieldRows := compiler.validateFields(policy)
	literalRows := compiler.validateLiterals(policy)
	nodeRows := compiler.validateNodes(policy, fieldRows, literalRows, limits)
	compiler.validateReachability(policy, nodeRows)
	public.SortDiagnostics(compiler.diagnostics)
	return compiler.diagnostics
}

func exceedsLimits(policy *public.Policy, limits public.Limits, stringBytes uint64) bool {
	return uint64(len(policy.Source)) > uint64(limits.MaxSourceBytes) ||
		anyLengthOver(limits.MaxNodes,
			len(policy.NodeKinds), len(policy.NodeOps), len(policy.NodeFields), len(policy.NodeLiterals),
			len(policy.NodeChildStarts), len(policy.NodeChildCounts), len(policy.NodeListStarts), len(policy.NodeListCounts),
			len(policy.NodeSourceStarts), len(policy.NodeSourceEnds),
		) ||
		anyLengthOver(limits.MaxFields,
			len(policy.FieldNameStarts), len(policy.FieldNameLengths), len(policy.FieldTargetStarts),
			len(policy.FieldTargetLengths), len(policy.FieldKinds), len(policy.FieldGroups),
		) ||
		anyLengthOver(limits.MaxLiterals,
			len(policy.LiteralKinds), len(policy.LiteralRefs), len(policy.SymbolStarts), len(policy.SymbolLengths),
			len(policy.IntegerValues), len(policy.BooleanValues),
		) ||
		uint64(len(policy.ChildNodeIDs))+uint64(len(policy.ListLiteralIDs)) > uint64(limits.MaxChildren) ||
		stringBytes > uint64(limits.MaxStringBytes)
}

func anyLengthOver(limit uint32, lengths ...int) bool {
	for _, length := range lengths {
		if uint64(length) > uint64(limit) {
			return true
		}
	}
	return false
}

func (compiler *Compiler) validateFields(policy *public.Policy) int {
	n := len(policy.FieldKinds)
	if !allLengthsEqual(n,
		len(policy.FieldNameStarts), len(policy.FieldNameLengths),
		len(policy.FieldTargetStarts), len(policy.FieldTargetLengths), len(policy.FieldGroups),
	) {
		compiler.addDiagnostic(public.CodeInvalidPolicy, 0, 0, public.Span{})
	}
	rows := minLength(
		n, len(policy.FieldNameStarts), len(policy.FieldNameLengths),
		len(policy.FieldTargetStarts), len(policy.FieldTargetLengths), len(policy.FieldGroups),
	)
	tableSize := fieldTableSize(rows)
	compiler.sourceFieldSlots = resizeZeroed(compiler.sourceFieldSlots, tableSize)
	compiler.targetFieldSlots = resizeZeroed(compiler.targetFieldSlots, tableSize)
	for row := 0; row < rows; row++ {
		field := public.FieldID(row + 1)
		name, nameOK := byteRange(policy.FieldBytes, policy.FieldNameStarts[row], policy.FieldNameLengths[row])
		target, targetOK := byteRange(policy.FieldBytes, policy.FieldTargetStarts[row], policy.FieldTargetLengths[row])
		if !nameOK || !targetOK || !validPath(name) || !validPath(target) ||
			!policy.FieldKinds[row].Valid() || !policy.FieldGroups[row].Valid() {
			compiler.addDiagnostic(public.CodeInvalidBinding, uint32(row+1), field, public.Span{})
			continue
		}
		duplicateName := duplicateField(policy, name, row, policy.FieldNameStarts, policy.FieldNameLengths, compiler.sourceFieldSlots)
		duplicateTarget := duplicateField(policy, target, row, policy.FieldTargetStarts, policy.FieldTargetLengths, compiler.targetFieldSlots)
		if duplicateName || duplicateTarget {
			compiler.addDiagnostic(public.CodeDuplicate, uint32(row+1), field, public.Span{})
		}
	}
	return rows
}

func fieldTableSize(rows int) int {
	size := 4
	for size < rows*2 {
		size <<= 1
	}
	return size
}

func duplicateField(policy *public.Policy, value []byte, row int, starts, lengths []uint32, slots []uint32) bool {
	mask := uint64(len(slots) - 1)
	slot := int(schema.HashSymbol(value) & mask)
	for {
		previous := slots[slot]
		if previous == 0 {
			slots[slot] = uint32(row + 1)
			return false
		}
		previousRow := int(previous - 1)
		previousValue, _ := byteRange(policy.FieldBytes, starts[previousRow], lengths[previousRow])
		if bytes.Equal(value, previousValue) {
			return true
		}
		slot = (slot + 1) & int(mask)
	}
}

func (compiler *Compiler) validateLiterals(policy *public.Policy) int {
	n := len(policy.LiteralKinds)
	if len(policy.LiteralRefs) != n || len(policy.SymbolStarts) != len(policy.SymbolLengths) {
		compiler.addDiagnostic(public.CodeInvalidPolicy, 0, 0, public.Span{})
	}
	for row := 0; row < minLength(len(policy.SymbolStarts), len(policy.SymbolLengths)); row++ {
		value, ok := byteRange(policy.SymbolBytes, policy.SymbolStarts[row], policy.SymbolLengths[row])
		if !ok || !utf8.Valid(value) {
			compiler.addDiagnostic(public.CodeType, uint32(row+1), 0, public.Span{})
		}
	}
	for row, value := range policy.BooleanValues {
		if value > 1 {
			compiler.addDiagnostic(public.CodeType, uint32(row+1), 0, public.Span{})
		}
	}
	rows := minLength(n, len(policy.LiteralRefs))
	for row := 0; row < rows; row++ {
		kind := policy.LiteralKinds[row]
		ref := uint64(policy.LiteralRefs[row])
		valid := kind.Valid()
		switch kind {
		case public.ValueKindString:
			valid = ref < uint64(len(policy.SymbolStarts)) && ref < uint64(len(policy.SymbolLengths))
			if valid {
				_, valid = byteRange(policy.SymbolBytes, policy.SymbolStarts[ref], policy.SymbolLengths[ref])
			}
		case public.ValueKindInteger:
			valid = ref < uint64(len(policy.IntegerValues))
		case public.ValueKindBoolean:
			valid = ref < uint64(len(policy.BooleanValues)) && policy.BooleanValues[ref] <= 1
		}
		if !valid {
			compiler.addDiagnostic(public.CodeType, uint32(row+1), 0, public.Span{})
		}
	}
	return rows
}

func (compiler *Compiler) validateNodes(policy *public.Policy, fieldRows, literalRows int, limits public.Limits) int {
	n := len(policy.NodeKinds)
	if n == 0 || !allLengthsEqual(n,
		len(policy.NodeOps), len(policy.NodeFields), len(policy.NodeLiterals),
		len(policy.NodeChildStarts), len(policy.NodeChildCounts),
		len(policy.NodeListStarts), len(policy.NodeListCounts),
		len(policy.NodeSourceStarts), len(policy.NodeSourceEnds),
	) {
		compiler.addDiagnostic(public.CodeInvalidPolicy, 0, 0, public.Span{})
	}
	rows := minLength(
		n, len(policy.NodeOps), len(policy.NodeFields), len(policy.NodeLiterals),
		len(policy.NodeChildStarts), len(policy.NodeChildCounts),
		len(policy.NodeListStarts), len(policy.NodeListCounts),
		len(policy.NodeSourceStarts), len(policy.NodeSourceEnds),
	)
	compiler.depths = resizeZeroed(compiler.depths, rows)
	childCursor, listCursor := uint64(0), uint64(0)
	for row := 0; row < rows; row++ {
		span := public.Span{Start: policy.NodeSourceStarts[row], End: policy.NodeSourceEnds[row]}
		diagnosticSpan := span
		if !validSpan(policy.Source, span) {
			compiler.addDiagnostic(public.CodeInvalidPolicy, uint32(row+1), policy.NodeFields[row], public.Span{})
			diagnosticSpan = public.Span{}
		}

		kind := policy.NodeKinds[row]
		op := policy.NodeOps[row]
		field := policy.NodeFields[row]
		literal := policy.NodeLiterals[row]
		childStart, childCount := policy.NodeChildStarts[row], uint32(policy.NodeChildCounts[row])
		listStart, listCount := policy.NodeListStarts[row], uint32(policy.NodeListCounts[row])
		childrenOK := validRange(childStart, childCount, len(policy.ChildNodeIDs))
		listOK := validRange(listStart, listCount, len(policy.ListLiteralIDs))
		childrenOwned, listOwned := childCount == 0, listCount == 0
		if childCount != 0 {
			childrenOwned = childrenOK && uint64(childStart) == childCursor
			if childrenOwned {
				childCursor += uint64(childCount)
			} else {
				compiler.addDiagnostic(public.CodeInvalidPolicy, uint32(row+1), field, diagnosticSpan)
			}
		}
		if listCount != 0 {
			listOwned = listOK && uint64(listStart) == listCursor
			if listOwned {
				listCursor += uint64(listCount)
			} else {
				compiler.addDiagnostic(public.CodeInvalidPolicy, uint32(row+1), field, diagnosticSpan)
			}
		}
		depth := uint32(1)

		if !kind.Valid() {
			compiler.addDiagnostic(public.CodeInvalidPolicy, uint32(row+1), field, diagnosticSpan)
			continue
		}
		switch kind {
		case public.NodeKindBoolean:
			if op != public.CompareOpInvalid || field != 0 || childCount != 0 || listCount != 0 || !childrenOK || !listOK {
				compiler.addDiagnostic(public.CodeInvalidPolicy, uint32(row+1), field, diagnosticSpan)
			}
			if literal == 0 || int(literal) > literalRows || policy.LiteralKinds[literal-1] != public.ValueKindBoolean {
				compiler.addDiagnostic(public.CodeType, uint32(row+1), field, diagnosticSpan)
			}

		case public.NodeKindDefined:
			if op != public.CompareOpInvalid || literal != 0 || childCount != 0 || listCount != 0 || !childrenOK || !listOK {
				compiler.addDiagnostic(public.CodeInvalidPolicy, uint32(row+1), field, diagnosticSpan)
			}
			if _, fieldOK := policyFieldKind(policy, field, fieldRows); !fieldOK {
				compiler.addDiagnostic(public.CodeUnknownField, uint32(row+1), field, diagnosticSpan)
			}

		case public.NodeKindCompare:
			if !op.Valid() || childCount != 0 || !childrenOK {
				compiler.addDiagnostic(public.CodeInvalidPolicy, uint32(row+1), field, diagnosticSpan)
			}
			fieldKind, fieldOK := policyFieldKind(policy, field, fieldRows)
			if !fieldOK {
				compiler.addDiagnostic(public.CodeUnknownField, uint32(row+1), field, diagnosticSpan)
			}
			if op == public.CompareOpIn {
				if literal != 0 || listCount == 0 || !listOK {
					compiler.addDiagnostic(public.CodeInvalidPolicy, uint32(row+1), field, diagnosticSpan)
				}
				if listOK && listOwned {
					for offset := uint32(0); offset < listCount; offset++ {
						id := policy.ListLiteralIDs[listStart+offset]
						kind, ok := policyLiteralKind(policy, id, literalRows)
						if !ok || !fieldOK || kind != fieldKind {
							compiler.addDiagnostic(public.CodeType, uint32(row+1), field, diagnosticSpan)
							break
						}
					}
				}
			} else {
				if listCount != 0 || !listOK {
					compiler.addDiagnostic(public.CodeInvalidPolicy, uint32(row+1), field, diagnosticSpan)
				}
				literalKind, literalOK := policyLiteralKind(policy, literal, literalRows)
				if !literalOK || !fieldOK || literalKind != fieldKind || orderedOperation(op) && fieldKind != public.ValueKindInteger {
					compiler.addDiagnostic(public.CodeType, uint32(row+1), field, diagnosticSpan)
				}
			}

		case public.NodeKindAll, public.NodeKindAny:
			if op != public.CompareOpInvalid || field != 0 || literal != 0 || listCount != 0 || !listOK || childCount < 2 || !childrenOK {
				compiler.addDiagnostic(public.CodeInvalidPolicy, uint32(row+1), field, diagnosticSpan)
			}
			if childrenOK && childrenOwned {
				depth = compiler.childDepth(policy, row, childStart, childCount, diagnosticSpan)
			}

		case public.NodeKindNot:
			if op != public.CompareOpInvalid || field != 0 || literal != 0 || listCount != 0 || !listOK || childCount != 1 || !childrenOK {
				compiler.addDiagnostic(public.CodeInvalidPolicy, uint32(row+1), field, diagnosticSpan)
			}
			if childrenOK && childrenOwned && childCount == 1 {
				depth = compiler.childDepth(policy, row, childStart, childCount, diagnosticSpan)
			}
		}
		compiler.depths[row] = depth
		if uint64(depth) > uint64(limits.MaxDepth) {
			compiler.addDiagnostic(public.CodeLimit, uint32(row+1), field, diagnosticSpan)
		}
	}
	if childCursor != uint64(len(policy.ChildNodeIDs)) || listCursor != uint64(len(policy.ListLiteralIDs)) {
		compiler.addDiagnostic(public.CodeInvalidPolicy, 0, 0, public.Span{})
	}
	return rows
}

func (compiler *Compiler) childDepth(policy *public.Policy, row int, start, count uint32, span public.Span) uint32 {
	maxDepth := uint32(0)
	valid := true
	for offset := uint32(0); offset < count; offset++ {
		child := policy.ChildNodeIDs[start+offset]
		if child == 0 || uint64(child) > uint64(len(policy.NodeKinds)) || uint64(child) >= uint64(row+1) {
			compiler.addDiagnostic(public.CodeInvalidPolicy, uint32(row+1), 0, span)
			valid = false
			continue
		}
		depth := compiler.depths[child-1]
		if depth == 0 {
			valid = false
			continue
		}
		if depth > maxDepth {
			maxDepth = depth
		}
	}
	if !valid {
		return 0
	}
	return maxDepth + 1
}

func (compiler *Compiler) validateReachability(policy *public.Policy, nodeRows int) {
	if policy.Root == 0 || uint64(policy.Root) > uint64(nodeRows) {
		compiler.addDiagnostic(public.CodeInvalidPolicy, 0, 0, public.Span{})
		return
	}
	compiler.reachable = resizeZeroed(compiler.reachable, nodeRows)
	compiler.stack = append(compiler.stack[:0], policy.Root)
	for len(compiler.stack) != 0 {
		last := len(compiler.stack) - 1
		node := compiler.stack[last]
		compiler.stack = compiler.stack[:last]
		if node == 0 || int(node) > nodeRows || compiler.reachable[node-1] != 0 {
			continue
		}
		compiler.reachable[node-1] = 1
		row := int(node - 1)
		kind := policy.NodeKinds[row]
		if kind != public.NodeKindAll && kind != public.NodeKindAny && kind != public.NodeKindNot {
			continue
		}
		start := policy.NodeChildStarts[row]
		count := uint32(policy.NodeChildCounts[row])
		if !validRange(start, count, len(policy.ChildNodeIDs)) {
			continue
		}
		for offset := uint32(0); offset < count; offset++ {
			compiler.stack = append(compiler.stack, policy.ChildNodeIDs[start+offset])
		}
	}
	for row, reached := range compiler.reachable {
		if reached == 0 {
			compiler.addDiagnostic(public.CodeInvalidPolicy, uint32(row+1), policy.NodeFields[row], nodeSpan(policy, row))
		}
	}
}

func (compiler *Compiler) addDiagnostic(code public.DiagnosticCode, row uint32, field public.FieldID, span public.Span) {
	if len(compiler.diagnostics) >= int(public.DefaultLimits().MaxDiagnostics) {
		return
	}
	compiler.diagnostics = append(compiler.diagnostics, public.Diagnostic{Code: code, Row: row, Field: field, Span: span})
}

func nodeSpan(policy *public.Policy, row int) public.Span {
	if row < 0 || row >= len(policy.NodeSourceStarts) || row >= len(policy.NodeSourceEnds) {
		return public.Span{}
	}
	span := public.Span{Start: policy.NodeSourceStarts[row], End: policy.NodeSourceEnds[row]}
	if !validSpan(policy.Source, span) {
		return public.Span{}
	}
	return span
}

func validMetadata(value []byte) bool {
	if len(value) == 0 || !utf8.Valid(value) {
		return false
	}
	for len(value) != 0 {
		r, size := utf8.DecodeRune(value)
		if r < ' ' || r == 0x7f {
			return false
		}
		value = value[size:]
	}
	return true
}

func validPath(value []byte) bool {
	if len(value) == 0 {
		return false
	}
	segmentStart := true
	for _, character := range value {
		if character == '.' {
			if segmentStart {
				return false
			}
			segmentStart = true
			continue
		}
		if segmentStart {
			if !asciiLetter(character) && character != '_' {
				return false
			}
			segmentStart = false
			continue
		}
		if !asciiLetter(character) && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return !segmentStart
}

func asciiLetter(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func validSpan(source []byte, span public.Span) bool {
	if span.Start > span.End || uint64(span.End) > uint64(len(source)) {
		return false
	}
	return byteBoundary(source, span.Start) && byteBoundary(source, span.End)
}

func byteBoundary(source []byte, offset uint32) bool {
	return int(offset) == len(source) || utf8.RuneStart(source[offset])
}

func byteRange(slab []byte, start, length uint32) ([]byte, bool) {
	end := uint64(start) + uint64(length)
	if end > uint64(len(slab)) {
		return nil, false
	}
	return slab[int(start):int(end)], true
}

func policyFieldKind(policy *public.Policy, id public.FieldID, rows int) (public.ValueKind, bool) {
	if id == 0 || int(id) > rows {
		return public.ValueKindInvalid, false
	}
	return policy.FieldKinds[id-1], true
}

func policyLiteralKind(policy *public.Policy, id public.LiteralID, rows int) (public.ValueKind, bool) {
	if id == 0 || int(id) > rows {
		return public.ValueKindInvalid, false
	}
	return policy.LiteralKinds[id-1], true
}

func orderedOperation(operation public.CompareOp) bool {
	return operation >= public.CompareOpLess && operation <= public.CompareOpGreaterEqual
}

func validRange(start, count uint32, total int) bool {
	return uint64(start)+uint64(count) <= uint64(total)
}

func allLengthsEqual(want int, lengths ...int) bool {
	for _, length := range lengths {
		if length != want {
			return false
		}
	}
	return true
}

func minLength(lengths ...int) int {
	if len(lengths) == 0 {
		return 0
	}
	minimum := lengths[0]
	for _, length := range lengths[1:] {
		if length < minimum {
			minimum = length
		}
	}
	return minimum
}

func resizeZeroed[T ~uint8 | ~uint32](values []T, length int) []T {
	if cap(values) < length {
		return make([]T, length)
	}
	values = values[:length]
	clear(values)
	return values
}
