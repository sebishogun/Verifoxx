package debug

import "slices"

// checkpointSet stores only deterministic instruction boundaries. Retain-all
// scratch preserves operand state, so replay does not duplicate mask slabs.
type checkpointSet struct {
	cursors  []uint32
	interval uint32
	maximum  int
}

func newCheckpointSet(interval uint32, maximum int) checkpointSet {
	return checkpointSet{cursors: make([]uint32, 0, maximum), interval: interval, maximum: maximum}
}

func (checkpoints *checkpointSet) reset() {
	checkpoints.cursors = checkpoints.cursors[:0]
}

func (checkpoints *checkpointSet) record(cursor, instructionCount uint32) {
	if checkpoints == nil || cursor == 0 ||
		(cursor != instructionCount && cursor%checkpoints.interval != 0) {
		return
	}
	if index, found := slices.BinarySearch(checkpoints.cursors, cursor); found {
		return
	} else if len(checkpoints.cursors) < checkpoints.maximum {
		checkpoints.cursors = append(checkpoints.cursors, 0)
		copy(checkpoints.cursors[index+1:], checkpoints.cursors[index:])
		checkpoints.cursors[index] = cursor
		return
	}
	copy(checkpoints.cursors, checkpoints.cursors[1:])
	checkpoints.cursors[len(checkpoints.cursors)-1] = cursor
}

func (checkpoints *checkpointSet) nearest(target uint32) uint32 {
	if checkpoints == nil || len(checkpoints.cursors) == 0 {
		return 0
	}
	index, found := slices.BinarySearch(checkpoints.cursors, target)
	if found {
		return target
	}
	if index == 0 {
		return 0
	}
	return checkpoints.cursors[index-1]
}

func (checkpoints *checkpointSet) truncate(target uint32) {
	if checkpoints == nil {
		return
	}
	index, found := slices.BinarySearch(checkpoints.cursors, target)
	if found {
		index++
	}
	checkpoints.cursors = checkpoints.cursors[:index]
}

func (checkpoints *checkpointSet) count() uint32 {
	if checkpoints == nil {
		return 0
	}
	return uint32(len(checkpoints.cursors))
}
