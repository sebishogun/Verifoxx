package frontend

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestEnumValuesAreStableAndBounded(t *testing.T) {
	tests := []struct {
		name string
		got  []uint8
		want []uint8
	}{
		{"language", []uint8{uint8(LanguageNative), uint8(LanguageCEL), uint8(LanguageRego), uint8(LanguageCedar), uint8(LanguageProtobuf)}, []uint8{1, 2, 3, 4, 5}},
		{"value kind", []uint8{uint8(ValueKindString), uint8(ValueKindInteger), uint8(ValueKindBoolean)}, []uint8{1, 2, 3}},
		{"field group", []uint8{uint8(FieldGroupSubject), uint8(FieldGroupAction), uint8(FieldGroupResource), uint8(FieldGroupOutput), uint8(FieldGroupContext)}, []uint8{1, 2, 3, 4, 5}},
		{"node kind", []uint8{uint8(NodeKindBoolean), uint8(NodeKindCompare), uint8(NodeKindAll), uint8(NodeKindAny), uint8(NodeKindNot), uint8(NodeKindDefined)}, []uint8{1, 2, 3, 4, 5, 6}},
		{"compare op", []uint8{uint8(CompareOpEqual), uint8(CompareOpNotEqual), uint8(CompareOpLess), uint8(CompareOpLessEqual), uint8(CompareOpGreater), uint8(CompareOpGreaterEqual), uint8(CompareOpIn)}, []uint8{1, 2, 3, 4, 5, 6, 7}},
		{"default", []uint8{uint8(DefaultEscalate), uint8(DefaultReject)}, []uint8{1, 2}},
		{"support", []uint8{uint8(SupportSupported), uint8(SupportRestricted), uint8(SupportRejected)}, []uint8{1, 2, 3}},
		{"diagnostic", []uint8{uint8(CodeSyntax), uint8(CodeType), uint8(CodeUnsupported), uint8(CodeUnknownField), uint8(CodeDuplicate), uint8(CodeLimit), uint8(CodeInvalidBinding), uint8(CodeInvalidPolicy)}, []uint8{1, 2, 3, 4, 5, 6, 7, 8}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !reflect.DeepEqual(tt.got, tt.want) {
				t.Fatalf("values = %v, want %v", tt.got, tt.want)
			}
		})
	}

	if LanguageInvalid.Valid() || Language(255).Valid() ||
		ValueKindInvalid.Valid() || ValueKind(255).Valid() ||
		FieldGroupInvalid.Valid() || FieldGroup(255).Valid() ||
		NodeKindInvalid.Valid() || NodeKind(255).Valid() ||
		CompareOpInvalid.Valid() || CompareOp(255).Valid() ||
		DefaultInvalid.Valid() || DefaultDecision(255).Valid() ||
		SupportInvalid.Valid() || Support(255).Valid() ||
		CodeInvalid.Valid() || DiagnosticCode(255).Valid() {
		t.Fatal("an invalid or out-of-range enum reported valid")
	}
}

func TestEnumsRoundTripAsStrictJSONText(t *testing.T) {
	type enums struct {
		Language   Language        `json:"language"`
		Value      ValueKind       `json:"value"`
		Group      FieldGroup      `json:"group"`
		Node       NodeKind        `json:"node"`
		Op         CompareOp       `json:"op"`
		Default    DefaultDecision `json:"default"`
		Support    Support         `json:"support"`
		Diagnostic DiagnosticCode  `json:"diagnostic"`
	}
	want := enums{
		Language: LanguageCEL, Value: ValueKindString, Group: FieldGroupContext,
		Node: NodeKindCompare, Op: CompareOpLessEqual, Default: DefaultEscalate,
		Support: SupportRestricted, Diagnostic: CodeUnsupported,
	}
	encoded, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	const wantJSON = `{"language":"cel","value":"string","group":"context","node":"compare","op":"less_equal","default":"escalate","support":"restricted","diagnostic":"unsupported"}`
	if string(encoded) != wantJSON {
		t.Fatalf("Marshal = %s, want %s", encoded, wantJSON)
	}
	var got enums
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}

	unknowns := []any{
		new(Language), new(ValueKind), new(FieldGroup), new(NodeKind),
		new(CompareOp), new(DefaultDecision), new(Support), new(DiagnosticCode),
	}
	for _, target := range unknowns {
		if err := json.Unmarshal([]byte(`"future"`), target); err == nil {
			t.Errorf("%T accepted unknown text", target)
		}
		if err := json.Unmarshal([]byte(`1`), target); err == nil {
			t.Errorf("%T accepted a numeric enum", target)
		}
	}
}

func TestDefaultLimitsAreFiniteAndNonzero(t *testing.T) {
	got := DefaultLimits()
	want := Limits{
		MaxSourceBytes: 4 << 20,
		MaxNodes:       65_536,
		MaxDepth:       128,
		MaxFields:      4_096,
		MaxLiterals:    131_072,
		MaxChildren:    65_536,
		MaxStringBytes: 1 << 20,
		MaxDiagnostics: 128,
	}
	if got != want {
		t.Fatalf("DefaultLimits = %+v, want %+v", got, want)
	}
	if !got.Valid() || (Limits{}).Valid() {
		t.Fatal("limits validity does not distinguish bounded defaults from zero")
	}
}

func TestBindingSetValidateRejectsMalformedOrDuplicateBindingsWithoutMutation(t *testing.T) {
	valid := func() BindingSet {
		return BindingSet{
			Name: "policy", Version: "v1", Decision: "allow",
			Fields: []Binding{
				{Source: "request.team", Target: "requester.team", Kind: ValueKindString, Group: FieldGroupSubject},
				{Source: "request.count", Target: "environment.count", Kind: ValueKindInteger, Group: FieldGroupContext},
			},
		}
	}
	tests := []struct {
		name   string
		mutate func(*BindingSet, *Limits)
	}{
		{"empty name", func(set *BindingSet, _ *Limits) { set.Name = "" }},
		{"empty version", func(set *BindingSet, _ *Limits) { set.Version = "" }},
		{"malformed source", func(set *BindingSet, _ *Limits) { set.Fields[0].Source = "request..team" }},
		{"malformed target", func(set *BindingSet, _ *Limits) { set.Fields[0].Target = ".requester" }},
		{"malformed decision", func(set *BindingSet, _ *Limits) { set.Decision = "9allow" }},
		{"invalid kind", func(set *BindingSet, _ *Limits) { set.Fields[0].Kind = ValueKindInvalid }},
		{"invalid group", func(set *BindingSet, _ *Limits) { set.Fields[0].Group = FieldGroupInvalid }},
		{"duplicate source", func(set *BindingSet, _ *Limits) { set.Fields[1].Source = set.Fields[0].Source }},
		{"duplicate target", func(set *BindingSet, _ *Limits) { set.Fields[1].Target = set.Fields[0].Target }},
		{"field limit", func(_ *BindingSet, limits *Limits) { limits.MaxFields = 1 }},
		{"string limit", func(_ *BindingSet, limits *Limits) { limits.MaxStringBytes = 8 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := valid()
			limits := DefaultLimits()
			tt.mutate(&set, &limits)
			before := set
			before.Fields = append([]Binding(nil), set.Fields...)
			if err := set.Validate(limits); err == nil {
				t.Fatal("Validate returned nil")
			}
			if !reflect.DeepEqual(set, before) {
				t.Fatalf("Validate mutated bindings: got %+v, want %+v", set, before)
			}
		})
	}

	set := valid()
	if err := set.Validate(DefaultLimits()); err != nil {
		t.Fatalf("valid bindings: %v", err)
	}
}

func TestDiagnosticsArePointerlessAndSortDeterministically(t *testing.T) {
	diagnostics := []Diagnostic{
		{Span: Span{Start: 8, End: 9}, Row: 1, Code: CodeType, Language: LanguageCEL},
		{Span: Span{Start: 2, End: 7}, Row: 3, Code: CodeUnsupported, Language: LanguageCEL},
		{Span: Span{Start: 2, End: 7}, Row: 2, Code: CodeUnsupported, Language: LanguageCEL},
		{Span: Span{Start: 2, End: 7}, Row: 9, Code: CodeSyntax, Language: LanguageCEL},
		{Span: Span{Start: 2, End: 6}, Row: 4, Code: CodeLimit, Language: LanguageCEL},
	}
	SortDiagnostics(diagnostics)
	wantCodes := []DiagnosticCode{CodeLimit, CodeSyntax, CodeUnsupported, CodeUnsupported, CodeType}
	wantRows := []uint32{4, 9, 2, 3, 1}
	for i := range diagnostics {
		if diagnostics[i].Code != wantCodes[i] || diagnostics[i].Row != wantRows[i] {
			t.Fatalf("diagnostic[%d] = %+v", i, diagnostics[i])
		}
	}
	assertPointerless(t, reflect.TypeOf(Diagnostic{}))
	assertPointerless(t, reflect.TypeOf(NodeID(0)))
	assertPointerless(t, reflect.TypeOf(FieldID(0)))
	assertPointerless(t, reflect.TypeOf(LiteralID(0)))
}

func assertPointerless(t *testing.T, typ reflect.Type) {
	t.Helper()
	switch typ.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.String, reflect.Interface, reflect.Func, reflect.Chan:
		t.Fatalf("%v contains pointers", typ)
	case reflect.Array:
		assertPointerless(t, typ.Elem())
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			assertPointerless(t, typ.Field(i).Type)
		}
	}
}
