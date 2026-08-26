// Package benchdata generates deterministic, exact-size benchmark columns.
package benchdata

import (
	"errors"

	"github.com/sebishogun/nornrune/internal/schema"
)

const (
	MaxRows        uint32 = 1 << 20
	MaxPolicyNodes uint32 = 1 << 16
)

var ErrInvalidConfig = errors.New("benchdata: invalid generator configuration")

// Config fixes one deterministic benchmark shape.
type Config struct {
	Seed            uint64
	Rows            uint32
	PolicyNodes     uint32
	EvidencePercent uint32
	MatchPercent    uint32
	TargetSymbol    schema.SymbolID
	OtherSymbol     schema.SymbolID
	TargetValue     schema.ValueID
	OtherValue      schema.ValueID
	EvidenceState   schema.EvidenceStateID
}

// Dataset stores request columns, generated predicate values, and one-edge
// evidence CSR rows in caller-consumable typed slices.
type Dataset struct {
	RequestIDs      []schema.RequestID
	RequestValues   []schema.SymbolID
	PolicyValues    []schema.ValueID
	EvidenceIDs     []schema.EvidenceID
	EvidenceStates  []schema.EvidenceStateID
	EvidenceOffsets []uint32
	EvidenceRefs    []uint32
	MatchRows       uint32
	EvidenceRows    uint32
}

// Generate allocates each output column exactly once and fills it without
// append growth. Percentage counts use floor(rows*percent/100).
func Generate(config Config) (Dataset, error) {
	if config.Rows == 0 || config.Rows > MaxRows || config.PolicyNodes == 0 ||
		config.PolicyNodes > MaxPolicyNodes || config.EvidencePercent > 100 || config.MatchPercent > 100 ||
		config.TargetSymbol == 0 || config.OtherSymbol == 0 || config.TargetSymbol == config.OtherSymbol ||
		config.TargetValue == 0 || config.OtherValue == 0 || config.TargetValue == config.OtherValue ||
		config.EvidenceState == 0 {
		return Dataset{}, ErrInvalidConfig
	}
	matchRows := percentageRows(config.Rows, config.MatchPercent)
	evidenceRows := percentageRows(config.Rows, config.EvidencePercent)
	dataset := Dataset{
		RequestIDs:      make([]schema.RequestID, config.Rows),
		RequestValues:   make([]schema.SymbolID, config.Rows),
		PolicyValues:    make([]schema.ValueID, config.PolicyNodes),
		EvidenceIDs:     make([]schema.EvidenceID, evidenceRows),
		EvidenceStates:  make([]schema.EvidenceStateID, evidenceRows),
		EvidenceOffsets: make([]uint32, config.Rows+1),
		EvidenceRefs:    make([]uint32, evidenceRows),
		MatchRows:       matchRows,
		EvidenceRows:    evidenceRows,
	}
	matchRotation := uint32(config.Seed % uint64(config.Rows))
	evidenceRotation := uint32((config.Seed*0x9e3779b97f4a7c15 + 1) % uint64(config.Rows))
	evidenceRow := uint32(0)
	for row := uint32(0); row < config.Rows; row++ {
		dataset.RequestIDs[row] = schema.RequestID(row + 1)
		dataset.RequestValues[row] = config.OtherSymbol
		if selectedRow(row, config.Rows, matchRows, matchRotation) {
			dataset.RequestValues[row] = config.TargetSymbol
		}
		if selectedRow(row, config.Rows, evidenceRows, evidenceRotation) {
			dataset.EvidenceIDs[evidenceRow] = schema.EvidenceID(evidenceRow + 1)
			dataset.EvidenceStates[evidenceRow] = config.EvidenceState
			dataset.EvidenceRefs[evidenceRow] = evidenceRow
			evidenceRow++
		}
		dataset.EvidenceOffsets[row+1] = evidenceRow
	}
	nodeRotation := uint32(config.Seed & 1)
	for row := uint32(0); row < config.PolicyNodes; row++ {
		dataset.PolicyValues[row] = config.OtherValue
		if (row+nodeRotation)&1 == 0 {
			dataset.PolicyValues[row] = config.TargetValue
		}
	}
	return dataset, nil
}

func percentageRows(rows, percent uint32) uint32 {
	return uint32(uint64(rows) * uint64(percent) / 100)
}

func selectedRow(row, rows, selected, rotation uint32) bool {
	if selected == 0 {
		return false
	}
	position := row + rotation
	if position >= rows {
		position -= rows
	}
	before := uint64(position) * uint64(selected) / uint64(rows)
	after := uint64(position+1) * uint64(selected) / uint64(rows)
	return before != after
}
