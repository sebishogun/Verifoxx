package wasm

import (
	"crypto/sha256"
	"reflect"
	"testing"

	"github.com/sebishogun/nornrune/internal/adapters/jsonpolicy"
	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/compile"
	"github.com/sebishogun/nornrune/internal/program"
	"github.com/sebishogun/nornrune/internal/schema"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

func TestProgramArtifactRoundTripOwnsEveryCanonicalColumn(t *testing.T) {
	compiled := compileWASMTestProgram(t)
	manifest := testManifest()
	artifact, err := EncodeProgram(nil, compiled, manifest)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EncodeProgram(nil, compiled, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(artifact, second) {
		t.Fatal("same Program produced different artifacts")
	}

	decoded, metadata, err := DecodeProgram(artifact, manifest.Limits)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.ABI != manifest.ABI || metadata.Schema != manifest.Schema || metadata.Profile != manifest.Profile ||
		metadata.RequiredCapabilities != manifest.RequiredCapabilities || metadata.ProgramHash != compiled.ContentHash ||
		metadata.ArtifactHash == [sha256.Size]byte{} {
		t.Fatalf("metadata = %+v", metadata)
	}
	want, err := program.Freeze(compiled)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(&want, decoded) {
		t.Fatal("decoded Program differs from frozen source")
	}

	decoded.InputBytes[0] ^= 0xff
	decoded.SymbolBytes[0] ^= 0xff
	decoded.Opcodes[0]++
	if compiled.InputBytes[0] == decoded.InputBytes[0] || compiled.SymbolBytes[0] == decoded.SymbolBytes[0] || compiled.Opcodes[0] == decoded.Opcodes[0] {
		t.Fatal("decoded Program borrowed source Program storage")
	}
}

func TestProgramArtifactRejectsNilAndWrongProgramHash(t *testing.T) {
	manifest := testManifest()
	if _, err := EncodeProgram(nil, nil, manifest); err == nil {
		t.Fatal("EncodeProgram(nil Program) succeeded")
	}
	compiled := compileWASMTestProgram(t)
	compiled.ContentHash[0] ^= 0xff
	if _, err := EncodeProgram(nil, compiled, manifest); err == nil {
		t.Fatal("EncodeProgram accepted mismatched source hash")
	}
}

func TestProgramArtifactDecodeRejectsInvalidExecutionSemantics(t *testing.T) {
	manifest := testManifest()
	tests := []struct {
		name   string
		mutate func(*program.Program)
	}{
		{name: "opcode", mutate: func(compiled *program.Program) { compiled.Opcodes[0] = 255 }},
		{name: "field reference", mutate: func(compiled *program.Program) { compiled.Fields[0] = schema.FieldID(len(compiled.FieldNames) + 1) }},
		{name: "symbol range", mutate: func(compiled *program.Program) { compiled.SymbolStarts[0] = uint32(len(compiled.SymbolBytes) + 1) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compiled := compileWASMTestProgram(t)
			test.mutate(compiled)
			artifact, err := EncodeProgram(nil, compiled, manifest)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := DecodeProgram(artifact, manifest.Limits); err == nil {
				t.Fatal("DecodeProgram accepted invalid execution semantics")
			}
		})
	}
}

func TestProgramSchemaVersionPinsCanonicalLayout(t *testing.T) {
	if CurrentSchemaVersion != 1 {
		t.Fatalf("schema version = %d; add the new version's pinned layout digest", CurrentSchemaVersion)
	}
	got := programLayoutDigest()
	if got != version1ProgramLayoutDigest {
		t.Fatalf("Program layout digest = %x, want version 1 digest %x; bump schema version for a wire-layout change", got, version1ProgramLayoutDigest)
	}
}

func compileWASMTestProgram(t testing.TB) *program.Program {
	t.Helper()
	fields := []struct {
		name  string
		group schema.FieldGroup
	}{
		{name: "requester.team", group: schema.FieldGroupSubject},
		{name: "requester.trust", group: schema.FieldGroupSubject},
		{name: "action.type", group: schema.FieldGroupAction},
		{name: "action.output", group: schema.FieldGroupAction},
		{name: "action.dataset", group: schema.FieldGroupResource},
		{name: "environment.execution_env", group: schema.FieldGroupContext},
		{name: "environment.usage", group: schema.FieldGroupContext},
	}
	symbols := schema.NewSymbolInterner(len(fields) * 2)
	builder := schema.NewBuilder()
	for _, field := range fields {
		name, err := symbols.Intern([]byte(field.name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := builder.AddField(name, schema.ValueKindSymbol, field.group); err != nil {
			t.Fatal(err)
		}
	}
	fieldSchema := builder.Finish()
	limits := jsonpolicy.Limits{
		MaxSourceBytes: 16 << 20, MaxCatalogItems: 1024, MaxStringBytes: 1 << 20,
		MaxDepth: 256, MaxNodes: 1 << 20, MaxValues: 1 << 17, MaxArrayItems: 1 << 16,
		MaxSymbolBytes: 4 << 20, MaxRequirements: 1024, MaxClauses: 1 << 13,
		MaxTemplateBytes: 1 << 20, MaxAssumptions: 1024, MaxUncertainty: 1024,
	}
	var document ast.Builder
	var decoder jsonpolicy.Decoder
	if err := decoder.Decode(&document, []byte(nornrune.Source()), fieldSchema, symbols, limits); err != nil {
		t.Fatal(err)
	}
	var lowerer compile.Lowerer
	var compiled program.Program
	if err := lowerer.Lower(&compiled, document.Document(), fieldSchema, symbols); err != nil {
		t.Fatal(err)
	}
	return &compiled
}
