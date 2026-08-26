package jsonpolicy

import (
	"strings"
	"testing"

	"github.com/sebishogun/nornrune/internal/ast"
	"github.com/sebishogun/nornrune/internal/schema"
)

func testResolutionBranch(outcome, rationale, uncertainty string) string {
	return `{"outcome":"` + outcome + `","explanation":{"rationale":"` + rationale + `","uncertainty":` + uncertainty + `}}`
}

func testCompleteResolution() string {
	return `{"satisfied":` + testResolutionBranch("Approve", "Satisfied {outcome} for {request_id}.", `[]`) +
		`,"false":` + testResolutionBranch("Reject", "False branch for {requirement_id}.", `[]`) +
		`,"missing":` + testResolutionBranch("Escalate", "Missing because {reason}.", `["Supply evidence for {node_id}."]`) +
		`,"stale":` + testResolutionBranch("Escalate", "Stale because {reason}.", `[]`) +
		`,"unclear":` + testResolutionBranch("Escalate", "Unclear because {reason}.", `[]`) +
		`,"unverifiable":` + testResolutionBranch("Escalate", "Unverifiable because {reason}.", `[]`) +
		`,"conflict":` + testResolutionBranch("Escalate", "Conflict because {reason}.", `[]`) + `}`
}

func testEvidenceExpression() string {
	return `{"op":"evidence_matches","kind":"approval_record","state":"current",` +
		`"explanation":{"issue":"{evidence_kind} is {reason}.",` +
		`"conflict":"{evidence_id} has {evidence_state}."}}`
}

func testExplanationPolicy(assumptions, evidence, resolution string) string {
	requirement := `{"id":"R1","source":"source",` +
		`"applies":{"op":"exists","field":"context.environment"},` +
		`"clauses":[{"assert":{"op":"exists","field":"context.environment"},` +
		`"evidence":[` + evidence + `],"resolution":` + resolution + `,"remediations":[]}]}`
	return `{"schema_version":1,"name":"p","version":"1",` +
		`"assumptions":` + assumptions + `,` +
		`"evidence_kinds":[{"name":"approval_record"}],` +
		`"evidence_states":[{"name":"current"}],` + outcomeCatalog +
		`,"requirements":[` + requirement + `]}`
}

func TestDecodePolicyAuthoredExplanationsIntoTypedAST(t *testing.T) {
	source := testExplanationPolicy(
		`["Policy {policy_name}.","Request {request_id}."]`,
		testEvidenceExpression(),
		testCompleteResolution(),
	)
	b := decodePolicy(t, []byte(source), Limits{})
	d := b.Document()

	assumptions, ok := d.Assumptions()
	if !ok || len(assumptions) != 2 {
		t.Fatalf("Assumptions = (%v, %v)", assumptions, ok)
	}
	for _, id := range assumptions {
		if d.TemplateContexts[id-1] != ast.TemplateContextAssumption {
			t.Fatalf("assumption %d context = %v", id, d.TemplateContexts[id-1])
		}
	}

	clauses, ok := d.RequirementClauses(1)
	if !ok || len(clauses) != 1 {
		t.Fatalf("RequirementClauses = (%v, %v)", clauses, ok)
	}
	_, resolution, ok := d.Clause(clauses[0])
	if !ok || resolution.OnSatisfied != 1 || resolution.OnFalse != 2 || resolution.OnMissing != 4 {
		t.Fatalf("resolution outcomes = (%+v, %v)", resolution, ok)
	}
	decision := [...]schema.ExplanationID{resolution.OnSatisfiedExplanation, resolution.OnFalseExplanation}
	for _, id := range decision {
		rationale, _, found := d.Explanation(id)
		if !found || d.TemplateContexts[rationale-1] != ast.TemplateContextDecision {
			t.Fatalf("decision explanation %d = rationale %d found %v", id, rationale, found)
		}
	}
	unresolved := [...]schema.ExplanationID{
		resolution.OnMissingExplanation,
		resolution.OnStaleExplanation,
		resolution.OnUnclearExplanation,
		resolution.OnUnverifiableExplanation,
		resolution.OnConflictExplanation,
	}
	for _, id := range unresolved {
		rationale, _, found := d.Explanation(id)
		if !found || d.TemplateContexts[rationale-1] != ast.TemplateContextUnresolved {
			t.Fatalf("unresolved explanation %d = rationale %d found %v", id, rationale, found)
		}
	}
	_, uncertainty, _ := d.Explanation(resolution.OnMissingExplanation)
	if len(uncertainty) != 1 {
		t.Fatalf("missing uncertainty = %v", uncertainty)
	}

	evidence, ok := d.ClauseEvidence(clauses[0])
	if !ok || len(evidence) != 1 {
		t.Fatalf("ClauseEvidence = (%v, %v)", evidence, ok)
	}
	issues, ok := d.EvidenceIssueTemplates(evidence[0])
	if !ok || len(issues) != ast.EvidenceIssueReasonCount {
		t.Fatalf("EvidenceIssueTemplates = (%v, %v)", issues, ok)
	}
	if issues[ast.EvidenceIssueMissing] != issues[ast.EvidenceIssueStale] {
		t.Fatal("fallback issue was not expanded in fixed reason order")
	}
	if issues[ast.EvidenceIssueConflict] == issues[ast.EvidenceIssueMissing] {
		t.Fatal("conflict override did not replace the fallback")
	}
	if d.TemplateContexts[issues[ast.EvidenceIssueMissing]-1] != ast.TemplateContextEvidenceMissing ||
		d.TemplateContexts[issues[ast.EvidenceIssueConflict]-1] != ast.TemplateContextEvidencePresent {
		t.Fatal("evidence templates used the wrong binding contexts")
	}
}

func TestDecodePolicyAuthoredExplanationKeyOrderIsFree(t *testing.T) {
	evidence := `{"explanation":{"conflict":"{evidence_id}","issue":"{evidence_kind}"},` +
		`"state":"current","kind":"approval_record","op":"evidence_matches"}`
	branch := `{"explanation":{"uncertainty":[],"rationale":"ok"},"outcome":"Approve"}`
	resolution := `{"conflict":` + strings.Replace(branch, `"Approve"`, `"Escalate"`, 1) +
		`,"unverifiable":` + strings.Replace(branch, `"Approve"`, `"Escalate"`, 1) +
		`,"unclear":` + strings.Replace(branch, `"Approve"`, `"Escalate"`, 1) +
		`,"stale":` + strings.Replace(branch, `"Approve"`, `"Escalate"`, 1) +
		`,"missing":` + strings.Replace(branch, `"Approve"`, `"Escalate"`, 1) +
		`,"false":` + strings.Replace(branch, `"Approve"`, `"Reject"`, 1) +
		`,"satisfied":` + branch + `}`
	source := testExplanationPolicy(`[]`, evidence, resolution)
	decodePolicy(t, []byte(source), Limits{})
}

func TestDecodePolicyAuthoredExplanationsRejectInvalidShapes(t *testing.T) {
	validEvidence := testEvidenceExpression()
	validResolution := testCompleteResolution()
	valid := testExplanationPolicy(`[]`, validEvidence, validResolution)
	tests := []struct {
		name string
		src  string
		code ErrorCode
	}{
		{"missing assumptions", strings.Replace(valid, `"assumptions":[],`, "", 1), CodeMissingKey},
		{"assumptions not array", strings.Replace(valid, `"assumptions":[]`, `"assumptions":{}`, 1), CodeInvalidType},
		{"missing evidence explanation", testExplanationPolicy(`[]`, `{"op":"evidence_matches","kind":"approval_record","state":"current"}`, validResolution), CodeInvalidArity},
		{"evidence explanation missing issue", testExplanationPolicy(`[]`, strings.Replace(validEvidence, `"issue":"{evidence_kind} is {reason}.",`, "", 1), validResolution), CodeMissingKey},
		{"evidence explanation unknown key", testExplanationPolicy(`[]`, strings.Replace(validEvidence, `"issue":`, `"other":"x","issue":`, 1), validResolution), CodeUnknownKey},
		{"evidence explanation duplicate issue", testExplanationPolicy(`[]`, strings.Replace(validEvidence, `"issue":`, `"issue":"x","issue":`, 1), validResolution), CodeDuplicateKey},
		{"old resolution string", strings.Replace(valid, `"satisfied":`+testResolutionBranch("Approve", "Satisfied {outcome} for {request_id}.", `[]`), `"satisfied":"Approve"`, 1), CodeInvalidType},
		{"branch missing outcome", strings.Replace(valid, `"outcome":"Approve",`, "", 1), CodeMissingKey},
		{"branch missing explanation", strings.Replace(valid, `,"explanation":{"rationale":"Satisfied {outcome} for {request_id}.","uncertainty":[]}`, "", 1), CodeMissingKey},
		{"explanation missing rationale", strings.Replace(valid, `"rationale":"Satisfied {outcome} for {request_id}.",`, "", 1), CodeMissingKey},
		{"explanation missing uncertainty", strings.Replace(valid, `,"uncertainty":[]`, "", 1), CodeMissingKey},
		{"unknown branch key", strings.Replace(valid, `"outcome":"Approve",`, `"other":1,"outcome":"Approve",`, 1), CodeUnknownKey},
		{"duplicate branch outcome", strings.Replace(valid, `"outcome":"Approve",`, `"outcome":"Approve","outcome":"Approve",`, 1), CodeDuplicateKey},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rejectPolicy(t, tt.src, Limits{}, tt.code)
		})
	}
}

func TestDecodePolicyAuthoredExplanationsRejectTemplateErrorsAtString(t *testing.T) {
	valid := testExplanationPolicy(`[]`, testEvidenceExpression(), testCompleteResolution())
	tests := []struct {
		name string
		old  string
		bad  string
		code ErrorCode
	}{
		{"malformed", "Satisfied {outcome} for {request_id}.", "bad {", CodeMalformed},
		{"assumption context", `[]`, `["{outcome}"]`, CodeMalformed},
		{"decision context", "Satisfied {outcome} for {request_id}.", "{reason}", CodeMalformed},
		{"fallback evidence id", "{evidence_kind} is {reason}.", "{evidence_id}", CodeMalformed},
		{"byte limit", "Satisfied {outcome} for {request_id}.", strings.Repeat("x", ast.MaxTemplateBytes+1), CodeLimit},
		{"operation limit", "Satisfied {outcome} for {request_id}.", strings.Repeat("{request_id}x", ast.MaxTemplateOps/2) + "{request_id}", CodeLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := strings.Replace(valid, tt.old, tt.bad, 1)
			je := rejectPolicy(t, source, Limits{}, tt.code)
			want := strings.Index(source, `"`+tt.bad+`"`)
			if want >= 0 && je.Offset != want {
				t.Fatalf("error offset = %d, want template string at %d", je.Offset, want)
			}
		})
	}
}

func TestDecodePolicyAuthoredExplanationLimits(t *testing.T) {
	valid := testExplanationPolicy(`[]`, testEvidenceExpression(), testCompleteResolution())
	tooManyAssumptions := testExplanationPolicy(`["a","a","a","a","a","a","a","a","a"]`, testEvidenceExpression(), testCompleteResolution())
	rejectPolicy(t, tooManyAssumptions, Limits{}, CodeLimit)
	twoAssumptions := strings.Replace(valid, `"assumptions":[]`, `"assumptions":["a","b"]`, 1)
	rejectPolicy(t, twoAssumptions, Limits{MaxAssumptions: 1}, CodeLimit)

	tooManyUncertainty := strings.Replace(valid, `"uncertainty":[]`, `"uncertainty":["a","a","a","a","a","a","a","a","a"]`, 1)
	rejectPolicy(t, tooManyUncertainty, Limits{}, CodeLimit)
	rejectPolicy(t, strings.Replace(valid, `"uncertainty":[]`, `"uncertainty":["a","b"]`, 1), Limits{MaxUncertainty: 1}, CodeLimit)

	shortTemplate := strings.Replace(valid, "Satisfied {outcome} for {request_id}.", "12345", 1)
	rejectPolicy(t, shortTemplate, Limits{MaxTemplateBytes: 4}, CodeLimit)
}
