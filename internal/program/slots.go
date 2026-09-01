package program

// Scratch-slot contract:
//
// TruthSlots and ReasonSlots are parallel to the instruction rows and contain
// one-based schema.SlotIDs. Zero means no slot in that class. Peak slot counts
// size evaluator-owned batch scratch; slot allocations never replace the
// InstructionIDs stored in operand and semantic-root columns.
