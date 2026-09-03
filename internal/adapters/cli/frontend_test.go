package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sebishogun/nornrune/frontend"
	"github.com/sebishogun/nornrune/internal/fixtures"
	nornrune "github.com/sebishogun/nornrune/policies/nornrune"
)

const (
	celPolicy  = `team == "external_partner"`
	regoPolicy = `package nornrune

allow if { input.team == "external_partner" }`
	cedarPolicy = `permit(principal, action, resource) when { context.team == "external_partner" };`

	celBindings      = `{"name":"nornrune","version":"v1","fields":[{"source":"team","target":"requester.team","kind":"string","group":"subject"}]}`
	regoBindings     = `{"name":"nornrune","version":"v1","decision":"allow","fields":[{"source":"input.team","target":"requester.team","kind":"string","group":"subject"}]}`
	cedarBindings    = `{"name":"nornrune","version":"v1","fields":[{"source":"context.team","target":"requester.team","kind":"string","group":"subject"}]}`
	frontendRequests = `{"schema_version":1,"pack":"nornrune","requests":[{"id":"R1","requester":{"team":"external_partner"}},{"id":"R2","requester":{"team":"internal_team"}}]}`
	frontendEvidence = `{"schema_version":1,"pack":"nornrune","evidence":[]}`
)

func frontendTestDependencies() dependencies {
	files := map[string]string{
		"native.policy": nornrune.Source(),
		"cel.policy":    celPolicy, "cel.bindings": celBindings,
		"rego.policy": regoPolicy, "rego.bindings": regoBindings,
		"cedar.policy": cedarPolicy, "cedar.bindings": cedarBindings,
		"requests.json": fixtures.RequestsJSON(), "evidence.json": fixtures.EvidenceJSON(),
		"frontend-requests.json": frontendRequests, "frontend-evidence.json": frontendEvidence,
		"bad-cel.policy": `team + 1`,
	}
	return dependencies{
		readFile: func(path string) ([]byte, error) {
			value, ok := files[path]
			if !ok {
				return nil, errors.New("missing file")
			}
			return []byte(value), nil
		},
		readBoundedFile: func(path string, maxBytes uint32) ([]byte, error) {
			value, ok := files[path]
			if !ok {
				return nil, errors.New("missing file")
			}
			if uint64(len(value)) > uint64(maxBytes) {
				return nil, errors.New("file exceeds limit")
			}
			return []byte(value), nil
		},
		policy: nornrune.Source(), requests: fixtures.RequestsJSON(), evidence: fixtures.EvidenceJSON(), version: "test-engine",
	}
}

func TestExplicitNativeFormatMatchesOmittedFormat(t *testing.T) {
	deps := frontendTestDependencies()
	for _, command := range []string{"compile", "validate", "evaluate"} {
		code, stdout, stderr := runCLIWithDependencies(t, deps, command)
		explicitCode, explicitStdout, explicitStderr := runCLIWithDependencies(t, deps, command, "--format", "native")
		if explicitCode != code || explicitStdout != stdout || explicitStderr != stderr {
			t.Errorf("%s --format native = (%d,%q,%q), omitted = (%d,%q,%q)", command, explicitCode, explicitStdout, explicitStderr, code, stdout, stderr)
		}

		code, stdout, stderr = runCLIWithDependencies(t, deps, command, "--help")
		explicitCode, explicitStdout, explicitStderr = runCLIWithDependencies(t, deps, command, "--format", "native", "--help")
		if explicitCode != code || explicitStdout != stdout || explicitStderr != stderr {
			t.Errorf("%s explicit native help differs from omitted format", command)
		}
	}
}

func TestFrontendFormatsRequireExplicitPolicyAndBindings(t *testing.T) {
	deps := frontendTestDependencies()
	for _, format := range []string{"cel", "rego", "cedar"} {
		for _, args := range [][]string{
			{"validate", "--format", format},
			{"validate", "--format", format, "--policy", format + ".policy"},
			{"validate", "--format", format, "--bindings", format + ".bindings"},
		} {
			code, stdout, stderr := runCLIWithDependencies(t, deps, args...)
			if code != 2 || stdout != "" || !strings.Contains(stderr, "Usage:") {
				t.Errorf("Execute(%q) = (%d,%q,%q), want usage error", args, code, stdout, stderr)
			}
		}
	}
}

func TestFrontendFlagsRejectInvalidSelections(t *testing.T) {
	deps := frontendTestDependencies()
	tests := [][]string{
		{"compile", "--format", "native", "--bindings", "cel.bindings"},
		{"compile", "--format", "unknown"},
		{"compile", "--format", "protobuf", "--policy", "cel.policy", "--bindings", "cel.bindings"},
		{"bench", "--format", "cel"},
	}
	for _, args := range tests {
		code, stdout, stderr := runCLIWithDependencies(t, deps, args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "Usage:") {
			t.Errorf("Execute(%q) = (%d,%q,%q), want usage error", args, code, stdout, stderr)
		}
	}
}

func TestSQLFormatIsRejectedBeforeReadingInputs(t *testing.T) {
	reads := 0
	deps := frontendTestDependencies()
	deps.readFile = func(string) ([]byte, error) {
		reads++
		return nil, errors.New("unexpected read")
	}
	deps.readBoundedFile = func(string, uint32) ([]byte, error) {
		reads++
		return nil, errors.New("unexpected read")
	}
	code, stdout, stderr := runCLIWithDependencies(t, deps,
		"compile", "--format", "sql", "--policy", "policy.sql", "--bindings", "bindings.json")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "unsupported policy format: sql") || reads != 0 {
		t.Fatalf("SQL format = (%d,%q,%q), reads %d, want early usage error", code, stdout, stderr, reads)
	}
}

func TestFrontendInputsAllowOnlyOneStdinReader(t *testing.T) {
	deps := frontendTestDependencies()
	for _, args := range [][]string{
		{"evaluate", "--format", "cel", "--policy", "-", "--bindings", "-"},
		{"evaluate", "--format", "cel", "--policy", "cel.policy", "--requests", "-", "--bindings", "-"},
	} {
		var stdout, stderr bytes.Buffer
		code := executeWithDependencies(args, strings.NewReader(celPolicy), &stdout, &stderr, deps)
		if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "only one input") {
			t.Fatalf("multiple stdin %q = (%d,%q,%q), want usage error", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestDecodeBindingsRejectsMalformedDeclarations(t *testing.T) {
	limits := frontend.DefaultLimits()
	tests := []struct {
		name   string
		source string
	}{
		{name: "unknown field", source: `{"name":"p","version":"v1","extra":true,"fields":[]}`},
		{name: "duplicate root field", source: `{"name":"p","name":"q","version":"v1","fields":[]}`},
		{name: "duplicate binding field", source: `{"name":"p","version":"v1","fields":[{"source":"team","source":"other","target":"requester.team","kind":"string","group":"subject"}]}`},
		{name: "trailing JSON", source: `{"name":"p","version":"v1","fields":[]} {}`},
		{name: "unsupported enum", source: `{"name":"p","version":"v1","fields":[{"source":"team","target":"requester.team","kind":"float","group":"subject"}]}`},
		{name: "duplicate source", source: `{"name":"p","version":"v1","fields":[{"source":"team","target":"requester.team","kind":"string","group":"subject"},{"source":"team","target":"requester.trust","kind":"string","group":"subject"}]}`},
		{name: "oversized", source: strings.Repeat(" ", int(limits.MaxSourceBytes)+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeBindings([]byte(test.source), limits); err == nil {
				t.Fatal("decodeBindings accepted malformed declarations")
			}
		})
	}
}

func TestLoadBindingSourcePassesLimitToFileReader(t *testing.T) {
	const limit uint32 = 17
	var gotPath string
	var gotLimit uint32
	source, err := loadBindingSource("bindings.json", nil, func(path string, maxBytes uint32) ([]byte, error) {
		gotPath, gotLimit = path, maxBytes
		return []byte(`{}`), nil
	}, limit)
	if err != nil || string(source) != `{}` || gotPath != "bindings.json" || gotLimit != limit {
		t.Fatalf("loadBindingSource = (%q,%v,%q,%d)", source, err, gotPath, gotLimit)
	}
}

func TestExplicitFrontendsCompileValidateAndEvaluate(t *testing.T) {
	deps := frontendTestDependencies()
	for _, format := range []string{"cel", "rego", "cedar"} {
		t.Run(format, func(t *testing.T) {
			common := []string{"--format", format, "--policy", format + ".policy", "--bindings", format + ".bindings"}
			for _, command := range []string{"compile", "validate", "evaluate"} {
				args := append([]string{command}, common...)
				if command == "evaluate" {
					args = append(args, "--requests", "frontend-requests.json", "--evidence", "frontend-evidence.json")
				}
				code, stdout, stderr := runCLIWithDependencies(t, deps, args...)
				if code != 0 || stderr != "" {
					t.Fatalf("Execute(%q) = (%d,%q,%q), want success", args, code, stdout, stderr)
				}
				if command == "compile" && !strings.Contains(stdout, `"name":"nornrune"`) {
					t.Fatalf("compile output = %q", stdout)
				}
				if command == "validate" && stdout != "{\"valid\":true,\"diagnostics\":[]}\n" {
					t.Fatalf("validate output = %q", stdout)
				}
				if command == "evaluate" && (!strings.Contains(stdout, `"request_id": "R1"`) || !strings.Contains(stdout, `"decision": "Approve"`)) {
					t.Fatalf("evaluate output = %q", stdout)
				}
			}
		})
	}
}

func TestFrontendDiagnosticsAreStructuredAndQuiet(t *testing.T) {
	code, stdout, stderr := runCLIWithDependencies(t, frontendTestDependencies(),
		"validate", "--format", "cel", "--policy", "bad-cel.policy", "--bindings", "cel.bindings")
	if code != 1 || stderr != "" {
		t.Fatalf("validate diagnostic = (%d,%q,%q), want quiet status 1", code, stdout, stderr)
	}
	for _, field := range []string{`"language":"cel"`, `"code":`, `"span":{"start":`, `"row":`, `"field":`} {
		if !strings.Contains(stdout, field) {
			t.Errorf("diagnostic output %q lacks %q", stdout, field)
		}
	}
}

func TestFrontendContentIsNeverAutoDetected(t *testing.T) {
	code, stdout, stderr := runCLIWithDependencies(t, frontendTestDependencies(), "compile", "--policy", "cel.policy")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "decode policy") {
		t.Fatalf("native CEL input = (%d,%q,%q), want native decode failure", code, stdout, stderr)
	}
}
