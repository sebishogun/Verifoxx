package doccheck_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestFrontendCapabilityDocumentationIsComplete(t *testing.T) {
	document := readDocument(t, "docs/development.md")
	required := []string{
		"cel.dev/cel-go v0.32.0", "github.com/open-policy-agent/opa v1.19.1",
		"github.com/cedar-policy/cedar-go v1.8.0", "google.golang.org/protobuf v1.36.12",
		"boolean_literals", "scalar_variables", "object_selection", "scalar_comparisons", "logical_operators",
		"constant_list_membership", "function_calls", "macros_and_comprehensions", "maps_messages_and_optionals",
		"rego_v1_modules", "complete_boolean_decisions", "boolean_defaults", "multiple_rules", "conjunctive_bodies",
		"static_input_references", "constant_membership", "presence_aware_negation", "imports_and_data",
		"functions_and_recursion", "variables_and_comprehensions", "with_and_unsupported_builtins",
		"static_permit_forbid", "equality_scopes", "context_scalar_conditions", "boolean_composition",
		"forbid_precedence", "entity_hierarchy_and_attributes", "sets_records_and_extensions", "templates_and_annotations",
	}
	for _, value := range required {
		if !strings.Contains(document, value) {
			t.Errorf("docs/development.md does not contain %q", value)
		}
	}
}

func TestEvaluatorCoreDoesNotImportFrontendDependencies(t *testing.T) {
	packages := []string{"eval", "scheduler", "program", "result", "truth", "index"}
	forbidden := []string{
		"github.com/sebishogun/verifoxx/frontend", "cel.dev/cel-go",
		"github.com/open-policy-agent/opa", "github.com/cedar-policy/cedar-go",
		"google.golang.org/protobuf",
	}
	for _, name := range packages {
		directory := filepath.Join(repositoryRoot, "internal", name)
		parsed, err := parser.ParseDir(token.NewFileSet(), directory, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse imports for internal/%s: %v", name, err)
		}
		for _, pkg := range parsed {
			for filename, file := range pkg.Files {
				for _, spec := range file.Imports {
					path, err := strconv.Unquote(spec.Path.Value)
					if err != nil {
						t.Fatalf("unquote import %s in %s: %v", spec.Path.Value, filename, err)
					}
					for _, prefix := range forbidden {
						if strings.HasPrefix(path, prefix) {
							t.Errorf("%s imports forbidden frontend dependency %s", filename, path)
						}
					}
				}
			}
		}
	}
}

func TestCIHasBoundedFrontendLane(t *testing.T) {
	workflow := readDocument(t, ".github/workflows/ci.yml")
	for _, required := range []string{
		"frontend:", "name: Compatibility frontends", "timeout-minutes:",
		"timeout 240s go test -count=1 -timeout 210s ./frontend/... ./internal/frontend ./internal/doccheck",
		"timeout 120s go test -count=1 -timeout 90s -run '^Fuzz(Compile|SemanticPolicy)$' ./frontend/cel ./frontend/rego ./frontend/cedar ./internal/frontend",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf(".github/workflows/ci.yml does not contain %q", required)
		}
	}
}

func TestFrontendGuideDocumentsTheBoundedContract(t *testing.T) {
	document := readDocument(t, "docs/frontends.md")
	required := []string{
		"not drop-in replacements", "--format cel", "--format rego", "--format cedar", "--bindings",
		`"source"`, `"target"`, `"kind"`, `"group"`,
		"True", "False", "Missing", "Default", "forbid precedence",
		"4 MiB", "65,536", "128", "4,096", "131,072", "1 MiB",
		"cel.dev/cel-go v0.32.0", "github.com/open-policy-agent/opa v1.19.1",
		"github.com/cedar-policy/cedar-go v1.8.0", "google.golang.org/protobuf v1.36.12",
		"Supported", "Restricted", "Rejected", "no fixed performance claim",
		"proto:gen", "proto:check", "protoc-gen-verifoxx", "canonical_target",
		"repeated", "oneof", "optional", "nested messages", "maps", "enums", "bytes", "floating-point",
		"compile-time", "shared evaluator kernels", "native JSON",
		"differential corpus", "UTF-8 byte offsets", "runtime Protobuf",
		"OPA undefined", "non-empty constant",
	}
	for _, value := range required {
		if !strings.Contains(document, value) {
			t.Errorf("docs/frontends.md does not contain %q", value)
		}
	}
}

func TestFrontendGuideIsLinkedAtSystemBoundaries(t *testing.T) {
	for _, file := range []string{"README.md", "docs/architecture.md", "docs/development.md", "docs/operations.md"} {
		document := readDocument(t, file)
		if !strings.Contains(document, "frontends.md") {
			t.Errorf("%s does not link to frontends.md", file)
		}
	}
}
