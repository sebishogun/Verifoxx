package compile

import (
	"errors"
	"reflect"
	"testing"

	"github.com/sebishogun/verifoxx/internal/ast"
	"github.com/sebishogun/verifoxx/internal/program"
	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

func TestLowerAPI(t *testing.T) {
	doc, fields, syms := lowerFixture(t)
	got, err := Lower(doc, fields, syms)
	if err != nil {
		t.Fatalf("Lower: %v", err)
	}
	if got == nil || got.InstructionCount() == 0 {
		t.Fatal("Lower returned an empty Program")
	}
	assertProgramSlots(t, got)
	var lowerer Lowerer
	var dst program.Program
	if err := lowerer.Lower(&dst, doc, fields, syms); err != nil {
		t.Fatalf("Lowerer.Lower: %v", err)
	}
	if !reflect.DeepEqual(got, &dst) {
		t.Fatal("convenience and reusable lowering differ")
	}
	assertExactProgramSlices(t, reflect.ValueOf(&dst).Elem(), "Program")
	resolver := got.ResultResolver()
	resolution, ok := resolver.Resolve(1, truth.ReasonBit(truth.ReasonMissing))
	if !ok || resolution.Outcome != 4 || !resolution.Terminal {
		t.Fatalf("Missing resolution = %+v, %v", resolution, ok)
	}
	if _, ok := got.LookupSymbol([]byte("not-a-program-symbol")); ok {
		t.Fatal("frozen lookup admitted an unknown symbol")
	}
	for id := schema.SymbolID(1); id <= schema.SymbolID(got.ProgramSymbolCount); id++ {
		bytes, ok := got.Symbol(id)
		if !ok {
			t.Fatalf("frozen SymbolID %d is missing", id)
		}
		found, ok := got.LookupSymbol(bytes)
		if !ok || found != id || found > schema.SymbolID(got.ProgramSymbolCount) {
			t.Fatalf("LookupSymbol(%q) = %d, %v", bytes, found, ok)
		}
	}
	if _, ok := got.Symbol(schema.SymbolID(got.ProgramSymbolCount + 1)); ok {
		t.Fatal("ProgramSymbolCount+1 resolved from the frozen symbol slab")
	}
	assertExactProgramSlices(t, reflect.ValueOf(got).Elem(), "Program")
}

func TestLowerErrorsAreAtomic(t *testing.T) {
	doc, fields, syms := lowerFixture(t)
	bad := *doc
	bad.NodeRefs = nil
	emptyFields := schema.NewBuilder().Finish()
	emptySyms := schema.NewSymbolInterner(0)

	var lowerer Lowerer
	if err := lowerer.Lower(nil, doc, fields, syms); !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("nil destination error = %v", err)
	}
	if got, err := Lower(nil, fields, syms); got != nil || !errors.Is(err, ErrInvalidDocument) {
		t.Fatalf("convenience nil document = %v, %v", got, err)
	}

	tests := []struct {
		name   string
		doc    *ast.Document
		fields *schema.Schema
		syms   *schema.Interner
		want   error
	}{
		{"nil document", nil, fields, syms, ErrInvalidDocument},
		{"nil schema", doc, nil, syms, ErrInvalidDocument},
		{"nil interner", doc, fields, nil, ErrInvalidDocument},
		{"validator diagnostic", &bad, fields, syms, ErrInvalidDocument},
		{"empty policy", &ast.Document{}, emptyFields, emptySyms, ErrEmptyPolicy},
		{"missing field symbols", doc, fields, emptySyms, ErrInvalidSymbols},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dst := program.Program{
				Opcodes:            []program.Opcode{program.OpcodeEqual},
				InputBytes:         []byte("unchanged"),
				ProgramSymbolCount: 77,
			}
			before := snapshotExported(reflect.ValueOf(dst))
			err := lowerer.Lower(&dst, tc.doc, tc.fields, tc.syms)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Lowerer.Lower error = %v, want %v", err, tc.want)
			}
			after := snapshotExported(reflect.ValueOf(dst))
			if !reflect.DeepEqual(after, before) {
				t.Fatal("destination changed after failed lowering")
			}
		})
	}
}

func TestLowerSlotPlanningErrorIsAtomic(t *testing.T) {
	p := slotTestProgram(
		[]program.Opcode{program.OpcodeInvalid},
		[][]schema.InstructionID{nil},
		[]program.RootFlags{program.RootAssertion},
	)
	p.TruthSlots = []schema.SlotID{7}
	p.ReasonSlots = []schema.SlotID{8}
	p.TruthSlotCount = 7
	p.ReasonSlotCount = 8
	wantTruth := append([]schema.SlotID(nil), p.TruthSlots...)
	wantReasons := append([]schema.SlotID(nil), p.ReasonSlots...)
	var lowerer Lowerer
	if err := lowerer.assignSlots(&p, slotReuse); !errors.Is(err, ErrInvalidGeneratedProgram) {
		t.Fatalf("assignSlots error = %v, want %v", err, ErrInvalidGeneratedProgram)
	}
	if !reflect.DeepEqual(p.TruthSlots, wantTruth) || !reflect.DeepEqual(p.ReasonSlots, wantReasons) ||
		p.TruthSlotCount != 7 || p.ReasonSlotCount != 8 {
		t.Fatalf("failed slot planning published %v/%v counts %d/%d",
			p.TruthSlots, p.ReasonSlots, p.TruthSlotCount, p.ReasonSlotCount)
	}
}

func TestLowerOwnership(t *testing.T) {
	doc, fields, syms := lowerFixture(t)
	var lowerer Lowerer
	var got program.Program
	if err := lowerer.Lower(&got, doc, fields, syms); err != nil {
		t.Fatalf("Lowerer.Lower: %v", err)
	}
	want := snapshotExported(reflect.ValueOf(got))
	wantInput := append([]byte(nil), got.InputBytes...)
	wantName, ok := got.Symbol(got.PolicyName)
	if !ok {
		t.Fatal("PolicyName did not resolve")
	}
	wantName = append([]byte(nil), wantName...)
	resolver := got.ResultResolver()
	wantResolution, ok := resolver.Resolve(1, truth.ReasonBit(truth.ReasonMissing))
	if !ok {
		t.Fatal("Missing did not resolve before source mutation")
	}

	otherFixture := buildNormalizeFixture(t)
	var other program.Program
	if err := lowerer.Lower(&other, otherFixture.doc, otherFixture.fields, otherFixture.syms); err != nil {
		t.Fatalf("interleaved Lowerer.Lower: %v", err)
	}
	if after := snapshotExported(reflect.ValueOf(got)); !reflect.DeepEqual(after, want) {
		t.Fatal("a later Lowerer call changed an earlier Program")
	}

	zeroDocumentSlices(doc)
	syms.Reset()
	if _, err := syms.Intern([]byte("replacement")); err != nil {
		t.Fatal(err)
	}
	if after := snapshotExported(reflect.ValueOf(got)); !reflect.DeepEqual(after, want) {
		t.Fatal("source mutation changed the frozen Program")
	}
	if !reflect.DeepEqual(got.InputBytes, wantInput) {
		t.Fatalf("InputBytes = %q, want %q", got.InputBytes, wantInput)
	}
	name, ok := got.Symbol(got.PolicyName)
	if !ok || !reflect.DeepEqual(name, wantName) {
		t.Fatalf("PolicyName bytes = %q, %v; want %q", name, ok, wantName)
	}
	afterResolver := got.ResultResolver()
	afterResolution, ok := afterResolver.Resolve(1, truth.ReasonBit(truth.ReasonMissing))
	if !ok || !reflect.DeepEqual(afterResolution, wantResolution) {
		t.Fatalf("resolution changed to %+v, %v; want %+v", afterResolution, ok, wantResolution)
	}
}

func TestLowerDeterministic(t *testing.T) {
	doc, fields, syms := lowerFixture(t)
	coldA, err := Lower(doc, fields, syms)
	if err != nil {
		t.Fatal(err)
	}
	coldB, err := Lower(doc, fields, syms)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(coldA, coldB) {
		t.Fatal("cold Lower calls differ")
	}
	var lowerer Lowerer
	var warm program.Program
	for i := 0; i < 4; i++ {
		if err := lowerer.Lower(&warm, doc, fields, syms); err != nil {
			t.Fatalf("warm call %d: %v", i, err)
		}
		if !reflect.DeepEqual(&warm, coldA) {
			t.Fatalf("warm call %d differs from cold output", i)
		}
	}
}

func TestProgramPointerlessColumns(t *testing.T) {
	assertPointerlessColumns(t, reflect.TypeOf(program.Program{}), "Program")
}

func assertProgramSlots(t *testing.T, p *program.Program) {
	t.Helper()
	n := p.InstructionCount()
	if len(p.TruthSlots) != n || len(p.ReasonSlots) != n {
		t.Fatalf("slot column lengths = %d/%d, want %d", len(p.TruthSlots), len(p.ReasonSlots), n)
	}
	if p.TruthSlotCount == 0 || uint64(p.TruthSlotCount) > uint64(n) || uint64(p.ReasonSlotCount) > uint64(n) {
		t.Fatalf("slot peaks = %d/%d for %d instructions", p.TruthSlotCount, p.ReasonSlotCount, n)
	}
	reasonLive := make([]uint8, n)
	for row, flags := range p.RootFlags {
		if flags != 0 {
			reasonLive[row] = 1
		}
	}
	for row := n; row > 0; {
		row--
		if reasonLive[row] == 0 {
			continue
		}
		switch p.Opcodes[row] {
		case program.OpcodeAll, program.OpcodeAny, program.OpcodeNot:
			start := int(p.OperandStarts[row])
			end := start + int(p.OperandCounts[row])
			for _, operand := range p.Operands[start:end] {
				reasonLive[operand-1] = 1
			}
		}
	}
	for row := range n {
		if p.TruthSlots[row] == 0 || uint32(p.TruthSlots[row]) > p.TruthSlotCount {
			t.Fatalf("truth slot[%d] = %d outside 1..%d", row, p.TruthSlots[row], p.TruthSlotCount)
		}
		if reasonLive[row] != 0 {
			if p.ReasonSlots[row] == 0 || uint32(p.ReasonSlots[row]) > p.ReasonSlotCount {
				t.Fatalf("reason slot[%d] = %d outside 1..%d", row, p.ReasonSlots[row], p.ReasonSlotCount)
			}
		} else if p.ReasonSlots[row] != 0 {
			t.Fatalf("reason-irrelevant row %d has slot %d", row, p.ReasonSlots[row])
		}
	}
}

func snapshotExported(value reflect.Value) any {
	switch value.Kind() {
	case reflect.Struct:
		fields := make(map[string]any, value.NumField())
		typ := value.Type()
		for i := 0; i < value.NumField(); i++ {
			if typ.Field(i).PkgPath == "" {
				fields[typ.Field(i).Name] = snapshotExported(value.Field(i))
			}
		}
		return fields
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type()).Interface()
		}
		clone := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		reflect.Copy(clone, value)
		return clone.Interface()
	default:
		return value.Interface()
	}
}

func zeroDocumentSlices(doc *ast.Document) {
	value := reflect.ValueOf(doc).Elem()
	for i := 0; i < value.NumField(); i++ {
		field := value.Field(i)
		if field.Kind() == reflect.Slice && field.Len() != 0 {
			field.Index(0).Set(reflect.Zero(field.Type().Elem()))
		}
	}
}

func assertExactProgramSlices(t *testing.T, value reflect.Value, path string) {
	t.Helper()
	switch value.Kind() {
	case reflect.Struct:
		typ := value.Type()
		for i := 0; i < value.NumField(); i++ {
			assertExactProgramSlices(t, value.Field(i), path+"."+typ.Field(i).Name)
		}
	case reflect.Slice:
		if value.Len() == 0 {
			if !value.IsNil() {
				t.Fatalf("%s is a nonnil empty slice", path)
			}
			return
		}
		if value.Len() != value.Cap() {
			t.Fatalf("%s has len/cap %d/%d", path, value.Len(), value.Cap())
		}
	}
}

func assertPointerlessColumns(t *testing.T, typ reflect.Type, path string) {
	t.Helper()
	switch typ.Kind() {
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			assertPointerlessColumns(t, field.Type, path+"."+field.Name)
		}
	case reflect.Slice:
		if typeContainsPointer(typ.Elem()) {
			t.Fatalf("%s has pointer-bearing element type %s", path, typ.Elem())
		}
	case reflect.Array:
		if typeContainsPointer(typ.Elem()) {
			t.Fatalf("%s has pointer-bearing array element type %s", path, typ.Elem())
		}
	case reflect.Pointer, reflect.Map, reflect.Interface, reflect.Func, reflect.Chan, reflect.String, reflect.UnsafePointer:
		t.Fatalf("%s is pointer-bearing type %s", path, typ)
	}
}

func typeContainsPointer(typ reflect.Type) bool {
	switch typ.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Interface, reflect.Func, reflect.Chan, reflect.Slice, reflect.String, reflect.UnsafePointer:
		return true
	case reflect.Array:
		return typeContainsPointer(typ.Elem())
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			if typeContainsPointer(typ.Field(i).Type) {
				return true
			}
		}
	}
	return false
}
