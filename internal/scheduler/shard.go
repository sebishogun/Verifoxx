package scheduler

import (
	"errors"
	"math"

	"github.com/sebishogun/nornrune/internal/result"
)

var errInvalidShardResult = errors.New("scheduler: invalid shard result")

type rowRange struct {
	start uint32
	end   uint32
}

func partitionRows(dst []rowRange, rows uint32, shards int) []rowRange {
	if len(dst) == 0 {
		return dst[:0]
	}
	count := shards
	if count < 1 {
		count = 1
	}
	if count > len(dst) {
		count = len(dst)
	}
	words := (uint64(rows) + 63) >> 6
	if words == 0 {
		count = 1
	} else if uint64(count) > words {
		count = int(words)
	}

	ranges := dst[:count]
	wordsPerShard := words / uint64(count)
	extraWords := words % uint64(count)
	var wordStart uint64
	for shard := range ranges {
		wordCount := wordsPerShard
		if uint64(shard) < extraWords {
			wordCount++
		}
		start := wordStart << 6
		wordStart += wordCount
		end := wordStart << 6
		if end > uint64(rows) {
			end = uint64(rows)
		}
		ranges[shard] = rowRange{start: uint32(start), end: uint32(end)}
	}
	return ranges
}

type mergeTotals struct {
	requirements uint64
	drivers      uint64
	evidence     uint64
	reasons      uint64
	remediations uint64
}

func mergeResults(dst *result.Batch, shards []result.Batch, ranges []rowRange, rows uint32) error {
	if dst == nil || len(shards) == 0 || len(shards) != len(ranges) {
		return errInvalidShardResult
	}

	var totals mergeTotals
	var covered uint32
	for shardIndex := range shards {
		rangeRows := ranges[shardIndex]
		shard := &shards[shardIndex]
		if rangeRows.start != covered || rangeRows.end < rangeRows.start || rangeRows.end > rows ||
			uint64(rangeRows.end)-uint64(rangeRows.start) != uint64(shard.Rows) ||
			uint64(len(shard.OutcomeIDs)) != uint64(shard.Rows) {
			return errInvalidShardResult
		}

		requirements, ok := validShardCSR(shard.RequirementOffsets, len(shard.RequirementIDs), shard.Rows)
		if !ok {
			return errInvalidShardResult
		}
		drivers, ok := validShardCSR(shard.DriverOffsets, len(shard.DriverRequirements), shard.Rows)
		if !ok || uint64(len(shard.DriverClauses)) != uint64(drivers) ||
			uint64(len(shard.DriverNodes)) != uint64(drivers) ||
			uint64(len(shard.DriverReasons)) != uint64(drivers) ||
			uint64(len(shard.DriverExplanations)) != uint64(drivers) {
			return errInvalidShardResult
		}
		evidence, ok := validShardCSR(shard.EvidenceOffsets, len(shard.EvidenceIDs), shard.Rows)
		if !ok {
			return errInvalidShardResult
		}
		reasons, ok := validShardCSR(shard.ReasonOffsets, len(shard.ReasonIDs), shard.Rows)
		if !ok || uint64(len(shard.ReasonNodes)) != uint64(reasons) ||
			uint64(len(shard.ReasonEvidenceIDs)) != uint64(reasons) ||
			uint64(len(shard.ReasonEvidenceStates)) != uint64(reasons) {
			return errInvalidShardResult
		}
		remediations, ok := validShardCSR(shard.RemediationOffsets, len(shard.RemediationIDs), shard.Rows)
		if !ok {
			return errInvalidShardResult
		}

		if totals.requirements, ok = addMergeEdges(totals.requirements, requirements); !ok {
			return errInvalidShardResult
		}
		if totals.drivers, ok = addMergeEdges(totals.drivers, drivers); !ok {
			return errInvalidShardResult
		}
		if totals.evidence, ok = addMergeEdges(totals.evidence, evidence); !ok {
			return errInvalidShardResult
		}
		if totals.reasons, ok = addMergeEdges(totals.reasons, reasons); !ok {
			return errInvalidShardResult
		}
		if totals.remediations, ok = addMergeEdges(totals.remediations, remediations); !ok {
			return errInvalidShardResult
		}
		covered = rangeRows.end
	}
	if covered != rows {
		return errInvalidShardResult
	}

	if err := dst.Reset(rows); err != nil {
		return err
	}
	dst.RequirementIDs = growExact(dst.RequirementIDs, int(totals.requirements))
	dst.DriverRequirements = growExact(dst.DriverRequirements, int(totals.drivers))
	dst.DriverClauses = growExact(dst.DriverClauses, int(totals.drivers))
	dst.DriverNodes = growExact(dst.DriverNodes, int(totals.drivers))
	dst.DriverReasons = growExact(dst.DriverReasons, int(totals.drivers))
	dst.DriverExplanations = growExact(dst.DriverExplanations, int(totals.drivers))
	dst.EvidenceIDs = growExact(dst.EvidenceIDs, int(totals.evidence))
	dst.ReasonIDs = growExact(dst.ReasonIDs, int(totals.reasons))
	dst.ReasonNodes = growExact(dst.ReasonNodes, int(totals.reasons))
	dst.ReasonEvidenceIDs = growExact(dst.ReasonEvidenceIDs, int(totals.reasons))
	dst.ReasonEvidenceStates = growExact(dst.ReasonEvidenceStates, int(totals.reasons))
	dst.RemediationIDs = growExact(dst.RemediationIDs, int(totals.remediations))

	for shardIndex := range shards {
		shard := &shards[shardIndex]
		rangeRows := ranges[shardIndex]
		rowStart := int(rangeRows.start)
		rowEnd := int(rangeRows.end)
		copy(dst.OutcomeIDs[rowStart:rowEnd], shard.OutcomeIDs)

		rebaseShardOffsets(dst.RequirementOffsets, rowStart, shard.RequirementOffsets, uint32(len(dst.RequirementIDs)))
		dst.RequirementIDs = append(dst.RequirementIDs, shard.RequirementIDs...)
		rebaseShardOffsets(dst.DriverOffsets, rowStart, shard.DriverOffsets, uint32(len(dst.DriverRequirements)))
		dst.DriverRequirements = append(dst.DriverRequirements, shard.DriverRequirements...)
		dst.DriverClauses = append(dst.DriverClauses, shard.DriverClauses...)
		dst.DriverNodes = append(dst.DriverNodes, shard.DriverNodes...)
		dst.DriverReasons = append(dst.DriverReasons, shard.DriverReasons...)
		dst.DriverExplanations = append(dst.DriverExplanations, shard.DriverExplanations...)
		rebaseShardOffsets(dst.EvidenceOffsets, rowStart, shard.EvidenceOffsets, uint32(len(dst.EvidenceIDs)))
		dst.EvidenceIDs = append(dst.EvidenceIDs, shard.EvidenceIDs...)
		rebaseShardOffsets(dst.ReasonOffsets, rowStart, shard.ReasonOffsets, uint32(len(dst.ReasonIDs)))
		dst.ReasonIDs = append(dst.ReasonIDs, shard.ReasonIDs...)
		dst.ReasonNodes = append(dst.ReasonNodes, shard.ReasonNodes...)
		dst.ReasonEvidenceIDs = append(dst.ReasonEvidenceIDs, shard.ReasonEvidenceIDs...)
		dst.ReasonEvidenceStates = append(dst.ReasonEvidenceStates, shard.ReasonEvidenceStates...)
		rebaseShardOffsets(dst.RemediationOffsets, rowStart, shard.RemediationOffsets, uint32(len(dst.RemediationIDs)))
		dst.RemediationIDs = append(dst.RemediationIDs, shard.RemediationIDs...)
	}
	return nil
}

func validShardCSR(offsets []uint32, edgeCount int, rows uint32) (uint32, bool) {
	offsetCount := uint64(rows) + 1
	if offsetCount > uint64(^uint(0)>>1) || uint64(len(offsets)) != offsetCount || len(offsets) == 0 || offsets[0] != 0 {
		return 0, false
	}
	previous := uint32(0)
	for _, offset := range offsets[1:] {
		if offset < previous {
			return 0, false
		}
		previous = offset
	}
	return previous, uint64(previous) == uint64(edgeCount)
}

func rebaseShardOffsets(dst []uint32, rowStart int, source []uint32, base uint32) {
	for row, offset := range source {
		dst[rowStart+row] = base + offset
	}
}

func addMergeEdges(total uint64, count uint32) (uint64, bool) {
	limit := mergeEdgeLimit()
	if total > limit || uint64(count) > limit-total {
		return 0, false
	}
	return total + uint64(count), true
}

func mergeEdgeLimit() uint64 {
	limit := uint64(^uint(0) >> 1)
	if limit > math.MaxUint32 {
		return math.MaxUint32
	}
	return limit
}
