package truth

import "github.com/sebishogun/nornrune/internal/schema"

// ReasonCount is the number of engine uncertainty reasons, one per bit of a
// ReasonMask.
const ReasonCount = 9

// Reason IDs are fixed and one-based. ReasonInvalid means the evidence itself
// is invalid, not the zero invalid ID.
const (
	ReasonMissing schema.ReasonID = iota + 1
	ReasonStale
	ReasonUnclear
	ReasonUnverifiable
	ReasonWrongScope
	ReasonWrongSubject
	ReasonWrongTiming
	ReasonInvalid
	ReasonConflict
)

// ReasonMask is a bitset of uncertainty reasons; bit reason-1 corresponds to
// ReasonID reason.
type ReasonMask uint16

// AllReasonsMask has every reason bit set.
const AllReasonsMask ReasonMask = 1<<ReasonCount - 1

// ReasonBit returns the one-hot mask bit for reason, or zero for the invalid
// ID zero and for IDs above ReasonConflict.
func ReasonBit(reason schema.ReasonID) ReasonMask {
	if reason < ReasonMissing || reason > ReasonConflict {
		return 0
	}
	return 1 << (reason - 1)
}

// With returns mask with reason's bit set; invalid reasons change nothing.
func (mask ReasonMask) With(reason schema.ReasonID) ReasonMask {
	return mask | ReasonBit(reason)
}

// Has reports whether reason's bit is set. Invalid reasons are never present.
func (mask ReasonMask) Has(reason schema.ReasonID) bool {
	bit := ReasonBit(reason)
	return bit != 0 && mask&bit != 0
}

// Valid reports whether every set bit is a defined reason bit.
func (mask ReasonMask) Valid() bool {
	return mask&^AllReasonsMask == 0
}
