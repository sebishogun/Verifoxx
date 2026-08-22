package compile

import (
	"math"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

type instructionCandidate struct {
	listStart       uint32
	operandStart    uint32
	field           schema.FieldID
	value           schema.ValueID
	evidenceKind    schema.EvidenceKindID
	evidenceState   schema.EvidenceStateID
	evidenceSubject schema.SymbolID
	evidenceScope   schema.SymbolID
	evidenceTiming  schema.SymbolID
	listCount       uint16
	operandCount    uint16
	opcode          program.Opcode
}

func (l *Lowerer) prepareCandidateScratch(nodeCount, listHint, operandHint int) {
	l.candidateOpcodes = resizeSlots(l.candidateOpcodes, nodeCount)[:0]
	l.candidateFields = resizeSlots(l.candidateFields, nodeCount)[:0]
	l.candidateValues = resizeSlots(l.candidateValues, nodeCount)[:0]
	l.candidateListStarts = resizeSlots(l.candidateListStarts, nodeCount)[:0]
	l.candidateListCounts = resizeSlots(l.candidateListCounts, nodeCount)[:0]
	l.candidateOperandStarts = resizeSlots(l.candidateOperandStarts, nodeCount)[:0]
	l.candidateOperandCounts = resizeSlots(l.candidateOperandCounts, nodeCount)[:0]
	l.candidateEvidenceKinds = resizeSlots(l.candidateEvidenceKinds, nodeCount)[:0]
	l.candidateEvidenceState = resizeSlots(l.candidateEvidenceState, nodeCount)[:0]
	l.candidateEvidenceSubjects = resizeSlots(l.candidateEvidenceSubjects, nodeCount)[:0]
	l.candidateEvidenceScopes = resizeSlots(l.candidateEvidenceScopes, nodeCount)[:0]
	l.candidateEvidenceTimings = resizeSlots(l.candidateEvidenceTimings, nodeCount)[:0]
	l.candidateRootFlags = resizeSlots(l.candidateRootFlags, nodeCount)[:0]
	l.candidateNodes = resizeSlots(l.candidateNodes, nodeCount)[:0]
	l.candidateSourceStarts = resizeSlots(l.candidateSourceStarts, nodeCount)[:0]
	l.candidateSourceEnds = resizeSlots(l.candidateSourceEnds, nodeCount)[:0]
	l.candidateListValues = resizeSlots(l.candidateListValues, listHint)[:0]
	l.candidateOperands = resizeSlots(l.candidateOperands, operandHint)[:0]
	l.candidateLive = resizeSlots(l.candidateLive, nodeCount)
	l.candidateToFinal = resizeSlots(l.candidateToFinal, nodeCount)
	slots := slotSize(nodeCount)
	l.candidateHashes = resizeSlots(l.candidateHashes, slots)
	l.candidateIDs = resizeSlots(l.candidateIDs, slots)
}

func mixInstructionHash(hash, value uint64) uint64 {
	hash ^= value + 0x9e3779b97f4a7c15 + (hash << 6) + (hash >> 2)
	hash ^= hash >> 30
	hash *= 0xbf58476d1ce4e5b9
	return hash ^ (hash >> 27)
}

func (l *Lowerer) candidateHash(candidate instructionCandidate) uint64 {
	hash := mixInstructionHash(0x6a09e667f3bcc909, uint64(candidate.opcode))
	hash = mixInstructionHash(hash, uint64(candidate.field))
	hash = mixInstructionHash(hash, uint64(candidate.value))
	hash = mixInstructionHash(hash, uint64(candidate.evidenceKind))
	hash = mixInstructionHash(hash, uint64(candidate.evidenceState))
	hash = mixInstructionHash(hash, uint64(candidate.evidenceSubject))
	hash = mixInstructionHash(hash, uint64(candidate.evidenceScope))
	hash = mixInstructionHash(hash, uint64(candidate.evidenceTiming))
	hash = mixInstructionHash(hash, uint64(candidate.listCount))
	for i := uint32(0); i < uint32(candidate.listCount); i++ {
		hash = mixInstructionHash(hash, uint64(l.candidateListValues[candidate.listStart+i]))
	}
	hash = mixInstructionHash(hash, uint64(candidate.operandCount))
	for i := uint32(0); i < uint32(candidate.operandCount); i++ {
		hash = mixInstructionHash(hash, uint64(l.candidateOperands[candidate.operandStart+i]))
	}
	return hash
}

func equalValueRange(values []schema.ValueID, a uint32, ac uint16, b uint32, bc uint16) bool {
	if ac != bc {
		return false
	}
	for i := uint32(0); i < uint32(ac); i++ {
		if values[a+i] != values[b+i] {
			return false
		}
	}
	return true
}

func equalInstructionRange(values []schema.InstructionID, a uint32, ac uint16, b uint32, bc uint16) bool {
	if ac != bc {
		return false
	}
	for i := uint32(0); i < uint32(ac); i++ {
		if values[a+i] != values[b+i] {
			return false
		}
	}
	return true
}

func (l *Lowerer) candidateEqual(id schema.InstructionID, candidate instructionCandidate) bool {
	index := int(id) - 1
	return index >= 0 && index < len(l.candidateOpcodes) &&
		l.candidateOpcodes[index] == candidate.opcode &&
		l.candidateFields[index] == candidate.field &&
		l.candidateValues[index] == candidate.value &&
		l.candidateEvidenceKinds[index] == candidate.evidenceKind &&
		l.candidateEvidenceState[index] == candidate.evidenceState &&
		l.candidateEvidenceSubjects[index] == candidate.evidenceSubject &&
		l.candidateEvidenceScopes[index] == candidate.evidenceScope &&
		l.candidateEvidenceTimings[index] == candidate.evidenceTiming &&
		equalValueRange(l.candidateListValues,
			l.candidateListStarts[index], l.candidateListCounts[index], candidate.listStart, candidate.listCount) &&
		equalInstructionRange(l.candidateOperands,
			l.candidateOperandStarts[index], l.candidateOperandCounts[index], candidate.operandStart, candidate.operandCount)
}

func (l *Lowerer) appendCandidate(node schema.NodeID, span ast.SourceSpan, candidate instructionCandidate) (schema.InstructionID, error) {
	if len(l.candidateIDs) == 0 || uint64(len(l.candidateOpcodes)) >= uint64(math.MaxUint32) {
		return 0, ErrProgramTooLarge
	}
	hash := l.candidateHash(candidate)
	mask := uint64(len(l.candidateIDs) - 1)
	slot := int(hash & mask)
	for probes := 0; probes < len(l.candidateIDs); probes++ {
		id := l.candidateIDs[slot]
		if id == 0 {
			id = schema.InstructionID(len(l.candidateOpcodes) + 1)
			l.candidateOpcodes = append(l.candidateOpcodes, candidate.opcode)
			l.candidateFields = append(l.candidateFields, candidate.field)
			l.candidateValues = append(l.candidateValues, candidate.value)
			l.candidateListStarts = append(l.candidateListStarts, candidate.listStart)
			l.candidateListCounts = append(l.candidateListCounts, candidate.listCount)
			l.candidateOperandStarts = append(l.candidateOperandStarts, candidate.operandStart)
			l.candidateOperandCounts = append(l.candidateOperandCounts, candidate.operandCount)
			l.candidateEvidenceKinds = append(l.candidateEvidenceKinds, candidate.evidenceKind)
			l.candidateEvidenceState = append(l.candidateEvidenceState, candidate.evidenceState)
			l.candidateEvidenceSubjects = append(l.candidateEvidenceSubjects, candidate.evidenceSubject)
			l.candidateEvidenceScopes = append(l.candidateEvidenceScopes, candidate.evidenceScope)
			l.candidateEvidenceTimings = append(l.candidateEvidenceTimings, candidate.evidenceTiming)
			l.candidateRootFlags = append(l.candidateRootFlags, program.RootFlags(l.nodeRoots[node-1]))
			l.candidateNodes = append(l.candidateNodes, node)
			l.candidateSourceStarts = append(l.candidateSourceStarts, span.Start)
			l.candidateSourceEnds = append(l.candidateSourceEnds, span.End)
			l.candidateHashes[slot] = hash
			l.candidateIDs[slot] = id
			return id, nil
		}
		if l.candidateHashes[slot] == hash && l.candidateEqual(id, candidate) {
			l.candidateRootFlags[id-1] |= program.RootFlags(l.nodeRoots[node-1])
			return id, nil
		}
		slot = (slot + 1) & int(mask)
	}
	return 0, ErrProgramTooLarge
}

func (l *Lowerer) appendCanonicalList(doc *ast.Document, node schema.NodeID) (uint32, uint16, error) {
	values, ok := doc.InValues(node)
	if !ok {
		return 0, 0, ErrInvalidDocument
	}
	if len(values) > math.MaxUint16 {
		return 0, 0, ErrProgramTooLarge
	}
	if uint64(len(l.candidateListValues))+uint64(len(values)) > uint64(math.MaxUint32) {
		return 0, 0, ErrProgramTooLarge
	}
	start := uint32(len(l.candidateListValues))
	for _, value := range values {
		canonical, err := l.canonicalValue(value)
		if err != nil {
			return 0, 0, err
		}
		l.candidateListValues = append(l.candidateListValues, canonical)
	}
	return start, uint16(len(values)), nil
}

func (l *Lowerer) appendGroupOperands(doc *ast.Document, node schema.NodeID, kind ast.NodeKind) (uint32, uint16, error) {
	children, ok := doc.GroupChildren(node)
	if !ok || len(children) == 0 {
		return 0, 0, ErrInvalidDocument
	}
	if uint64(len(l.candidateOperands)) > uint64(math.MaxUint32) {
		return 0, 0, ErrProgramTooLarge
	}
	start := uint32(len(l.candidateOperands))
	count := uint64(0)
	for _, child := range children {
		childKind, ok := doc.Kind(child)
		if !ok {
			return 0, 0, ErrInvalidDocument
		}
		if childKind == kind {
			index := int(child) - 1
			childStart := l.nodeFlatStart[index]
			childCount := l.nodeFlatCount[index]
			if childCount == 0 || uint64(childStart)+uint64(childCount) > uint64(len(l.candidateOperands)) {
				return 0, 0, ErrInvalidDocument
			}
			count += uint64(childCount)
			if count > math.MaxUint16 || uint64(len(l.candidateOperands))+uint64(childCount) > uint64(math.MaxUint32) {
				return 0, 0, ErrProgramTooLarge
			}
			l.candidateOperands = append(l.candidateOperands,
				l.candidateOperands[childStart:childStart+uint32(childCount)]...)
			continue
		}
		canonical := l.nodeCanon[child-1]
		if canonical == 0 {
			return 0, 0, ErrInvalidDocument
		}
		count++
		if count > math.MaxUint16 || uint64(len(l.candidateOperands))+1 > uint64(math.MaxUint32) {
			return 0, 0, ErrProgramTooLarge
		}
		l.candidateOperands = append(l.candidateOperands, canonical)
	}
	return start, uint16(count), nil
}

func (l *Lowerer) internInstructionCandidate(dst *program.Program, doc *ast.Document, node schema.NodeID) error {
	kind, ok := doc.Kind(node)
	if !ok {
		return ErrInvalidDocument
	}
	span, ok := doc.Span(node)
	if !ok {
		return ErrInvalidDocument
	}
	var candidate instructionCandidate
	switch kind {
	case ast.NodeKindCompare:
		field, op, value, ok := doc.Compare(node)
		if !ok {
			return ErrInvalidDocument
		}
		opcode, ok := compareOpcode(op)
		if !ok {
			return ErrInvalidDocument
		}
		candidate.opcode = opcode
		candidate.field = field
		if op == ast.CompareOpIn {
			start, count, err := l.appendCanonicalList(doc, node)
			if err != nil {
				return err
			}
			candidate.listStart, candidate.listCount = start, count
		} else if value != 0 {
			canonical, err := l.canonicalValue(value)
			if err != nil {
				return err
			}
			candidate.value = canonical
		}
	case ast.NodeKindAll, ast.NodeKindAny:
		if kind == ast.NodeKindAll {
			candidate.opcode = program.OpcodeAll
		} else {
			candidate.opcode = program.OpcodeAny
		}
		start, count, err := l.appendGroupOperands(doc, node, kind)
		if err != nil {
			return err
		}
		candidate.operandStart, candidate.operandCount = start, count
		l.nodeFlatStart[node-1], l.nodeFlatCount[node-1] = start, count
	case ast.NodeKindNot:
		child, ok := doc.NotChild(node)
		if !ok {
			return ErrInvalidDocument
		}
		if uint64(len(l.candidateOperands))+1 > uint64(math.MaxUint32) {
			return ErrProgramTooLarge
		}
		operand := l.nodeCanon[child-1]
		if operand == 0 {
			return ErrInvalidDocument
		}
		candidate.opcode = program.OpcodeNot
		candidate.operandStart = uint32(len(l.candidateOperands))
		candidate.operandCount = 1
		l.candidateOperands = append(l.candidateOperands, operand)
	case ast.NodeKindEvidence:
		kindID, stateID, subject, scope, timing, ok := doc.EvidenceMatch(node)
		if !ok {
			return ErrInvalidDocument
		}
		candidate.opcode = program.OpcodeEvidence
		candidate.evidenceKind = kindID
		candidate.evidenceState = stateID
		var err error
		candidate.evidenceSubject, err = l.optionalSymbolForValue(dst, doc, subject)
		if err != nil {
			return err
		}
		candidate.evidenceScope, err = l.optionalSymbolForValue(dst, doc, scope)
		if err != nil {
			return err
		}
		candidate.evidenceTiming, err = l.optionalSymbolForValue(dst, doc, timing)
		if err != nil {
			return err
		}
	default:
		return ErrInvalidDocument
	}
	id, err := l.appendCandidate(node, span, candidate)
	if err != nil {
		return err
	}
	l.nodeCanon[node-1] = id
	return nil
}

func (l *Lowerer) markLiveCandidates() error {
	count := len(l.candidateOpcodes)
	l.candidateLive = resizeSlots(l.candidateLive, count)
	for i, flags := range l.candidateRootFlags {
		if flags != 0 {
			l.candidateLive[i] = 1
		}
	}
	for i := count - 1; i >= 0; i-- {
		if l.candidateLive[i] == 0 {
			continue
		}
		start := l.candidateOperandStarts[i]
		end := uint64(start) + uint64(l.candidateOperandCounts[i])
		if end > uint64(len(l.candidateOperands)) {
			return ErrInvalidDocument
		}
		for _, operand := range l.candidateOperands[start:uint32(end)] {
			if operand == 0 || uint64(operand) > uint64(count) {
				return ErrInvalidDocument
			}
			l.candidateLive[operand-1] = 1
		}
	}
	return nil
}

func (l *Lowerer) compactInstructions(dst *program.Program, doc *ast.Document) error {
	if err := l.markLiveCandidates(); err != nil {
		return err
	}
	count := len(l.candidateOpcodes)
	l.candidateToFinal = resizeSlots(l.candidateToFinal, count)
	liveCount := 0
	listTotal, operandTotal := uint64(0), uint64(0)
	for i := 0; i < count; i++ {
		if l.candidateLive[i] == 0 {
			continue
		}
		liveCount++
		l.candidateToFinal[i] = schema.InstructionID(liveCount)
		listTotal += uint64(l.candidateListCounts[i])
		operandTotal += uint64(l.candidateOperandCounts[i])
	}
	if listTotal > math.MaxUint32 || operandTotal > math.MaxUint32 {
		return ErrProgramTooLarge
	}
	l.prepareFinalInstructionColumns(dst, liveCount, int(listTotal), int(operandTotal))
	row, listPos, operandPos := 0, uint32(0), uint32(0)
	for i := 0; i < count; i++ {
		if l.candidateLive[i] == 0 {
			continue
		}
		dst.Opcodes[row] = l.candidateOpcodes[i]
		dst.Fields[row] = l.candidateFields[i]
		dst.Values[row] = l.candidateValues[i]
		dst.EvidenceKinds[row] = l.candidateEvidenceKinds[i]
		dst.EvidenceStates[row] = l.candidateEvidenceState[i]
		dst.EvidenceSubjects[row] = l.candidateEvidenceSubjects[i]
		dst.EvidenceScopes[row] = l.candidateEvidenceScopes[i]
		dst.EvidenceTimings[row] = l.candidateEvidenceTimings[i]
		dst.RootFlags[row] = l.candidateRootFlags[i]
		dst.InstructionNodes[row] = l.candidateNodes[i]
		dst.InstructionSourceStarts[row] = l.candidateSourceStarts[i]
		dst.InstructionSourceEnds[row] = l.candidateSourceEnds[i]
		listStart := l.candidateListStarts[i]
		listCount := l.candidateListCounts[i]
		if uint64(listStart)+uint64(listCount) > uint64(len(l.candidateListValues)) {
			return ErrInvalidDocument
		}
		dst.ListCounts[row] = listCount
		if listCount != 0 {
			dst.ListStarts[row] = listPos
			copy(dst.ListValues[listPos:], l.candidateListValues[listStart:listStart+uint32(listCount)])
			listPos += uint32(listCount)
		}
		operandStart := l.candidateOperandStarts[i]
		operandCount := l.candidateOperandCounts[i]
		if uint64(operandStart)+uint64(operandCount) > uint64(len(l.candidateOperands)) {
			return ErrInvalidDocument
		}
		dst.OperandCounts[row] = operandCount
		if operandCount != 0 {
			dst.OperandStarts[row] = operandPos
		}
		for _, old := range l.candidateOperands[operandStart : operandStart+uint32(operandCount)] {
			if old == 0 || uint64(old) > uint64(len(l.candidateToFinal)) {
				return ErrInvalidDocument
			}
			final := l.candidateToFinal[old-1]
			if final == 0 {
				return ErrInvalidDocument
			}
			dst.Operands[operandPos] = final
			operandPos++
		}
		row++
	}
	return l.buildNodeInstructionMap(dst, doc)
}

func (l *Lowerer) prepareFinalInstructionColumns(dst *program.Program, rows, lists, operands int) {
	dst.Opcodes = resizeSlots(dst.Opcodes, rows)
	dst.Fields = resizeSlots(dst.Fields, rows)
	dst.Values = resizeSlots(dst.Values, rows)
	dst.ListStarts = resizeSlots(dst.ListStarts, rows)
	dst.ListCounts = resizeSlots(dst.ListCounts, rows)
	dst.OperandStarts = resizeSlots(dst.OperandStarts, rows)
	dst.OperandCounts = resizeSlots(dst.OperandCounts, rows)
	dst.EvidenceKinds = resizeSlots(dst.EvidenceKinds, rows)
	dst.EvidenceStates = resizeSlots(dst.EvidenceStates, rows)
	dst.EvidenceSubjects = resizeSlots(dst.EvidenceSubjects, rows)
	dst.EvidenceScopes = resizeSlots(dst.EvidenceScopes, rows)
	dst.EvidenceTimings = resizeSlots(dst.EvidenceTimings, rows)
	dst.RootFlags = resizeSlots(dst.RootFlags, rows)
	dst.InstructionNodes = resizeSlots(dst.InstructionNodes, rows)
	dst.InstructionSourceStarts = resizeSlots(dst.InstructionSourceStarts, rows)
	dst.InstructionSourceEnds = resizeSlots(dst.InstructionSourceEnds, rows)
	dst.ListValues = resizeSlots(dst.ListValues, lists)
	dst.Operands = resizeSlots(dst.Operands, operands)
}

func (l *Lowerer) buildNodeInstructionMap(dst *program.Program, doc *ast.Document) error {
	nodes := doc.Len()
	dst.NodeInstructionStarts = resizeSlots(dst.NodeInstructionStarts, nodes)
	dst.NodeInstructionCounts = resizeSlots(dst.NodeInstructionCounts, nodes)
	total := uint64(0)
	for i := 0; i < nodes; i++ {
		canonical := l.nodeCanon[i]
		if canonical == 0 || uint64(canonical) > uint64(len(l.candidateToFinal)) {
			return ErrInvalidDocument
		}
		count := uint16(1)
		if l.candidateToFinal[canonical-1] == 0 {
			count = l.nodeFlatCount[i]
			if count == 0 {
				return ErrInvalidDocument
			}
		}
		dst.NodeInstructionStarts[i] = uint32(total)
		dst.NodeInstructionCounts[i] = count
		total += uint64(count)
		if total > math.MaxUint32 {
			return ErrProgramTooLarge
		}
	}
	if total > uint64(math.MaxInt) {
		return ErrProgramTooLarge
	}
	dst.NodeInstructionIDs = resizeSlots(dst.NodeInstructionIDs, int(total))
	position := uint32(0)
	for i := 0; i < nodes; i++ {
		canonical := l.nodeCanon[i]
		if final := l.candidateToFinal[canonical-1]; final != 0 {
			dst.NodeInstructionIDs[position] = final
			position++
			continue
		}
		start, count := l.nodeFlatStart[i], l.nodeFlatCount[i]
		if uint64(start)+uint64(count) > uint64(len(l.candidateOperands)) {
			return ErrInvalidDocument
		}
		for _, old := range l.candidateOperands[start : start+uint32(count)] {
			if old == 0 || uint64(old) > uint64(len(l.candidateToFinal)) {
				return ErrInvalidDocument
			}
			final := l.candidateToFinal[old-1]
			if final == 0 {
				return ErrInvalidDocument
			}
			dst.NodeInstructionIDs[position] = final
			position++
		}
	}
	return nil
}
