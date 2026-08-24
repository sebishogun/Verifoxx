package benchdata

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sebishogun/verifoxx/internal/schema"
)

func TestGenerateBuildsExactDeterministicShape(t *testing.T) {
	config := Config{
		Rows: 10, PolicyNodes: 7, EvidencePercent: 40, MatchPercent: 30, Seed: 3,
		TargetSymbol: 11, OtherSymbol: 12, TargetValue: 21, OtherValue: 22, EvidenceState: 1,
	}
	first, err := Generate(config)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	second, err := Generate(config)
	if err != nil {
		t.Fatalf("Generate() second error = %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("Generate() is not deterministic")
	}
	if len(first.RequestIDs) != 10 || len(first.RequestValues) != 10 || len(first.PolicyValues) != 7 ||
		len(first.EvidenceIDs) != 4 || len(first.EvidenceStates) != 4 || len(first.EvidenceOffsets) != 11 ||
		len(first.EvidenceRefs) != 4 {
		t.Fatalf("Generate() lengths = requests %d/%d, nodes %d, evidence %d/%d, CSR %d/%d",
			len(first.RequestIDs), len(first.RequestValues), len(first.PolicyValues),
			len(first.EvidenceIDs), len(first.EvidenceStates), len(first.EvidenceOffsets), len(first.EvidenceRefs))
	}
	if first.MatchRows != 3 || first.EvidenceRows != 4 {
		t.Fatalf("Generate() counts = match %d evidence %d", first.MatchRows, first.EvidenceRows)
	}
	matchRows := 0
	for row, id := range first.RequestIDs {
		if id != schema.RequestID(row+1) {
			t.Fatalf("RequestIDs[%d] = %d", row, id)
		}
		if first.RequestValues[row] == config.TargetSymbol {
			matchRows++
		} else if first.RequestValues[row] != config.OtherSymbol {
			t.Fatalf("RequestValues[%d] = %d", row, first.RequestValues[row])
		}
		if first.EvidenceOffsets[row] > first.EvidenceOffsets[row+1] {
			t.Fatalf("EvidenceOffsets are not monotonic at row %d", row)
		}
	}
	if matchRows != int(first.MatchRows) || first.EvidenceOffsets[10] != uint32(len(first.EvidenceRefs)) {
		t.Fatalf("materialized counts = match %d evidence %d", matchRows, first.EvidenceOffsets[10])
	}
	for row, ref := range first.EvidenceRefs {
		if ref != uint32(row) || first.EvidenceIDs[row] != schema.EvidenceID(row+1) ||
			first.EvidenceStates[row] != config.EvidenceState {
			t.Fatalf("evidence row %d = ref %d id %d state %d", row, ref, first.EvidenceIDs[row], first.EvidenceStates[row])
		}
	}
	for row, value := range first.PolicyValues {
		if value != config.TargetValue && value != config.OtherValue {
			t.Fatalf("PolicyValues[%d] = %d", row, value)
		}
	}

	rotated := config
	rotated.Seed++
	third, err := Generate(rotated)
	if err != nil {
		t.Fatalf("Generate(rotated) error = %v", err)
	}
	if reflect.DeepEqual(first.RequestValues, third.RequestValues) ||
		reflect.DeepEqual(first.EvidenceOffsets, third.EvidenceOffsets) {
		t.Fatal("different seeds did not rotate generated rows")
	}
	first.RequestValues[0] = 99
	if second.RequestValues[0] == 99 {
		t.Fatal("separate Generate calls alias storage")
	}
}

func TestGenerateRejectsInvalidConfig(t *testing.T) {
	valid := Config{
		Rows: 1, PolicyNodes: 1, EvidencePercent: 50, MatchPercent: 50,
		TargetSymbol: 1, OtherSymbol: 2, TargetValue: 1, OtherValue: 2, EvidenceState: 1,
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"zero rows", func(config *Config) { config.Rows = 0 }},
		{"too many rows", func(config *Config) { config.Rows = MaxRows + 1 }},
		{"zero nodes", func(config *Config) { config.PolicyNodes = 0 }},
		{"too many nodes", func(config *Config) { config.PolicyNodes = MaxPolicyNodes + 1 }},
		{"evidence percent", func(config *Config) { config.EvidencePercent = 101 }},
		{"match percent", func(config *Config) { config.MatchPercent = 101 }},
		{"zero target symbol", func(config *Config) { config.TargetSymbol = 0 }},
		{"duplicate symbols", func(config *Config) { config.OtherSymbol = config.TargetSymbol }},
		{"zero target value", func(config *Config) { config.TargetValue = 0 }},
		{"duplicate values", func(config *Config) { config.OtherValue = config.TargetValue }},
		{"zero evidence state", func(config *Config) { config.EvidenceState = 0 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if dataset, err := Generate(config); !errors.Is(err, ErrInvalidConfig) || !reflect.DeepEqual(dataset, Dataset{}) {
				t.Fatalf("Generate() = (%+v, %v), want zero and %v", dataset, err, ErrInvalidConfig)
			}
		})
	}
}

func TestPercentageRowsAvoidsNarrowMultiplicationOverflow(t *testing.T) {
	if got := percentageRows(MaxRows, 100); got != MaxRows {
		t.Fatalf("percentageRows(MaxRows, 100) = %d, want %d", got, MaxRows)
	}
}
