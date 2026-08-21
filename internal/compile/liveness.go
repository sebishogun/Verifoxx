package compile

import (
	"math"
	"math/bits"

	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
)

const knownRootFlags = program.RootApplicability | program.RootAssertion | program.RootEvidence

type slotMode uint8

const (
	slotReuse slotMode = iota
	slotRetainAll
)

// validateSlotProgram checks the generated instruction graph before liveness
// scans slice its CSR edges. Slot planning requires final topological order, so
// every operand ID must be strictly less than its one-based consumer ID.
func validateSlotProgram(p *program.Program) error {
	if p == nil {
		return ErrInvalidGeneratedProgram
	}
	if uint64(len(p.Opcodes)) > math.MaxUint32 {
		return ErrProgramTooLarge
	}
	if err := validateScheduleColumns(p); err != nil {
		return err
	}
	for row, flags := range p.RootFlags {
		if flags&^knownRootFlags != 0 {
			return ErrInvalidGeneratedProgram
		}
		start := uint64(p.OperandStarts[row])
		end := start + uint64(p.OperandCounts[row])
		if end > uint64(len(p.Operands)) {
			return ErrInvalidGeneratedProgram
		}
		consumerID := uint64(row) + 1
		for _, operand := range p.Operands[int(start):int(end)] {
			if operand == 0 || uint64(operand) >= consumerID {
				return ErrInvalidGeneratedProgram
			}
		}
	}
	return nil
}

// computeLastUses returns zero-based final-consumer rows for every instruction.
// Semantic roots use the one-past-the-end sentinel and therefore remain live
// until evaluation has consumed all roots.
func (l *Lowerer) computeLastUses(p *program.Program) ([]uint32, error) {
	if err := validateSlotProgram(p); err != nil {
		return nil, err
	}
	n := len(p.Opcodes)
	l.slotLastUses = resizeSlots(l.slotLastUses, n)
	for row := range n {
		l.slotLastUses[row] = uint32(row)
	}
	for consumer := range n {
		start := int(p.OperandStarts[consumer])
		end := start + int(p.OperandCounts[consumer])
		for _, operand := range p.Operands[start:end] {
			l.slotLastUses[int(operand)-1] = uint32(consumer)
		}
	}
	for row, flags := range p.RootFlags {
		if flags != 0 {
			l.slotLastUses[row] = uint32(n)
		}
	}
	return l.slotLastUses, nil
}

func slotWordCount(n int) int {
	words := n / 64
	if n%64 != 0 {
		words++
	}
	return words
}

func markSlotFree(words []uint64, slot schema.SlotID, firstFreeWord *int) error {
	if slot == 0 {
		return ErrInvalidGeneratedProgram
	}
	bitIndex := uint32(slot - 1)
	word := int(bitIndex >> 6)
	if word >= len(words) {
		return ErrInvalidGeneratedProgram
	}
	mask := uint64(1) << (bitIndex & 63)
	if words[word]&mask != 0 {
		return ErrInvalidGeneratedProgram
	}
	words[word] |= mask
	if word < *firstFreeWord {
		*firstFreeWord = word
	}
	return nil
}

func takeLowestFreeSlot(words []uint64, firstFreeWord *int) (schema.SlotID, bool) {
	word := *firstFreeWord
	for word < len(words) && words[word] == 0 {
		word++
	}
	if word == len(words) {
		*firstFreeWord = word
		return 0, false
	}
	bit := uint32(bits.TrailingZeros64(words[word]))
	words[word] &^= uint64(1) << bit
	if words[word] == 0 {
		*firstFreeWord = word + 1
	} else {
		*firstFreeWord = word
	}
	return schema.SlotID(uint32(word)*64 + bit + 1), true
}

func (l *Lowerer) allocateSlots(lastUses []uint32, live []uint8, dst []schema.SlotID, mode slotMode) (uint32, error) {
	n := len(lastUses)
	if len(dst) != n || (live != nil && len(live) != n) || (mode != slotReuse && mode != slotRetainAll) {
		return 0, ErrInvalidGeneratedProgram
	}
	if mode == slotRetainAll {
		var peak uint32
		for row := range n {
			if live != nil && live[row] == 0 {
				continue
			}
			if peak == math.MaxUint32 {
				return 0, ErrProgramTooLarge
			}
			peak++
			dst[row] = schema.SlotID(peak)
		}
		return peak, nil
	}

	l.slotReleaseHead = resizeSlots(l.slotReleaseHead, n)
	l.slotReleaseNext = resizeSlots(l.slotReleaseNext, n)
	for row, lastUse := range lastUses {
		if live != nil && live[row] == 0 {
			continue
		}
		if lastUse < uint32(row) || lastUse > uint32(n) {
			return 0, ErrInvalidGeneratedProgram
		}
		if lastUse > uint32(row) && lastUse < uint32(n) {
			l.slotReleaseNext[row] = l.slotReleaseHead[lastUse]
			l.slotReleaseHead[lastUse] = schema.InstructionID(row + 1)
		}
	}

	l.slotFreeWords = resizeSlots(l.slotFreeWords, slotWordCount(n))
	firstFreeWord := len(l.slotFreeWords)
	var peak uint32
	for row := range n {
		for id := l.slotReleaseHead[row]; id != 0; id = l.slotReleaseNext[id-1] {
			if uint64(id) > uint64(n) || (live != nil && live[id-1] == 0) {
				return 0, ErrInvalidGeneratedProgram
			}
			if err := markSlotFree(l.slotFreeWords, dst[id-1], &firstFreeWord); err != nil {
				return 0, err
			}
		}
		if live != nil && live[row] == 0 {
			continue
		}
		slot, ok := takeLowestFreeSlot(l.slotFreeWords, &firstFreeWord)
		if !ok {
			if peak == math.MaxUint32 {
				return 0, ErrProgramTooLarge
			}
			peak++
			slot = schema.SlotID(peak)
		}
		dst[row] = slot
		if lastUses[row] == uint32(row) {
			if err := markSlotFree(l.slotFreeWords, slot, &firstFreeWord); err != nil {
				return 0, err
			}
		}
	}
	return peak, nil
}

func (l *Lowerer) assignTruthSlots(p *program.Program, mode slotMode) (uint32, error) {
	lastUses, err := l.computeLastUses(p)
	if err != nil {
		return 0, err
	}
	l.slotTruth = resizeSlots(l.slotTruth, len(lastUses))
	return l.allocateSlots(lastUses, nil, l.slotTruth, mode)
}

func (l *Lowerer) computeReasonLive(p *program.Program) {
	n := len(p.Opcodes)
	l.slotReasonLive = resizeSlots(l.slotReasonLive, n)
	for row, flags := range p.RootFlags {
		if flags != 0 {
			l.slotReasonLive[row] = 1
		}
	}
	for row := n; row > 0; {
		row--
		if l.slotReasonLive[row] == 0 {
			continue
		}
		switch p.Opcodes[row] {
		case program.OpcodeAll, program.OpcodeAny, program.OpcodeNot:
			start := int(p.OperandStarts[row])
			end := start + int(p.OperandCounts[row])
			for _, operand := range p.Operands[start:end] {
				l.slotReasonLive[int(operand)-1] = 1
			}
		}
	}
}

// assignSlots computes both independent scratch plans into Lowerer-owned
// buffers. Program columns are replaced only after both allocations succeed.
func (l *Lowerer) assignSlots(p *program.Program, mode slotMode) error {
	truthPeak, err := l.assignTruthSlots(p, mode)
	if err != nil {
		return err
	}
	l.computeReasonLive(p)
	l.slotReasons = resizeSlots(l.slotReasons, len(l.slotLastUses))
	reasonPeak, err := l.allocateSlots(l.slotLastUses, l.slotReasonLive, l.slotReasons, mode)
	if err != nil {
		return err
	}
	p.TruthSlots = l.slotTruth
	p.ReasonSlots = l.slotReasons
	p.TruthSlotCount = truthPeak
	p.ReasonSlotCount = reasonPeak
	return nil
}
