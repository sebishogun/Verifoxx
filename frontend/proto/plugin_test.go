package frontproto

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestGenerateEmitsDeterministicStaticBindings(t *testing.T) {
	request := validRequest()
	first, err := Generate(request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.File) != 1 || len(second.File) != 1 || first.File[0].GetName() != "policy_nornrune.pb.go" {
		t.Fatalf("generated files = %+v and %+v", first.File, second.File)
	}
	if first.File[0].GetContent() != second.File[0].GetContent() {
		t.Fatal("Generate output is not deterministic")
	}
	content := first.File[0].GetContent()
	for _, want := range []string{
		`const PolicyRequestCEL = "team == \"blue\" && count >= 2 && enabled"`,
		`var PolicyRequestBindingSet = frontend.BindingSet{`,
		`Name:`,
		`"access-policy"`,
		`Version:`,
		`"v1"`,
		`Source: "teamName"`,
		`Target: "subject.team"`,
		`Kind: frontend.ValueKindString`,
		`Group: frontend.FieldGroupSubject`,
		`Source: "count"`,
		`Kind: frontend.ValueKindInteger`,
		`Group: frontend.FieldGroupContext`,
		`Source: "enabled"`,
		`Kind: frontend.ValueKindBoolean`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("generated code does not contain %q:\n%s", want, content)
		}
	}
	for _, forbidden := range []string{"protoreflect", "protodesc", "descriptorpb", "proto.GetExtension", "map[", "func init("} {
		if strings.Contains(content, forbidden) {
			t.Errorf("generated code contains runtime construct %q:\n%s", forbidden, content)
		}
	}
}

func TestGenerateIgnoresItsOptionSchemaGenerationTarget(t *testing.T) {
	request := validRequest()
	request.FileToGenerate = []string{"options.proto", "policy.proto"}
	request.ProtoFile = append([]*descriptorpb.FileDescriptorProto{{
		Name:    proto.String("options.proto"),
		Package: proto.String("nornrune.frontend"),
		Syntax:  proto.String("proto3"),
		Options: &descriptorpb.FileOptions{GoPackage: proto.String("github.com/sebishogun/nornrune/frontend/proto;frontproto")},
	}}, request.ProtoFile...)

	response, err := Generate(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.File) != 1 || response.File[0].GetName() != "policy_nornrune.pb.go" {
		t.Fatalf("generated files = %+v, want only policy binding", response.File)
	}
}

func TestGenerateAllowsProto2ImportDescriptors(t *testing.T) {
	request := validRequest()
	request.ProtoFile = append([]*descriptorpb.FileDescriptorProto{{
		Name:    proto.String("legacy/import.proto"),
		Package: proto.String("legacy"),
		Syntax:  proto.String("proto2"),
		Options: &descriptorpb.FileOptions{GoPackage: proto.String("example.com/legacy;legacy")},
	}}, request.ProtoFile...)

	response, err := Generate(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.File) != 1 || response.File[0].GetName() != "policy_nornrune.pb.go" {
		t.Fatalf("generated files = %+v, want policy binding", response.File)
	}
}

func TestGenerateRejectsUnsupportedFieldShapesAtomically(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*descriptorpb.DescriptorProto, *descriptorpb.FieldDescriptorProto)
	}{
		{name: "repeated", mutate: func(_ *descriptorpb.DescriptorProto, field *descriptorpb.FieldDescriptorProto) {
			field.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
		}},
		{name: "map", mutate: func(message *descriptorpb.DescriptorProto, field *descriptorpb.FieldDescriptorProto) {
			field.Label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum()
			field.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
			field.TypeName = proto.String(".example.PolicyRequest.LabelsEntry")
			message.NestedType = []*descriptorpb.DescriptorProto{{
				Name: proto.String("LabelsEntry"), Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
			}}
		}},
		{name: "oneof", mutate: func(message *descriptorpb.DescriptorProto, field *descriptorpb.FieldDescriptorProto) {
			message.OneofDecl = []*descriptorpb.OneofDescriptorProto{{Name: proto.String("choice")}}
			field.OneofIndex = proto.Int32(0)
		}},
		{name: "message", mutate: setFieldType(descriptorpb.FieldDescriptorProto_TYPE_MESSAGE)},
		{name: "enum", mutate: setFieldType(descriptorpb.FieldDescriptorProto_TYPE_ENUM)},
		{name: "float", mutate: setFieldType(descriptorpb.FieldDescriptorProto_TYPE_FLOAT)},
		{name: "double", mutate: setFieldType(descriptorpb.FieldDescriptorProto_TYPE_DOUBLE)},
		{name: "uint32", mutate: setFieldType(descriptorpb.FieldDescriptorProto_TYPE_UINT32)},
		{name: "uint64", mutate: setFieldType(descriptorpb.FieldDescriptorProto_TYPE_UINT64)},
		{name: "fixed32", mutate: setFieldType(descriptorpb.FieldDescriptorProto_TYPE_FIXED32)},
		{name: "fixed64", mutate: setFieldType(descriptorpb.FieldDescriptorProto_TYPE_FIXED64)},
		{name: "bytes", mutate: setFieldType(descriptorpb.FieldDescriptorProto_TYPE_BYTES)},
		{name: "proto3 optional", mutate: func(message *descriptorpb.DescriptorProto, field *descriptorpb.FieldDescriptorProto) {
			message.OneofDecl = []*descriptorpb.OneofDescriptorProto{{Name: proto.String("_team_name")}}
			field.OneofIndex = proto.Int32(0)
			field.Proto3Optional = proto.Bool(true)
		}},
		{name: "nested message", mutate: func(message *descriptorpb.DescriptorProto, _ *descriptorpb.FieldDescriptorProto) {
			message.NestedType = []*descriptorpb.DescriptorProto{{Name: proto.String("Nested")}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest()
			message := request.ProtoFile[0].MessageType[0]
			test.mutate(message, message.Field[0])
			response, err := Generate(request)
			if err == nil || response != nil {
				t.Fatalf("Generate = (%+v, %v), want atomic error", response, err)
			}
		})
	}
}

func TestGenerateRejectsInvalidOptionsAndFilesAtomically(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*pluginpb.CodeGeneratorRequest)
	}{
		{name: "missing name", mutate: func(request *pluginpb.CodeGeneratorRequest) {
			proto.ClearExtension(request.ProtoFile[0].MessageType[0].Options, E_PolicyName)
		}},
		{name: "missing version", mutate: func(request *pluginpb.CodeGeneratorRequest) {
			proto.ClearExtension(request.ProtoFile[0].MessageType[0].Options, E_PolicyVersion)
		}},
		{name: "missing expression", mutate: func(request *pluginpb.CodeGeneratorRequest) {
			proto.ClearExtension(request.ProtoFile[0].MessageType[0].Options, E_CelExpression)
		}},
		{name: "missing target", mutate: func(request *pluginpb.CodeGeneratorRequest) {
			proto.ClearExtension(request.ProtoFile[0].MessageType[0].Field[0].Options, E_CanonicalTarget)
		}},
		{name: "duplicate target", mutate: func(request *pluginpb.CodeGeneratorRequest) {
			proto.SetExtension(request.ProtoFile[0].MessageType[0].Field[1].Options, E_CanonicalTarget, "subject.team")
		}},
		{name: "invalid target group", mutate: func(request *pluginpb.CodeGeneratorRequest) {
			proto.SetExtension(request.ProtoFile[0].MessageType[0].Field[0].Options, E_CanonicalTarget, "unknown.team")
		}},
		{name: "edition", mutate: func(request *pluginpb.CodeGeneratorRequest) {
			request.ProtoFile[0].Syntax = proto.String("editions")
			request.ProtoFile[0].Edition = descriptorpb.Edition_EDITION_2024.Enum()
		}},
		{name: "proto2", mutate: func(request *pluginpb.CodeGeneratorRequest) {
			request.ProtoFile[0].Syntax = proto.String("proto2")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validRequest()
			test.mutate(request)
			response, err := Generate(request)
			if err == nil || response != nil {
				t.Fatalf("Generate = (%+v, %v), want atomic error", response, err)
			}
		})
	}
}

func TestGenerateRejectsNilAndMissingGenerationTargets(t *testing.T) {
	for _, request := range []*pluginpb.CodeGeneratorRequest{nil, {ProtoFile: validRequest().ProtoFile}} {
		response, err := Generate(request)
		if err == nil || response != nil {
			t.Fatalf("Generate = (%+v, %v), want atomic error", response, err)
		}
	}
}

func validRequest() *pluginpb.CodeGeneratorRequest {
	messageOptions := &descriptorpb.MessageOptions{}
	proto.SetExtension(messageOptions, E_PolicyName, "access-policy")
	proto.SetExtension(messageOptions, E_PolicyVersion, "v1")
	proto.SetExtension(messageOptions, E_CelExpression, `team == "blue" && count >= 2 && enabled`)
	fields := []*descriptorpb.FieldDescriptorProto{
		field("team_name", "teamName", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING, "subject.team"),
		field("count", "count", 2, descriptorpb.FieldDescriptorProto_TYPE_SINT64, "context.count"),
		field("enabled", "enabled", 3, descriptorpb.FieldDescriptorProto_TYPE_BOOL, "context.enabled"),
	}
	file := &descriptorpb.FileDescriptorProto{
		Name: proto.String("policy.proto"), Package: proto.String("example"), Syntax: proto.String("proto3"),
		Options:     &descriptorpb.FileOptions{GoPackage: proto.String("example.com/policy;policy")},
		MessageType: []*descriptorpb.DescriptorProto{{Name: proto.String("PolicyRequest"), Options: messageOptions, Field: fields}},
	}
	return &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"policy.proto"}, ProtoFile: []*descriptorpb.FileDescriptorProto{file},
		Parameter: proto.String("paths=source_relative"),
	}
}

func field(name, jsonName string, number int32, kind descriptorpb.FieldDescriptorProto_Type, target string) *descriptorpb.FieldDescriptorProto {
	options := &descriptorpb.FieldOptions{}
	proto.SetExtension(options, E_CanonicalTarget, target)
	return &descriptorpb.FieldDescriptorProto{
		Name: proto.String(name), JsonName: proto.String(jsonName), Number: proto.Int32(number),
		Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), Type: kind.Enum(), Options: options,
	}
}

func setFieldType(kind descriptorpb.FieldDescriptorProto_Type) func(*descriptorpb.DescriptorProto, *descriptorpb.FieldDescriptorProto) {
	return func(_ *descriptorpb.DescriptorProto, field *descriptorpb.FieldDescriptorProto) {
		field.Type = kind.Enum()
	}
}
