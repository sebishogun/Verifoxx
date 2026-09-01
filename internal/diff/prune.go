package diff

import (
	"bytes"
	"slices"

	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
)

func collectDependencies(plan *searchPlan, oldProgram, newProgram *program.Program, domain Domain) error {
	fieldCount := len(oldProgram.FieldNames)
	if len(newProgram.FieldNames) > fieldCount {
		fieldCount = len(newProgram.FieldNames)
	}
	plan.referenced = resizeBytes(plan.referenced, fieldCount)
	plan.changedFields = resizeBytes(plan.changedFields, fieldCount)
	markReferenced(plan.referenced, oldProgram)
	markReferenced(plan.referenced, newProgram)

	if !resultSemanticsEqual(oldProgram, newProgram) || len(oldProgram.Opcodes) != len(newProgram.Opcodes) {
		copy(plan.changedFields, plan.referenced)
	} else {
		plan.changedRows = resizeBytes(plan.changedRows, len(oldProgram.Opcodes))
		for row := range oldProgram.Opcodes {
			if !instructionRowEqual(oldProgram, newProgram, row) {
				plan.changedRows[row] = 1
			}
		}
		markReachableFields(plan, oldProgram)
		markReachableFields(plan, newProgram)
	}

	for id, referenced := range plan.referenced {
		if referenced == 0 {
			continue
		}
		fieldRow, ok := domainFieldRow(oldProgram, newProgram, schema.FieldID(id+1), domain)
		if !ok {
			return ErrInvalidDomain
		}
		plan.fieldRows = append(plan.fieldRows, fieldRow)
		plan.optionCounts = append(plan.optionCounts, uint32(len(domain.Fields[fieldRow].Values)))
		oldField, oldOK := lookupFieldID(oldProgram, domain.Fields[fieldRow].Name)
		newField, newOK := lookupFieldID(newProgram, domain.Fields[fieldRow].Name)
		if !oldOK || !newOK {
			return ErrInvalidDomain
		}
		plan.oldFieldIDs = append(plan.oldFieldIDs, oldField)
		plan.newFieldIDs = append(plan.newFieldIDs, newField)
		plan.changed = append(plan.changed, plan.changedFields[id])
	}
	sortPlanFields(plan, domain)
	return nil
}

func resizeBytes(values []uint8, length int) []uint8 {
	if cap(values) < length {
		values = make([]uint8, length)
	} else {
		values = values[:length]
		clear(values)
	}
	return values
}

func markReferenced(dst []uint8, compiled *program.Program) {
	for _, field := range compiled.Fields {
		if field != 0 && int(field) <= len(dst) {
			dst[field-1] = 1
		}
	}
}

func markReachableFields(plan *searchPlan, compiled *program.Program) {
	for row, changed := range plan.changedRows {
		if changed == 0 || row >= len(compiled.Opcodes) {
			continue
		}
		plan.stack = append(plan.stack[:0], schema.InstructionID(row+1))
		for len(plan.stack) != 0 {
			last := len(plan.stack) - 1
			id := plan.stack[last]
			plan.stack = plan.stack[:last]
			index := int(id - 1)
			if index < 0 || index >= len(compiled.Opcodes) {
				continue
			}
			if field := compiled.Fields[index]; field != 0 && int(field) <= len(plan.changedFields) {
				plan.changedFields[field-1] = 1
			}
			start := uint64(compiled.OperandStarts[index])
			count := uint64(compiled.OperandCounts[index])
			if start+count > uint64(len(compiled.Operands)) {
				continue
			}
			plan.stack = append(plan.stack, compiled.Operands[start:start+count]...)
		}
	}
}

func instructionRowEqual(oldProgram, newProgram *program.Program, row int) bool {
	if oldProgram.Opcodes[row] != newProgram.Opcodes[row] ||
		oldProgram.Fields[row] != newProgram.Fields[row] ||
		oldProgram.RootFlags[row] != newProgram.RootFlags[row] ||
		!semanticValuePairEqual(oldProgram, newProgram, oldProgram.Values[row], newProgram.Values[row]) ||
		oldProgram.EvidenceKinds[row] != newProgram.EvidenceKinds[row] ||
		oldProgram.EvidenceStates[row] != newProgram.EvidenceStates[row] ||
		!symbolPairEqual(oldProgram, newProgram, oldProgram.EvidenceSubjects[row], newProgram.EvidenceSubjects[row]) ||
		!symbolPairEqual(oldProgram, newProgram, oldProgram.EvidenceScopes[row], newProgram.EvidenceScopes[row]) ||
		!symbolPairEqual(oldProgram, newProgram, oldProgram.EvidenceTimings[row], newProgram.EvidenceTimings[row]) {
		return false
	}
	oldStart, oldCount := oldProgram.ListStarts[row], oldProgram.ListCounts[row]
	newStart, newCount := newProgram.ListStarts[row], newProgram.ListCounts[row]
	if oldCount != newCount || uint64(oldStart)+uint64(oldCount) > uint64(len(oldProgram.ListValues)) ||
		uint64(newStart)+uint64(newCount) > uint64(len(newProgram.ListValues)) {
		return false
	}
	for offset := uint16(0); offset < oldCount; offset++ {
		if !semanticValuePairEqual(oldProgram, newProgram, oldProgram.ListValues[oldStart+uint32(offset)], newProgram.ListValues[newStart+uint32(offset)]) {
			return false
		}
	}
	oldOperandStart, oldOperandCount := oldProgram.OperandStarts[row], oldProgram.OperandCounts[row]
	newOperandStart, newOperandCount := newProgram.OperandStarts[row], newProgram.OperandCounts[row]
	if oldOperandCount != newOperandCount || uint64(oldOperandStart)+uint64(oldOperandCount) > uint64(len(oldProgram.Operands)) ||
		uint64(newOperandStart)+uint64(newOperandCount) > uint64(len(newProgram.Operands)) {
		return false
	}
	return slices.Equal(
		oldProgram.Operands[oldOperandStart:oldOperandStart+uint32(oldOperandCount)],
		newProgram.Operands[newOperandStart:newOperandStart+uint32(newOperandCount)],
	)
}

func resultSemanticsEqual(oldProgram, newProgram *program.Program) bool {
	return bytes.Equal(oldProgram.TemplateBytes, newProgram.TemplateBytes) &&
		slices.Equal(oldProgram.TemplateOps, newProgram.TemplateOps) &&
		slices.Equal(oldProgram.TemplateArgs, newProgram.TemplateArgs) &&
		slices.Equal(oldProgram.ExplanationRationaleTemplateIDs, newProgram.ExplanationRationaleTemplateIDs) &&
		slices.Equal(oldProgram.ExplanationUncertaintyTemplateIDs, newProgram.ExplanationUncertaintyTemplateIDs) &&
		slices.Equal(oldProgram.AssumptionTemplateIDs, newProgram.AssumptionTemplateIDs) &&
		slices.Equal(oldProgram.EvidenceIssueTemplateIDs, newProgram.EvidenceIssueTemplateIDs) &&
		slices.Equal(oldProgram.ClauseOnSatisfied, newProgram.ClauseOnSatisfied) &&
		slices.Equal(oldProgram.ClauseOnFalse, newProgram.ClauseOnFalse) &&
		slices.Equal(oldProgram.ClauseExplanationIDs, newProgram.ClauseExplanationIDs) &&
		slices.Equal(oldProgram.ClauseRemediationIDs, newProgram.ClauseRemediationIDs) &&
		slices.Equal(oldProgram.Outcomes.Precedence, newProgram.Outcomes.Precedence) &&
		slices.Equal(oldProgram.Outcomes.Terminal, newProgram.Outcomes.Terminal) &&
		slices.Equal(oldProgram.Remediations.Kinds, newProgram.Remediations.Kinds) &&
		slices.Equal(oldProgram.Remediations.Fields, newProgram.Remediations.Fields) &&
		slices.Equal(oldProgram.Remediations.EvidenceKinds, newProgram.Remediations.EvidenceKinds) &&
		slices.Equal(oldProgram.Resolutions.OutcomeIDs, newProgram.Resolutions.OutcomeIDs) &&
		slices.Equal(oldProgram.Resolutions.ExplanationIDs, newProgram.Resolutions.ExplanationIDs) &&
		slices.Equal(oldProgram.Resolutions.RemediationStarts, newProgram.Resolutions.RemediationStarts) &&
		slices.Equal(oldProgram.Resolutions.RemediationCounts, newProgram.Resolutions.RemediationCounts) &&
		slices.Equal(oldProgram.Resolutions.RemediationIDs, newProgram.Resolutions.RemediationIDs) &&
		semanticSymbolsEqual(oldProgram, newProgram) &&
		remediationValuesEqual(oldProgram, newProgram)
}

func remediationValuesEqual(oldProgram, newProgram *program.Program) bool {
	if len(oldProgram.Remediations.Values) != len(newProgram.Remediations.Values) {
		return false
	}
	for row, oldValue := range oldProgram.Remediations.Values {
		if !semanticValuePairEqual(oldProgram, newProgram, oldValue, newProgram.Remediations.Values[row]) {
			return false
		}
	}
	return true
}

func domainFieldRow(oldProgram, newProgram *program.Program, id schema.FieldID, domain Domain) (uint32, bool) {
	name, ok := fieldName(oldProgram, id)
	if !ok {
		name, ok = fieldName(newProgram, id)
	}
	if !ok {
		return 0, false
	}
	for row := range domain.Fields {
		if bytesEqualString(name, domain.Fields[row].Name) {
			return uint32(row), true
		}
	}
	return 0, false
}

func fieldName(compiled *program.Program, id schema.FieldID) ([]byte, bool) {
	row := int(id - 1)
	if row < 0 || row >= len(compiled.FieldNames) {
		return nil, false
	}
	return compiled.Symbol(compiled.FieldNames[row])
}

func bytesEqualString(value []byte, text string) bool {
	if len(value) != len(text) {
		return false
	}
	for index := range value {
		if value[index] != text[index] {
			return false
		}
	}
	return true
}

func lookupFieldID(compiled *program.Program, name string) (schema.FieldID, bool) {
	for row, symbol := range compiled.FieldNames {
		value, ok := compiled.Symbol(symbol)
		if ok && bytesEqualString(value, name) {
			return schema.FieldID(row + 1), true
		}
	}
	return 0, false
}

func sortPlanFields(plan *searchPlan, domain Domain) {
	for row := 1; row < len(plan.fieldRows); row++ {
		for current := row; current > 0 && planFieldLess(plan, domain, current, current-1); current-- {
			plan.fieldRows[current], plan.fieldRows[current-1] = plan.fieldRows[current-1], plan.fieldRows[current]
			plan.optionCounts[current], plan.optionCounts[current-1] = plan.optionCounts[current-1], plan.optionCounts[current]
			plan.oldFieldIDs[current], plan.oldFieldIDs[current-1] = plan.oldFieldIDs[current-1], plan.oldFieldIDs[current]
			plan.newFieldIDs[current], plan.newFieldIDs[current-1] = plan.newFieldIDs[current-1], plan.newFieldIDs[current]
			plan.changed[current], plan.changed[current-1] = plan.changed[current-1], plan.changed[current]
		}
	}
}

func planFieldLess(plan *searchPlan, domain Domain, left, right int) bool {
	if plan.changed[left] != plan.changed[right] {
		return plan.changed[left] > plan.changed[right]
	}
	return domain.Fields[plan.fieldRows[left]].Name < domain.Fields[plan.fieldRows[right]].Name
}
