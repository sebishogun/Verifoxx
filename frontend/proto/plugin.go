package frontproto

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	public "github.com/sebishogun/nornrune/frontend"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

var errInvalidRequest = errors.New("protoc-gen-nornrune: invalid request")

const frontendImport = protogen.GoImportPath("github.com/sebishogun/nornrune/frontend")
const optionImport = protogen.GoImportPath("github.com/sebishogun/nornrune/frontend/proto")

type generatedField struct {
	source string
	target string
	kind   public.ValueKind
	group  public.FieldGroup
}

type generatedMessage struct {
	name       string
	policyName string
	version    string
	expression string
	fields     []generatedField
}

type generatedFile struct {
	file     *protogen.File
	messages []generatedMessage
}

// Generate validates one protoc request and returns an all-or-nothing response.
func Generate(request *pluginpb.CodeGeneratorRequest) (*pluginpb.CodeGeneratorResponse, error) {
	if err := validateRawRequest(request); err != nil {
		return nil, err
	}
	plugin, err := (protogen.Options{}).New(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidRequest, err)
	}
	if err := GeneratePlugin(plugin); err != nil {
		return nil, err
	}
	response := plugin.Response()
	if message := response.GetError(); message != "" {
		return nil, fmt.Errorf("%w: %s", errInvalidRequest, message)
	}
	return response, nil
}

// GeneratePlugin validates all input files before emitting deterministic Go bindings.
func GeneratePlugin(plugin *protogen.Plugin) error {
	if plugin == nil {
		return errInvalidRequest
	}
	files := make([]generatedFile, 0, len(plugin.Files))
	for _, file := range plugin.Files {
		if !file.Generate {
			continue
		}
		if file.Desc.Path() == "options.proto" && file.Desc.Package() == "nornrune.frontend" && file.GoImportPath == optionImport {
			continue
		}
		generated, err := inspectFile(file)
		if err != nil {
			return err
		}
		files = append(files, generated)
	}
	if len(files) == 0 {
		return fmt.Errorf("%w: no files to generate", errInvalidRequest)
	}
	for row := range files {
		emitFile(plugin, &files[row])
	}
	return nil
}

func validateRawRequest(request *pluginpb.CodeGeneratorRequest) error {
	if request == nil || len(request.FileToGenerate) == 0 || len(request.ProtoFile) == 0 {
		return errInvalidRequest
	}
	for _, target := range request.FileToGenerate {
		found := false
		for _, file := range request.ProtoFile {
			if file.GetName() != target {
				continue
			}
			found = true
			if file.GetSyntax() != "proto3" || file.GetEdition() != descriptorpb.Edition_EDITION_UNKNOWN {
				return fmt.Errorf("%w: %s must use proto3 without editions", errInvalidRequest, file.GetName())
			}
			break
		}
		if !found {
			return fmt.Errorf("%w: missing descriptor for %s", errInvalidRequest, target)
		}
	}
	return nil
}

func inspectFile(file *protogen.File) (generatedFile, error) {
	if file == nil || file.Desc.Syntax() != protoreflect.Proto3 {
		return generatedFile{}, errInvalidRequest
	}
	result := generatedFile{file: file, messages: make([]generatedMessage, 0, len(file.Messages))}
	if len(file.Messages) == 0 {
		return generatedFile{}, fmt.Errorf("%w: %s has no policy message", errInvalidRequest, file.Desc.Path())
	}
	for _, message := range file.Messages {
		generated, err := inspectMessage(message)
		if err != nil {
			return generatedFile{}, err
		}
		result.messages = append(result.messages, generated)
	}
	return result, nil
}

func inspectMessage(message *protogen.Message) (generatedMessage, error) {
	if message == nil || message.Desc.IsMapEntry() {
		return generatedMessage{}, errInvalidRequest
	}
	if len(message.Messages) != 0 {
		return generatedMessage{}, fmt.Errorf("%w: %s contains unsupported nested messages", errInvalidRequest, message.Desc.FullName())
	}
	options, ok := message.Desc.Options().(*descriptorpb.MessageOptions)
	if !ok || options == nil {
		return generatedMessage{}, fmt.Errorf("%w: %s is missing policy options", errInvalidRequest, message.Desc.FullName())
	}
	policyName, ok := requiredExtension(options, E_PolicyName)
	if !ok {
		return generatedMessage{}, fmt.Errorf("%w: %s is missing policy_name", errInvalidRequest, message.Desc.FullName())
	}
	version, ok := requiredExtension(options, E_PolicyVersion)
	if !ok {
		return generatedMessage{}, fmt.Errorf("%w: %s is missing policy_version", errInvalidRequest, message.Desc.FullName())
	}
	expression, ok := requiredExtension(options, E_CelExpression)
	if !ok {
		return generatedMessage{}, fmt.Errorf("%w: %s is missing cel_expression", errInvalidRequest, message.Desc.FullName())
	}
	result := generatedMessage{
		name: string(message.GoIdent.GoName), policyName: policyName, version: version, expression: expression,
		fields: make([]generatedField, 0, len(message.Fields)),
	}
	bindings := public.BindingSet{Name: policyName, Version: version, Fields: make([]public.Binding, 0, len(message.Fields))}
	for _, field := range message.Fields {
		generated, err := inspectField(field)
		if err != nil {
			return generatedMessage{}, err
		}
		result.fields = append(result.fields, generated)
		bindings.Fields = append(bindings.Fields, public.Binding{
			Source: generated.source, Target: generated.target, Kind: generated.kind, Group: generated.group,
		})
	}
	if err := bindings.Validate(public.DefaultLimits()); err != nil {
		return generatedMessage{}, fmt.Errorf("%w: %s has invalid or duplicate bindings", errInvalidRequest, message.Desc.FullName())
	}
	return result, nil
}

func inspectField(field *protogen.Field) (generatedField, error) {
	if field == nil || field.Desc.Cardinality() == protoreflect.Repeated || field.Desc.ContainingOneof() != nil || field.Desc.HasOptionalKeyword() {
		return generatedField{}, fmt.Errorf("%w: unsupported field shape", errInvalidRequest)
	}
	kind, ok := valueKind(field.Desc.Kind())
	if !ok {
		return generatedField{}, fmt.Errorf("%w: %s has unsupported type %s", errInvalidRequest, field.Desc.FullName(), field.Desc.Kind())
	}
	options, ok := field.Desc.Options().(*descriptorpb.FieldOptions)
	if !ok || options == nil {
		return generatedField{}, fmt.Errorf("%w: %s is missing canonical_target", errInvalidRequest, field.Desc.FullName())
	}
	target, ok := requiredExtension(options, E_CanonicalTarget)
	if !ok {
		return generatedField{}, fmt.Errorf("%w: %s is missing canonical_target", errInvalidRequest, field.Desc.FullName())
	}
	group, ok := targetGroup(target)
	if !ok {
		return generatedField{}, fmt.Errorf("%w: %s has unsupported target group", errInvalidRequest, field.Desc.FullName())
	}
	return generatedField{source: field.Desc.JSONName(), target: target, kind: kind, group: group}, nil
}

func requiredExtension(options proto.Message, extension protoreflect.ExtensionType) (string, bool) {
	if !proto.HasExtension(options, extension) {
		return "", false
	}
	value, ok := proto.GetExtension(options, extension).(string)
	return value, ok && value != ""
}

func valueKind(kind protoreflect.Kind) (public.ValueKind, bool) {
	switch kind {
	case protoreflect.StringKind:
		return public.ValueKindString, true
	case protoreflect.BoolKind:
		return public.ValueKindBoolean, true
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return public.ValueKindInteger, true
	default:
		return public.ValueKindInvalid, false
	}
}

func targetGroup(target string) (public.FieldGroup, bool) {
	switch {
	case strings.HasPrefix(target, "subject."):
		return public.FieldGroupSubject, true
	case strings.HasPrefix(target, "action."):
		return public.FieldGroupAction, true
	case strings.HasPrefix(target, "resource."):
		return public.FieldGroupResource, true
	case strings.HasPrefix(target, "output."):
		return public.FieldGroupOutput, true
	case strings.HasPrefix(target, "context."):
		return public.FieldGroupContext, true
	default:
		return public.FieldGroupInvalid, false
	}
}

func emitFile(plugin *protogen.Plugin, file *generatedFile) {
	generated := plugin.NewGeneratedFile(file.file.GeneratedFilenamePrefix+"_nornrune.pb.go", file.file.GoImportPath)
	generated.P("// Code generated by protoc-gen-nornrune. DO NOT EDIT.")
	generated.P("// source: ", file.file.Desc.Path())
	generated.P()
	generated.P("package ", file.file.GoPackageName)
	generated.P()
	for _, message := range file.messages {
		emitMessage(generated, &message)
	}
}

func emitMessage(generated *protogen.GeneratedFile, message *generatedMessage) {
	bindingSet := generated.QualifiedGoIdent(protogen.GoIdent{GoName: "BindingSet", GoImportPath: frontendImport})
	binding := generated.QualifiedGoIdent(protogen.GoIdent{GoName: "Binding", GoImportPath: frontendImport})
	generated.P("const ", message.name, "CEL = ", strconv.Quote(message.expression))
	generated.P()
	generated.P("var ", message.name, "BindingSet = ", bindingSet, "{")
	generated.P("Name: ", strconv.Quote(message.policyName), ",")
	generated.P("Version: ", strconv.Quote(message.version), ",")
	generated.P("Fields: []", binding, "{")
	for _, field := range message.fields {
		generated.P("{Source: ", strconv.Quote(field.source), ", Target: ", strconv.Quote(field.target),
			", Kind: ", valueKindIdent(generated, field.kind), ", Group: ", fieldGroupIdent(generated, field.group), "},")
	}
	generated.P("},")
	generated.P("}")
	generated.P()
}

func valueKindIdent(generated *protogen.GeneratedFile, kind public.ValueKind) string {
	return generated.QualifiedGoIdent(protogen.GoIdent{GoName: "ValueKind" + exportedEnumName(kind.String()), GoImportPath: frontendImport})
}

func fieldGroupIdent(generated *protogen.GeneratedFile, group public.FieldGroup) string {
	return generated.QualifiedGoIdent(protogen.GoIdent{GoName: "FieldGroup" + exportedEnumName(group.String()), GoImportPath: frontendImport})
}

func exportedEnumName(value string) string {
	if value == "" {
		return "Invalid"
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
