package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	public "github.com/sebishogun/nornrune/frontend"
	"github.com/sebishogun/nornrune/frontend/cedar"
	"github.com/sebishogun/nornrune/frontend/cel"
	"github.com/sebishogun/nornrune/frontend/rego"
	internalfrontend "github.com/sebishogun/nornrune/internal/frontend"
	"github.com/sebishogun/nornrune/internal/program"
)

var (
	errInvalidFrontendFormat = errors.New("unsupported policy format")
	errFrontendInputs        = errors.New("non-native formats require explicit --policy and --bindings paths")
	errNativeBindings        = errors.New("--bindings is not valid for the native format")
	errRuntimeProtobuf       = errors.New("protobuf is a build-time frontend and cannot be selected at runtime")
	errInvalidBindings       = errors.New("invalid frontend bindings")
)

type frontendFlags struct {
	format       string
	bindingsPath string
}

type frontendSelection struct {
	bindings public.BindingSet
	language public.Language
}

type strictBindingSet struct {
	Name     string
	Version  string
	Decision string
	Fields   []strictBinding
}

type strictBinding struct {
	Source string
	Target string
	Kind   public.ValueKind
	Group  public.FieldGroup
}

func bindFrontendFlags(cmd *cobra.Command, flags *frontendFlags) {
	cmd.Flags().StringVar(&flags.format, "format", "native", "policy format: native, cel, rego, or cedar")
	cmd.Flags().StringVar(&flags.bindingsPath, "bindings", "", "frontend binding JSON path (- for stdin)")
}

func loadFrontendSources(
	sourceFlags sourceFlags,
	flags frontendFlags,
	stdin io.Reader,
	deps dependencies,
	need sourceMask,
) (sources, frontendSelection, error) {
	var language public.Language
	if err := language.UnmarshalText([]byte(flags.format)); err != nil {
		return sources{}, frontendSelection{}, usageError(fmt.Errorf("%w: %s", errInvalidFrontendFormat, flags.format))
	}
	if language == public.LanguageSQL {
		return sources{}, frontendSelection{}, usageError(fmt.Errorf("%w: %s", errInvalidFrontendFormat, flags.format))
	}
	if language == public.LanguageProtobuf {
		return sources{}, frontendSelection{}, usageError(errRuntimeProtobuf)
	}
	if language == public.LanguageNative {
		if flags.bindingsPath != "" {
			return sources{}, frontendSelection{}, usageError(errNativeBindings)
		}
		loaded, err := loadSources(sourceFlags, stdin, deps, need)
		return loaded, frontendSelection{language: language}, err
	}
	if sourceFlags.policyPath == "" || flags.bindingsPath == "" {
		return sources{}, frontendSelection{}, usageError(errFrontendInputs)
	}
	if countStdinInputs(sourceFlags, flags.bindingsPath, need) > 1 {
		return sources{}, frontendSelection{}, usageError(errors.New("only one input may read from stdin"))
	}
	loaded, err := loadSources(sourceFlags, stdin, deps, need)
	if err != nil {
		return sources{}, frontendSelection{}, err
	}
	bindingSource, err := loadBindingSource(flags.bindingsPath, stdin, deps.readBoundedFile, public.DefaultLimits().MaxSourceBytes)
	if err != nil {
		return sources{}, frontendSelection{}, usageError(fmt.Errorf("%w: %v", errInvalidBindings, err))
	}
	bindings, err := decodeBindings(bindingSource, public.DefaultLimits())
	if err != nil {
		return sources{}, frontendSelection{}, usageError(fmt.Errorf("%w: %v", errInvalidBindings, err))
	}
	return loaded, frontendSelection{bindings: bindings, language: language}, nil
}

func countStdinInputs(flags sourceFlags, bindingsPath string, need sourceMask) int {
	count := 0
	if need&sourcePolicy != 0 && flags.policyPath == "-" {
		count++
	}
	if need&sourceRequests != 0 && flags.requestPath == "-" {
		count++
	}
	if need&sourceEvidence != 0 && flags.evidencePath == "-" {
		count++
	}
	if bindingsPath == "-" {
		count++
	}
	return count
}

func loadBindingSource(path string, stdin io.Reader, readFile func(string, uint32) ([]byte, error), maxBytes uint32) ([]byte, error) {
	var (
		source []byte
		err    error
	)
	if path == "-" {
		if stdin == nil {
			return nil, errors.New("stdin is unavailable")
		}
		source, err = io.ReadAll(io.LimitReader(stdin, int64(maxBytes)+1))
	} else {
		if readFile == nil {
			return nil, errors.New("file input is unavailable")
		}
		source, err = readFile(path, maxBytes)
	}
	if err != nil {
		return nil, err
	}
	if uint64(len(source)) > uint64(maxBytes) {
		return nil, errors.New("binding source exceeds limit")
	}
	return source, nil
}

func decodeBindings(source []byte, limits public.Limits) (public.BindingSet, error) {
	if !limits.Valid() || uint64(len(source)) > uint64(limits.MaxSourceBytes) {
		return public.BindingSet{}, errInvalidBindings
	}
	decoder := json.NewDecoder(bytes.NewReader(source))
	var strict strictBindingSet
	if err := decoder.Decode(&strict); err != nil {
		return public.BindingSet{}, err
	}
	if err := requireJSONEnd(decoder); err != nil {
		return public.BindingSet{}, err
	}
	bindings := public.BindingSet{
		Name: strict.Name, Version: strict.Version, Decision: strict.Decision,
		Fields: make([]public.Binding, len(strict.Fields)),
	}
	for row := range strict.Fields {
		field := &strict.Fields[row]
		bindings.Fields[row] = public.Binding{Source: field.Source, Target: field.Target, Kind: field.Kind, Group: field.Group}
	}
	if err := bindings.Validate(limits); err != nil {
		return public.BindingSet{}, err
	}
	return bindings, nil
}

func (value *strictBindingSet) UnmarshalJSON(source []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(source))
	if err := requireJSONObject(decoder); err != nil {
		return err
	}
	var seen uint8
	for decoder.More() {
		key, err := decodeJSONKey(decoder)
		if err != nil {
			return err
		}
		var bit uint8
		switch key {
		case "name":
			bit, err = 1<<0, decoder.Decode(&value.Name)
		case "version":
			bit, err = 1<<1, decoder.Decode(&value.Version)
		case "decision":
			bit, err = 1<<2, decoder.Decode(&value.Decision)
		case "fields":
			bit, err = 1<<3, decoder.Decode(&value.Fields)
		default:
			return fmt.Errorf("unknown binding-set field %q", key)
		}
		if seen&bit != 0 {
			return fmt.Errorf("duplicate binding-set field %q", key)
		}
		seen |= bit
		if err != nil {
			return err
		}
	}
	return requireJSONObjectEnd(decoder)
}

func (value *strictBinding) UnmarshalJSON(source []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(source))
	if err := requireJSONObject(decoder); err != nil {
		return err
	}
	var seen uint8
	for decoder.More() {
		key, err := decodeJSONKey(decoder)
		if err != nil {
			return err
		}
		var bit uint8
		switch key {
		case "source":
			bit, err = 1<<0, decoder.Decode(&value.Source)
		case "target":
			bit, err = 1<<1, decoder.Decode(&value.Target)
		case "kind":
			bit, err = 1<<2, decoder.Decode(&value.Kind)
		case "group":
			bit, err = 1<<3, decoder.Decode(&value.Group)
		default:
			return fmt.Errorf("unknown binding field %q", key)
		}
		if seen&bit != 0 {
			return fmt.Errorf("duplicate binding field %q", key)
		}
		seen |= bit
		if err != nil {
			return err
		}
	}
	return requireJSONObjectEnd(decoder)
}

func requireJSONObject(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return errors.New("expected JSON object")
	}
	return nil
}

func requireJSONObjectEnd(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '}' {
		return errors.New("expected end of JSON object")
	}
	return requireJSONEnd(decoder)
}

func decodeJSONKey(decoder *json.Decoder) (string, error) {
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}
	key, ok := token.(string)
	if !ok {
		return "", errors.New("expected JSON object field")
	}
	return key, nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func compileFrontend(selection frontendSelection, source []byte) (*program.Program, []public.Diagnostic, error) {
	limits := public.DefaultLimits()
	var (
		policy      *public.Policy
		diagnostics []public.Diagnostic
	)
	switch selection.language {
	case public.LanguageCEL:
		policy, diagnostics = cel.Compile(source, selection.bindings, limits)
	case public.LanguageRego:
		policy, diagnostics = rego.Compile(source, selection.bindings, limits)
	case public.LanguageCedar:
		policy, diagnostics = cedar.Compile(source, selection.bindings, limits)
	default:
		return nil, nil, errInvalidFrontendFormat
	}
	if len(diagnostics) != 0 {
		return nil, diagnostics, nil
	}
	compiled, diagnostics, err := internalfrontend.Compile(policy)
	for row := range diagnostics {
		if diagnostics[row].Language == 0 {
			diagnostics[row].Language = selection.language
		}
	}
	return compiled, diagnostics, err
}
