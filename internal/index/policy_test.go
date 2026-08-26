package index

import (
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/sebishogun/nornrune/internal/schema"
)

const (
	testActionField   schema.FieldID = 1
	testResourceField schema.FieldID = 2
	testTrustField    schema.FieldID = 3
)

func policyConstraints() Constraints {
	return Constraints{
		Rows:        []uint32{2, 1, 4, 0, 1},
		Fields:      []schema.FieldID{testTrustField, testResourceField, testActionField, testTrustField, testActionField},
		ValueStarts: []uint32{0, 1, 2, 4, 5},
		ValueCounts: []uint32{1, 1, 2, 1, 1},
		Values:      []schema.SymbolID{11, 30, 22, 21, 10, 20},
	}
}

func reversedPolicyConstraints() Constraints {
	return Constraints{
		Rows:        []uint32{1, 0, 4, 1, 2},
		Fields:      []schema.FieldID{testActionField, testTrustField, testActionField, testResourceField, testTrustField},
		ValueStarts: []uint32{0, 1, 2, 4, 5},
		ValueCounts: []uint32{1, 1, 2, 1, 1},
		Values:      []schema.SymbolID{20, 10, 21, 22, 30, 11},
	}
}

func TestPolicyBuildCanonicalMasks(t *testing.T) {
	var builder PolicyBuilder
	var got Policy
	if err := builder.Build(&got, 5, policyConstraints()); err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got.RequirementCount != 5 || got.WordCount != 1 {
		t.Fatalf("counts = requirements %d words %d", got.RequirementCount, got.WordCount)
	}
	if !reflect.DeepEqual(got.FieldIDs, []schema.FieldID{1, 2, 3}) ||
		!reflect.DeepEqual(got.FieldValueStarts, []uint32{0, 3, 4}) ||
		!reflect.DeepEqual(got.FieldValueCounts, []uint32{3, 1, 2}) {
		t.Fatalf("field columns = %v %v %v", got.FieldIDs, got.FieldValueStarts, got.FieldValueCounts)
	}
	if !reflect.DeepEqual(got.Values, []schema.SymbolID{20, 21, 22, 30, 10, 11}) {
		t.Fatalf("values = %v", got.Values)
	}
	if !reflect.DeepEqual(got.AllMask, []uint64{0x1f}) {
		t.Fatalf("all mask = %#x", got.AllMask)
	}
	if !reflect.DeepEqual(got.WildcardMasks, []uint64{0x0d, 0x1d, 0x1a}) {
		t.Fatalf("wildcard masks = %#x", got.WildcardMasks)
	}
	if !reflect.DeepEqual(got.ValueMasks, []uint64{0x0f, 0x1d, 0x1d, 0x1f, 0x1b, 0x1e}) {
		t.Fatalf("value masks = %#x", got.ValueMasks)
	}

	var reordered Policy
	if err := builder.Build(&reordered, 5, reversedPolicyConstraints()); err != nil {
		t.Fatalf("reordered Build: %v", err)
	}
	if !reflect.DeepEqual(reordered, got) {
		t.Fatalf("input order changed Policy:\n got  %+v\n want %+v", reordered, got)
	}
}

func TestPolicyBuildClearsTailBits(t *testing.T) {
	constraints := Constraints{
		Rows:        []uint32{64},
		Fields:      []schema.FieldID{testTrustField},
		ValueStarts: []uint32{0},
		ValueCounts: []uint32{1},
		Values:      []schema.SymbolID{10},
	}
	var builder PolicyBuilder
	var got Policy
	if err := builder.Build(&got, 65, constraints); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.AllMask, []uint64{math.MaxUint64, 1}) ||
		!reflect.DeepEqual(got.WildcardMasks, []uint64{math.MaxUint64, 0}) ||
		!reflect.DeepEqual(got.ValueMasks, []uint64{math.MaxUint64, 1}) {
		t.Fatalf("65-row masks = all %#x wildcard %#x value %#x", got.AllMask, got.WildcardMasks, got.ValueMasks)
	}
}

func TestPolicyBuildCloneOwnsExactStorage(t *testing.T) {
	var builder PolicyBuilder
	var src Policy
	if err := builder.Build(&src, 5, policyConstraints()); err != nil {
		t.Fatal(err)
	}
	clone := src.Clone()
	if !reflect.DeepEqual(clone, src) {
		t.Fatalf("Clone differs: %+v / %+v", clone, src)
	}
	assertPolicyExactAndDistinct(t, &clone, &src)
	if err := builder.Build(&src, 0, Constraints{}); err != nil {
		t.Fatal(err)
	}
	if clone.RequirementCount != 5 || !reflect.DeepEqual(clone.AllMask, []uint64{0x1f}) {
		t.Fatalf("source rebuild changed clone: %+v", clone)
	}
	empty := src.Clone()
	if empty.FieldIDs != nil || empty.AllMask != nil || empty.Values != nil {
		t.Fatalf("empty Clone has nonnil storage: %+v", empty)
	}
}

func assertPolicyExactAndDistinct(t *testing.T, clone, src *Policy) {
	t.Helper()
	cloneValue := reflect.ValueOf(clone).Elem()
	srcValue := reflect.ValueOf(src).Elem()
	for i := 0; i < cloneValue.NumField(); i++ {
		field := cloneValue.Field(i)
		if field.Kind() != reflect.Slice || field.Len() == 0 {
			continue
		}
		if field.Len() != field.Cap() {
			t.Fatalf("%s len/cap = %d/%d", cloneValue.Type().Field(i).Name, field.Len(), field.Cap())
		}
		if field.Pointer() == srcValue.Field(i).Pointer() {
			t.Fatalf("%s borrows source storage", cloneValue.Type().Field(i).Name)
		}
	}
}

func TestPolicyBuildRejectsMalformedAtomically(t *testing.T) {
	var builder PolicyBuilder
	var dst Policy
	if err := builder.Build(&dst, 5, policyConstraints()); err != nil {
		t.Fatal(err)
	}
	want := dst.Clone()
	tests := []struct {
		name        string
		constraints func() Constraints
	}{
		{"misaligned rows", func() Constraints { c := policyConstraints(); c.Rows = c.Rows[:4]; return c }},
		{"bad CSR range", func() Constraints { c := policyConstraints(); c.ValueStarts[0] = math.MaxUint32; return c }},
		{"zero field", func() Constraints { c := policyConstraints(); c.Fields[0] = 0; return c }},
		{"zero value", func() Constraints { c := policyConstraints(); c.Values[0] = 0; return c }},
		{"row out of range", func() Constraints { c := policyConstraints(); c.Rows[0] = 5; return c }},
		{"duplicate field row", func() Constraints {
			return Constraints{
				Rows:        []uint32{1, 1},
				Fields:      []schema.FieldID{testActionField, testActionField},
				ValueStarts: []uint32{0, 1},
				ValueCounts: []uint32{1, 1},
				Values:      []schema.SymbolID{20, 21},
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := builder.Build(&dst, 5, tt.constraints()); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("Build error = %v, want %v", err, ErrInvalidPolicy)
			}
			if !reflect.DeepEqual(dst, want) {
				t.Fatalf("failed Build changed destination: %+v", dst)
			}
		})
	}
	if err := builder.Build(nil, 0, Constraints{}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("nil destination error = %v", err)
	}
	var nilBuilder *PolicyBuilder
	if err := nilBuilder.Build(&dst, 0, Constraints{}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("nil builder error = %v", err)
	}
}

func buildTestPolicy(t *testing.T) Policy {
	t.Helper()
	var builder PolicyBuilder
	var policy Policy
	if err := builder.Build(&policy, 5, policyConstraints()); err != nil {
		t.Fatal(err)
	}
	return policy
}

func TestPolicyCandidatesExactAndConservative(t *testing.T) {
	policy := buildTestPolicy(t)
	tests := []struct {
		name    string
		fields  []schema.FieldID
		values  []schema.SymbolID
		present []uint8
		want    uint64
	}{
		{
			name:    "action resource trust",
			fields:  []schema.FieldID{testActionField, testResourceField, testTrustField},
			values:  []schema.SymbolID{20, 30, 10},
			present: []uint8{1, 1, 1},
			want:    0x0b,
		},
		{"action", []schema.FieldID{testActionField}, []schema.SymbolID{20}, []uint8{1}, 0x0f},
		{"resource", []schema.FieldID{testResourceField}, []schema.SymbolID{30}, []uint8{1}, 0x1f},
		{"trust", []schema.FieldID{testTrustField}, []schema.SymbolID{10}, []uint8{1}, 0x1b},
		{
			name:    "missing trust does not filter",
			fields:  []schema.FieldID{testTrustField, testActionField},
			values:  []schema.SymbolID{10, 20},
			present: []uint8{0, 1},
			want:    0x0f,
		},
		{"present unknown action", []schema.FieldID{testActionField}, []schema.SymbolID{0}, []uint8{1}, 0x0d},
		{"absent action symbol", []schema.FieldID{testActionField}, []schema.SymbolID{99}, []uint8{1}, 0x0d},
		{"unindexed field", []schema.FieldID{4}, []schema.SymbolID{99}, []uint8{1}, 0x1f},
		{
			name:    "selector order independent",
			fields:  []schema.FieldID{testTrustField, testActionField, testResourceField},
			values:  []schema.SymbolID{10, 20, 30},
			present: []uint8{1, 1, 1},
			want:    0x0b,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := []uint64{math.MaxUint64}
			if err := policy.Candidates(dst, tt.fields, tt.values, tt.present); err != nil {
				t.Fatalf("Candidates: %v", err)
			}
			if dst[0] != tt.want {
				t.Fatalf("candidate mask = %#x, want %#x", dst[0], tt.want)
			}
		})
	}
}

func TestPolicyCandidatesEmptyAndTail(t *testing.T) {
	var builder PolicyBuilder
	var empty Policy
	if err := builder.Build(&empty, 0, Constraints{}); err != nil {
		t.Fatal(err)
	}
	if err := empty.Candidates(nil, nil, nil, nil); err != nil {
		t.Fatalf("empty Candidates: %v", err)
	}

	constraints := Constraints{
		Rows:        []uint32{64},
		Fields:      []schema.FieldID{testTrustField},
		ValueStarts: []uint32{0},
		ValueCounts: []uint32{1},
		Values:      []schema.SymbolID{10},
	}
	var tail Policy
	if err := builder.Build(&tail, 65, constraints); err != nil {
		t.Fatal(err)
	}
	dst := []uint64{0, 0}
	if err := tail.Candidates(dst, []schema.FieldID{4}, []schema.SymbolID{99}, []uint8{1}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(dst, []uint64{math.MaxUint64, 1}) {
		t.Fatalf("tail candidates = %#x", dst)
	}
}

func TestPolicyCandidatesRejectsMalformedQueryAtomically(t *testing.T) {
	policy := buildTestPolicy(t)
	tests := []struct {
		name    string
		dst     []uint64
		fields  []schema.FieldID
		values  []schema.SymbolID
		present []uint8
	}{
		{"short destination", nil, nil, nil, nil},
		{"short values", []uint64{0xaa}, []schema.FieldID{1}, nil, []uint8{1}},
		{"short presence", []uint64{0xaa}, []schema.FieldID{1}, []schema.SymbolID{20}, nil},
		{"zero field", []uint64{0xaa}, []schema.FieldID{0}, []schema.SymbolID{20}, []uint8{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := append([]uint64(nil), tt.dst...)
			if err := policy.Candidates(tt.dst, tt.fields, tt.values, tt.present); !errors.Is(err, ErrInvalidQuery) {
				t.Fatalf("Candidates error = %v, want %v", err, ErrInvalidQuery)
			}
			if !reflect.DeepEqual(tt.dst, want) {
				t.Fatalf("failed query changed destination: %#x", tt.dst)
			}
		})
	}
}

func TestPolicyCandidatesWarmAllocations(t *testing.T) {
	policy := buildTestPolicy(t)
	dst := make([]uint64, policy.WordCount)
	fields := []schema.FieldID{testActionField, testResourceField, testTrustField}
	values := []schema.SymbolID{20, 30, 10}
	present := []uint8{1, 1, 1}
	if err := policy.Candidates(dst, fields, values, present); err != nil {
		t.Fatal(err)
	}
	var queryErr error
	allocs := testing.AllocsPerRun(1000, func() {
		queryErr = policy.Candidates(dst, fields, values, present)
	})
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	if allocs != 0 {
		t.Fatalf("Candidates allocations = %g, want 0", allocs)
	}
}

func TestQueryCandidatesExactAndConservative(t *testing.T) {
	policy := buildTestPolicy(t)
	var query Query
	if err := query.Bind(&policy); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	tests := []struct {
		name    string
		values  []schema.SymbolID
		present []uint8
		want    uint64
	}{
		{"all selectors", []schema.SymbolID{20, 30, 10}, []uint8{1, 1, 1}, 0x0b},
		{"missing trust", []schema.SymbolID{20, 30, 10}, []uint8{1, 1, 0}, 0x0f},
		{"unknown action", []schema.SymbolID{99, 0, 0}, []uint8{1, 0, 0}, 0x0d},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := []uint64{math.MaxUint64}
			if err := query.Candidates(dst, tt.values, tt.present); err != nil {
				t.Fatalf("Candidates: %v", err)
			}
			if dst[0] != tt.want {
				t.Fatalf("candidate mask = %#x, want %#x", dst[0], tt.want)
			}
		})
	}
}

func TestQueryCandidatesEmptyAndTail(t *testing.T) {
	var builder PolicyBuilder
	var empty Policy
	if err := builder.Build(&empty, 0, Constraints{}); err != nil {
		t.Fatal(err)
	}
	var query Query
	if err := query.Bind(&empty); err != nil {
		t.Fatalf("empty Bind: %v", err)
	}
	if err := query.Candidates(nil, nil, nil); err != nil {
		t.Fatalf("empty Candidates: %v", err)
	}

	constraints := Constraints{
		Rows:        []uint32{64},
		Fields:      []schema.FieldID{testTrustField},
		ValueStarts: []uint32{0},
		ValueCounts: []uint32{1},
		Values:      []schema.SymbolID{10},
	}
	var tail Policy
	if err := builder.Build(&tail, 65, constraints); err != nil {
		t.Fatal(err)
	}
	if err := query.Bind(&tail); err != nil {
		t.Fatalf("tail Bind: %v", err)
	}
	dst := []uint64{0, 0}
	if err := query.Candidates(dst, []schema.SymbolID{10}, []uint8{1}); err != nil {
		t.Fatalf("tail Candidates: %v", err)
	}
	if !reflect.DeepEqual(dst, []uint64{math.MaxUint64, 1}) {
		t.Fatalf("tail candidates = %#x", dst)
	}
}

func TestQueryBindRejectsMalformedAtomically(t *testing.T) {
	policy := buildTestPolicy(t)
	var query Query
	if err := query.Bind(&policy); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		edit func(*Policy)
	}{
		{"short field starts", func(p *Policy) { p.FieldValueStarts = p.FieldValueStarts[:2] }},
		{"zero field", func(p *Policy) { p.FieldIDs[0] = 0 }},
		{"unsorted fields", func(p *Policy) { p.FieldIDs[1] = p.FieldIDs[0] }},
		{"bad value range", func(p *Policy) { p.FieldValueStarts[0] = math.MaxUint32 }},
		{"zero value", func(p *Policy) { p.Values[0] = 0 }},
		{"unsorted values", func(p *Policy) { p.Values[0], p.Values[1] = p.Values[1], p.Values[0] }},
		{"short wildcard masks", func(p *Policy) { p.WildcardMasks = p.WildcardMasks[:2] }},
		{"short value masks", func(p *Policy) { p.ValueMasks = p.ValueMasks[:5] }},
		{"dirty all tail", func(p *Policy) { p.AllMask[0] = math.MaxUint64 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad := policy.Clone()
			tt.edit(&bad)
			if err := query.Bind(&bad); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("Bind error = %v, want %v", err, ErrInvalidPolicy)
			}
			dst := []uint64{0}
			if err := query.Candidates(dst, []schema.SymbolID{20, 30, 10}, []uint8{1, 1, 1}); err != nil {
				t.Fatalf("prior binding Candidates: %v", err)
			}
			if dst[0] != 0x0b {
				t.Fatalf("prior binding candidates = %#x, want 0x0b", dst[0])
			}
		})
	}
	if err := query.Bind(nil); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("nil Bind error = %v, want %v", err, ErrInvalidPolicy)
	}
}

func TestQueryCandidatesRejectsMalformedAtomically(t *testing.T) {
	policy := buildTestPolicy(t)
	var query Query
	if err := query.Bind(&policy); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		dst     []uint64
		values  []schema.SymbolID
		present []uint8
	}{
		{"short destination", nil, []schema.SymbolID{20, 30, 10}, []uint8{1, 1, 1}},
		{"short values", []uint64{0xaa}, []schema.SymbolID{20, 30}, []uint8{1, 1, 1}},
		{"short presence", []uint64{0xaa}, []schema.SymbolID{20, 30, 10}, []uint8{1, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := append([]uint64(nil), tt.dst...)
			if err := query.Candidates(tt.dst, tt.values, tt.present); !errors.Is(err, ErrInvalidQuery) {
				t.Fatalf("Candidates error = %v, want %v", err, ErrInvalidQuery)
			}
			if !reflect.DeepEqual(tt.dst, want) {
				t.Fatalf("failed Candidates changed destination: %#x", tt.dst)
			}
		})
	}
	var unbound Query
	if err := unbound.Candidates(nil, nil, nil); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("unbound Candidates error = %v, want %v", err, ErrInvalidQuery)
	}
}

func TestQueryCandidatesWarmAllocations(t *testing.T) {
	policy := buildTestPolicy(t)
	var query Query
	if err := query.Bind(&policy); err != nil {
		t.Fatal(err)
	}
	dst := make([]uint64, policy.WordCount)
	values := []schema.SymbolID{20, 30, 10}
	present := []uint8{1, 1, 1}
	if err := query.Candidates(dst, values, present); err != nil {
		t.Fatal(err)
	}
	var queryErr error
	allocs := testing.AllocsPerRun(1000, func() {
		queryErr = query.Candidates(dst, values, present)
	})
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	if allocs != 0 {
		t.Fatalf("bound Candidates allocations = %g, want 0", allocs)
	}
}

func TestQuerySamePolicyBindIsNoOp(t *testing.T) {
	policy := buildTestPolicy(t)
	var query Query
	if err := query.Bind(&policy); err != nil {
		t.Fatal(err)
	}
	policy.AllMask[0] = math.MaxUint64
	if err := query.Bind(&policy); err != nil {
		t.Fatalf("same immutable Policy Bind rescanned columns: %v", err)
	}
}
