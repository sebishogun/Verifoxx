package result

import (
	"errors"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/sebishogun/verifoxx/internal/schema"
	"github.com/sebishogun/verifoxx/internal/truth"
)

type explainerFixture struct {
	catalog ExplanationCatalog
	batch   Batch
}

type explainerTemplatePart struct {
	text string
	op   TemplateOp
}

func explainerLiteral(text string) explainerTemplatePart {
	return explainerTemplatePart{text: text, op: TemplateOpLiteral}
}

func explainerPlaceholder(op TemplateOp) explainerTemplatePart {
	return explainerTemplatePart{op: op}
}

func appendExplainerTemplate(table *TemplateTable, maximum uint32, parts ...explainerTemplatePart) schema.TemplateID {
	table.OpStarts = append(table.OpStarts, uint32(len(table.Ops)))
	table.OpCounts = append(table.OpCounts, uint16(len(parts)))
	table.LiteralStarts = append(table.LiteralStarts, uint32(len(table.LiteralBytes)))
	table.MaxBytes = append(table.MaxBytes, maximum)
	for _, part := range parts {
		table.Ops = append(table.Ops, part.op)
		if part.op == TemplateOpLiteral {
			table.LiteralBytes = append(table.LiteralBytes, part.text...)
			table.Args = append(table.Args, uint32(len(part.text)))
		} else {
			table.Args = append(table.Args, 0)
		}
	}
	return schema.TemplateID(len(table.OpStarts))
}

func appendExplainerSymbol(catalog *ExplanationCatalog, value string) schema.SymbolID {
	catalog.SymbolStarts = append(catalog.SymbolStarts, uint32(len(catalog.SymbolBytes)))
	catalog.SymbolLengths = append(catalog.SymbolLengths, uint32(len(value)))
	catalog.SymbolBytes = append(catalog.SymbolBytes, value...)
	return schema.SymbolID(len(catalog.SymbolStarts))
}

func newExplainerFixture(t testing.TB) explainerFixture {
	t.Helper()
	var fixture explainerFixture
	catalog := &fixture.catalog
	policyName := appendExplainerSymbol(catalog, "policy")
	policyVersion := appendExplainerSymbol(catalog, "v1")
	outcomeName := appendExplainerSymbol(catalog, "Revise")
	fieldSymbol := appendExplainerSymbol(catalog, "usage_symbol")
	fieldInteger := appendExplainerSymbol(catalog, "usage_integer")
	fieldBoolean := appendExplainerSymbol(catalog, "usage_boolean")
	fieldTimestamp := appendExplainerSymbol(catalog, "usage_timestamp")
	valueSymbol := appendExplainerSymbol(catalog, "standard")
	approvalKind := appendExplainerSymbol(catalog, "approval_record")
	attestationKind := appendExplainerSymbol(catalog, "attestation")
	validState := appendExplainerSymbol(catalog, "valid")
	verifiedState := appendExplainerSymbol(catalog, "verified")
	staleState := appendExplainerSymbol(catalog, "stale")

	catalog.PolicyName = policyName
	catalog.PolicyVersion = policyVersion
	catalog.Outcomes = OutcomeTable{
		Names:      []schema.SymbolID{outcomeName},
		Precedence: []uint8{1},
		Terminal:   []bool{false},
	}
	catalog.FieldNames = []schema.SymbolID{fieldSymbol, fieldInteger, fieldBoolean, fieldTimestamp}
	catalog.FieldKinds = []schema.ValueKind{
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
		schema.ValueKindBoolean,
		schema.ValueKindTimestamp,
	}
	catalog.EvidenceKindNames = []schema.SymbolID{approvalKind, attestationKind}
	catalog.EvidenceStateNames = []schema.SymbolID{validState, verifiedState, staleState}
	catalog.RequirementIDs = []schema.RequirementID{math.MaxUint32}
	catalog.ValueKinds = []schema.ValueKind{
		schema.ValueKindSymbol,
		schema.ValueKindInteger,
		schema.ValueKindBoolean,
		schema.ValueKindTimestamp,
	}
	catalog.ValueRefs = []uint32{uint32(valueSymbol), 1, 1, 1}
	catalog.IntegerValues = []int64{-42}
	catalog.BooleanValues = []uint64{1}
	catalog.TimestampValues = []int64{1_700_000_000}
	catalog.Remediations = RemediationTable{
		Kinds: []RemediationKind{
			RemediationSetField,
			RemediationSetField,
			RemediationSetField,
			RemediationSetField,
			RemediationAddEvidence,
		},
		Fields:        []schema.FieldID{1, 2, 3, 4, 0},
		Values:        []schema.ValueID{1, 2, 3, 4, 0},
		EvidenceKinds: []schema.EvidenceKindID{0, 0, 0, 0, 2},
	}

	rationale := appendExplainerTemplate(&catalog.Templates, 128,
		explainerPlaceholder(TemplateOpPolicyName), explainerLiteral("|"),
		explainerPlaceholder(TemplateOpPolicyVersion), explainerLiteral("|"),
		explainerPlaceholder(TemplateOpRequestID), explainerLiteral("|"),
		explainerPlaceholder(TemplateOpOutcome), explainerLiteral("|"),
		explainerPlaceholder(TemplateOpRequirementID), explainerLiteral("|"),
		explainerPlaceholder(TemplateOpClauseID), explainerLiteral("|"),
		explainerPlaceholder(TemplateOpNodeID), explainerLiteral("|"),
		explainerPlaceholder(TemplateOpReason), explainerLiteral("|{literal}"),
	)
	missingIssue := appendExplainerTemplate(&catalog.Templates, 128,
		explainerLiteral("missing "), explainerPlaceholder(TemplateOpEvidenceKind),
		explainerLiteral(" needs "), explainerPlaceholder(TemplateOpRequiredEvidenceState),
		explainerLiteral(" because "), explainerPlaceholder(TemplateOpReason),
		explainerLiteral(" at "), explainerPlaceholder(TemplateOpNodeID),
	)
	presentIssue := appendExplainerTemplate(&catalog.Templates, 192,
		explainerPlaceholder(TemplateOpEvidenceID), explainerLiteral(" "),
		explainerPlaceholder(TemplateOpEvidenceKind), explainerLiteral(" actual="),
		explainerPlaceholder(TemplateOpEvidenceState), explainerLiteral(" required="),
		explainerPlaceholder(TemplateOpRequiredEvidenceState), explainerLiteral(" reason="),
		explainerPlaceholder(TemplateOpReason), explainerLiteral(" node="),
		explainerPlaceholder(TemplateOpNodeID),
	)
	uncertainty := appendExplainerTemplate(&catalog.Templates, 32,
		explainerLiteral("uncertain "), explainerPlaceholder(TemplateOpRequestID),
	)
	assumption := appendExplainerTemplate(&catalog.Templates, 64,
		explainerLiteral("assume "), explainerPlaceholder(TemplateOpPolicyName),
		explainerLiteral(" "), explainerPlaceholder(TemplateOpPolicyVersion),
		explainerLiteral(" "), explainerPlaceholder(TemplateOpRequestID),
	)
	uncertaintySecond := appendExplainerTemplate(&catalog.Templates, 32, explainerLiteral("uncertain second"))
	assumptionSecond := appendExplainerTemplate(&catalog.Templates, 32, explainerLiteral("assume second"))
	catalog.Explanations = ExplanationTable{
		RationaleTemplateIDs:   []schema.TemplateID{rationale},
		UncertaintyStarts:      []uint32{0},
		UncertaintyCounts:      []uint16{2},
		UncertaintyTemplateIDs: []schema.TemplateID{uncertainty, uncertaintySecond},
		AssumptionTemplateIDs:  []schema.TemplateID{assumption, assumptionSecond},
	}

	firstNode := schema.NodeID(math.MaxUint32 - 1)
	secondNode := schema.NodeID(math.MaxUint32)
	catalog.EvidenceIssueNodeIDs = []schema.NodeID{firstNode, secondNode}
	catalog.EvidenceIssueTemplateIDs = make([]schema.TemplateID, 2*EvidenceIssueTemplateCount)
	for row := range 2 {
		for reason := range EvidenceIssueTemplateCount {
			catalog.EvidenceIssueTemplateIDs[row*EvidenceIssueTemplateCount+reason] = presentIssue
		}
		catalog.EvidenceIssueTemplateIDs[row*EvidenceIssueTemplateCount+int(truth.ReasonMissing-1)] = missingIssue
	}
	catalog.EvidenceSourceNodes = []schema.NodeID{firstNode, secondNode}
	catalog.EvidenceInstructionIDs = []schema.InstructionID{1, 2}
	catalog.InstructionEvidenceKinds = []schema.EvidenceKindID{1, 2}
	catalog.InstructionEvidenceStates = []schema.EvidenceStateID{1, 2}

	batch := &fixture.batch
	if err := batch.Reset(1); err != nil {
		t.Fatal(err)
	}
	batch.OutcomeIDs[0] = 1
	batch.RequirementOffsets[1] = 1
	batch.RequirementIDs = append(batch.RequirementIDs, math.MaxUint32)
	batch.DriverOffsets[1] = 1
	batch.DriverRequirements = append(batch.DriverRequirements, math.MaxUint32)
	batch.DriverClauses = append(batch.DriverClauses, math.MaxUint32)
	batch.DriverNodes = append(batch.DriverNodes, secondNode)
	batch.DriverReasons = append(batch.DriverReasons, truth.ReasonMissing)
	batch.DriverExplanations = append(batch.DriverExplanations, 1)
	batch.EvidenceOffsets[1] = 1
	batch.EvidenceIDs = append(batch.EvidenceIDs, math.MaxUint32)
	batch.ReasonOffsets[1] = 2
	batch.ReasonIDs = append(batch.ReasonIDs, truth.ReasonMissing, truth.ReasonStale)
	batch.ReasonNodes = append(batch.ReasonNodes, firstNode, secondNode)
	batch.ReasonEvidenceIDs = append(batch.ReasonEvidenceIDs, 0, math.MaxUint32)
	batch.ReasonEvidenceStates = append(batch.ReasonEvidenceStates, 0, 3)
	batch.RemediationOffsets[1] = 5
	batch.RemediationIDs = append(batch.RemediationIDs, 1, 2, 3, 4, 5)
	return fixture
}

func materializedText(t testing.TB, materialized *Materialized, text TextRange) string {
	t.Helper()
	if text.Start > text.End || uint64(text.End) > uint64(len(materialized.Bytes)) {
		t.Fatalf("invalid text range %+v over %d bytes", text, len(materialized.Bytes))
	}
	return string(materialized.Bytes[text.Start:text.End])
}

func TestExplainerMaterializesCompletePolicyAuthoredResult(t *testing.T) {
	fixture := newExplainerFixture(t)
	var explainer Explainer
	if err := explainer.Bind(fixture.catalog); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	var got Materialized
	if err := explainer.Materialize(&got, &fixture.batch, 0, math.MaxUint32); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	wantRationale := "policy|v1|R4294967295|Revise|R4294967295|C4294967295|N4294967295|missing|{literal}"
	wantIssues := []string{
		"missing approval_record needs valid because missing at N4294967294",
		"E4294967295 attestation actual=stale required=verified reason=stale node=N4294967295",
	}
	wantAssumptions := []string{"assume policy v1 R4294967295", "assume second"}
	wantUncertainty := []string{"uncertain R4294967295", "uncertain second"}
	if text := materializedText(t, &got, got.Rationale); text != wantRationale {
		t.Fatalf("rationale = %q, want %q", text, wantRationale)
	}
	if string(got.Outcome) != "Revise" || got.DriverRequirementRow != 0 {
		t.Fatalf("driver metadata = outcome %q requirement row %d, want Revise/0", got.Outcome, got.DriverRequirementRow)
	}
	for _, test := range []struct {
		name   string
		ranges []TextRange
		want   []string
	}{
		{"issues", got.EvidenceIssues, wantIssues},
		{"assumptions", got.Assumptions, wantAssumptions},
		{"uncertainty", got.Uncertainty, wantUncertainty},
	} {
		if len(test.ranges) != len(test.want) {
			t.Fatalf("%s range count = %d, want %d", test.name, len(test.ranges), len(test.want))
		}
		for i := range test.ranges {
			if text := materializedText(t, &got, test.ranges[i]); text != test.want[i] {
				t.Fatalf("%s[%d] = %q, want %q", test.name, i, text, test.want[i])
			}
		}
	}

	if len(got.Remediations) != 5 {
		t.Fatalf("remediation count = %d, want 5", len(got.Remediations))
	}
	wantFields := []string{"usage_symbol", "usage_integer", "usage_boolean", "usage_timestamp"}
	wantValues := []string{"standard", "-42", "true", "1700000000"}
	wantKinds := []schema.ValueKind{schema.ValueKindSymbol, schema.ValueKindInteger, schema.ValueKindBoolean, schema.ValueKindTimestamp}
	for i := range 4 {
		remediation := got.Remediations[i]
		if remediation.Kind != RemediationSetField || remediation.ValueKind != wantKinds[i] ||
			materializedText(t, &got, remediation.FieldName) != wantFields[i] ||
			materializedText(t, &got, remediation.Value) != wantValues[i] {
			t.Fatalf("set-field remediation %d = %+v", i, remediation)
		}
	}
	addEvidence := got.Remediations[4]
	if addEvidence.Kind != RemediationAddEvidence || addEvidence.ValueKind != schema.ValueKindInvalid ||
		materializedText(t, &got, addEvidence.EvidenceKindName) != "attestation" {
		t.Fatalf("add-evidence remediation = %+v", addEvidence)
	}

	if !slices.Equal(got.Requirements, []schema.RequirementID{math.MaxUint32}) ||
		!slices.Equal(got.Evidence, []schema.EvidenceID{math.MaxUint32}) {
		t.Fatalf("zero-copy views = requirements %v evidence %v", got.Requirements, got.Evidence)
	}
	if &got.Requirements[0] != &fixture.batch.RequirementIDs[0] || &got.Evidence[0] != &fixture.batch.EvidenceIDs[0] {
		t.Fatal("requirements or evidence were copied")
	}
	wantBytes := wantRationale + wantIssues[0] + wantIssues[1] + wantAssumptions[0] + wantAssumptions[1] +
		wantUncertainty[0] + wantUncertainty[1] +
		"usage_symbolstandardusage_integer-42usage_booleantrueusage_timestamp1700000000attestation"
	if string(got.Bytes) != wantBytes {
		t.Fatalf("byte slab = %q, want %q", got.Bytes, wantBytes)
	}
}

func TestExplainerMaterializesSourceOrderedRequirementSubset(t *testing.T) {
	fixture := newExplainerFixture(t)
	fixture.catalog.RequirementIDs = []schema.RequirementID{2, 1}
	fixture.batch.RequirementIDs[0] = 1
	fixture.batch.DriverRequirements[0] = 1
	var explainer Explainer
	if err := explainer.Bind(fixture.catalog); err != nil {
		t.Fatalf("Bind source-ordered requirements: %v", err)
	}
	var got Materialized
	if err := explainer.Materialize(&got, &fixture.batch, 0, math.MaxUint32); err != nil {
		t.Fatalf("Materialize source-ordered requirement subset: %v", err)
	}
	if !slices.Equal(got.Requirements, []schema.RequirementID{1}) {
		t.Fatalf("requirements = %v, want [1]", got.Requirements)
	}
	if got.DriverRequirementRow != 1 {
		t.Fatalf("driver requirement row = %d, want 1", got.DriverRequirementRow)
	}
}

func TestExplainerSkipsFactReasonsFromEvidenceIssues(t *testing.T) {
	fixture := newExplainerFixture(t)
	fixture.batch.ReasonNodes[0] = 1
	var explainer Explainer
	if err := explainer.Bind(fixture.catalog); err != nil {
		t.Fatal(err)
	}
	var got Materialized
	if err := explainer.Materialize(&got, &fixture.batch, 0, math.MaxUint32); err != nil {
		t.Fatal(err)
	}
	if len(got.EvidenceIssues) != 1 {
		t.Fatalf("evidence issue count = %d, want 1", len(got.EvidenceIssues))
	}
	if text := materializedText(t, &got, got.EvidenceIssues[0]); text != "E4294967295 attestation actual=stale required=verified reason=stale node=N4294967295" {
		t.Fatalf("remaining evidence issue = %q", text)
	}
}

func TestExplainerReasonNamesAreStable(t *testing.T) {
	want := [...]string{
		"missing", "stale", "unclear", "unverifiable", "wrong_scope",
		"wrong_subject", "wrong_timing", "invalid", "conflict",
	}
	for row, expected := range want {
		id := schema.ReasonID(row + 1)
		if got, ok := ReasonName(id); !ok || got != expected {
			t.Fatalf("reason %d = (%q,%v), want (%q,true)", id, got, ok, expected)
		}
	}
	for _, id := range []schema.ReasonID{0, truth.ReasonConflict + 1} {
		if got, ok := ReasonName(id); ok || got != "" {
			t.Fatalf("invalid reason %d = (%q,%v)", id, got, ok)
		}
	}
}

func cloneMaterialized(source Materialized) Materialized {
	clone := source
	clone.Bytes = slices.Clone(source.Bytes)
	clone.Outcome = slices.Clone(source.Outcome)
	clone.EvidenceIssues = slices.Clone(source.EvidenceIssues)
	clone.Assumptions = slices.Clone(source.Assumptions)
	clone.Uncertainty = slices.Clone(source.Uncertainty)
	clone.Remediations = slices.Clone(source.Remediations)
	clone.Requirements = slices.Clone(source.Requirements)
	clone.Evidence = slices.Clone(source.Evidence)
	return clone
}

func poisonedMaterialized() Materialized {
	return Materialized{
		Bytes:                []byte("poison"),
		Outcome:              []byte("poison-outcome"),
		EvidenceIssues:       []TextRange{{1, 2}},
		Assumptions:          []TextRange{{2, 3}},
		Uncertainty:          []TextRange{{3, 4}},
		Remediations:         []RenderedRemediation{{Kind: RemediationAddEvidence}},
		Requirements:         []schema.RequirementID{7},
		Evidence:             []schema.EvidenceID{8},
		Rationale:            TextRange{Start: 4, End: 5},
		DriverRequirementRow: math.MaxUint32,
	}
}

func TestExplainerBindRejectsMalformedCatalogAtomically(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ExplanationCatalog)
	}{
		{"symbol range", func(catalog *ExplanationCatalog) { catalog.SymbolLengths[0]++ }},
		{"template maximum", func(catalog *ExplanationCatalog) { catalog.Templates.MaxBytes[0] = 1 }},
		{"assumption context", func(catalog *ExplanationCatalog) { catalog.Explanations.AssumptionTemplateIDs[0] = 3 }},
		{"missing issue context", func(catalog *ExplanationCatalog) { catalog.EvidenceIssueTemplateIDs[0] = 3 }},
		{"issue node order", func(catalog *ExplanationCatalog) {
			catalog.EvidenceIssueNodeIDs[0], catalog.EvidenceIssueNodeIDs[1] = catalog.EvidenceIssueNodeIDs[1], catalog.EvidenceIssueNodeIDs[0]
		}},
		{"issue template shape", func(catalog *ExplanationCatalog) {
			catalog.EvidenceIssueTemplateIDs = catalog.EvidenceIssueTemplateIDs[:len(catalog.EvidenceIssueTemplateIDs)-1]
		}},
		{"issue instruction", func(catalog *ExplanationCatalog) { catalog.EvidenceInstructionIDs[0] = 99 }},
		{"value reference", func(catalog *ExplanationCatalog) { catalog.ValueRefs[1] = 99 }},
		{"remediation type", func(catalog *ExplanationCatalog) { catalog.FieldKinds[0] = schema.ValueKindInteger }},
		{"duplicate requirement", func(catalog *ExplanationCatalog) {
			catalog.RequirementIDs = append(catalog.RequirementIDs, catalog.RequirementIDs[0])
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExplainerFixture(t)
			test.mutate(&fixture.catalog)
			var explainer Explainer
			if err := explainer.Bind(fixture.catalog); !errors.Is(err, ErrInvalidExplanationCatalog) {
				t.Fatalf("Bind error = %v, want %v", err, ErrInvalidExplanationCatalog)
			}
		})
	}

	fixture := newExplainerFixture(t)
	var explainer Explainer
	if err := explainer.Bind(fixture.catalog); err != nil {
		t.Fatalf("prime Bind: %v", err)
	}
	var before Materialized
	if err := explainer.Materialize(&before, &fixture.batch, 0, math.MaxUint32); err != nil {
		t.Fatalf("prime Materialize: %v", err)
	}
	bad := newExplainerFixture(t)
	bad.catalog.SymbolLengths[0]++
	if err := explainer.Bind(bad.catalog); !errors.Is(err, ErrInvalidExplanationCatalog) {
		t.Fatalf("failed rebind error = %v, want %v", err, ErrInvalidExplanationCatalog)
	}
	var after Materialized
	if err := explainer.Materialize(&after, &fixture.batch, 0, math.MaxUint32); err != nil {
		t.Fatalf("Materialize after failed Bind: %v", err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("failed Bind replaced usable catalog\nafter:  %+v\nbefore: %+v", after, before)
	}
	var nilExplainer *Explainer
	if err := nilExplainer.Bind(fixture.catalog); !errors.Is(err, ErrInvalidExplanationCatalog) {
		t.Fatalf("nil Bind error = %v, want %v", err, ErrInvalidExplanationCatalog)
	}
}

func TestExplainerRejectsMalformedResultAtomically(t *testing.T) {
	tests := []struct {
		name      string
		row       uint32
		requestID schema.RequestID
		mutate    func(*Batch)
	}{
		{"row", 1, math.MaxUint32, func(*Batch) {}},
		{"request ID", 0, 0, func(*Batch) {}},
		{"offset length", 0, math.MaxUint32, func(batch *Batch) {
			batch.RequirementOffsets = batch.RequirementOffsets[:1]
		}},
		{"offset range", 0, math.MaxUint32, func(batch *Batch) { batch.ReasonOffsets[1] = 3 }},
		{"driver count", 0, math.MaxUint32, func(batch *Batch) {
			batch.DriverOffsets[1] = 0
			batch.DriverRequirements = batch.DriverRequirements[:0]
			batch.DriverClauses = batch.DriverClauses[:0]
			batch.DriverNodes = batch.DriverNodes[:0]
			batch.DriverReasons = batch.DriverReasons[:0]
			batch.DriverExplanations = batch.DriverExplanations[:0]
		}},
		{"driver columns", 0, math.MaxUint32, func(batch *Batch) {
			batch.DriverExplanations = batch.DriverExplanations[:0]
		}},
		{"outcome ID", 0, math.MaxUint32, func(batch *Batch) { batch.OutcomeIDs[0] = 0 }},
		{"requirement ID", 0, math.MaxUint32, func(batch *Batch) { batch.DriverRequirements[0] = 1 }},
		{"applied requirement ID", 0, math.MaxUint32, func(batch *Batch) { batch.RequirementIDs[0] = 1 }},
		{"clause ID", 0, math.MaxUint32, func(batch *Batch) { batch.DriverClauses[0] = 0 }},
		{"node ID", 0, math.MaxUint32, func(batch *Batch) { batch.DriverNodes[0] = 0 }},
		{"explanation ID", 0, math.MaxUint32, func(batch *Batch) { batch.DriverExplanations[0] = 0 }},
		{"driver reason", 0, math.MaxUint32, func(batch *Batch) { batch.DriverReasons[0] = truth.ReasonConflict + 1 }},
		{"reason columns", 0, math.MaxUint32, func(batch *Batch) { batch.ReasonNodes = batch.ReasonNodes[:1] }},
		{"reason order", 0, math.MaxUint32, func(batch *Batch) {
			batch.ReasonIDs[0], batch.ReasonIDs[1] = batch.ReasonIDs[1], batch.ReasonIDs[0]
		}},
		{"missing invented evidence", 0, math.MaxUint32, func(batch *Batch) {
			batch.ReasonEvidenceIDs[0] = math.MaxUint32
			batch.ReasonEvidenceStates[0] = 1
		}},
		{"present missing evidence", 0, math.MaxUint32, func(batch *Batch) {
			batch.ReasonEvidenceIDs[1] = 0
			batch.ReasonEvidenceStates[1] = 0
		}},
		{"evidence state", 0, math.MaxUint32, func(batch *Batch) { batch.ReasonEvidenceStates[1] = 4 }},
		{"causal evidence", 0, math.MaxUint32, func(batch *Batch) { batch.ReasonEvidenceIDs[1]-- }},
		{"evidence view", 0, math.MaxUint32, func(batch *Batch) { batch.EvidenceIDs[0] = 0 }},
		{"remediation ID", 0, math.MaxUint32, func(batch *Batch) { batch.RemediationIDs[0] = 99 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExplainerFixture(t)
			var explainer Explainer
			if err := explainer.Bind(fixture.catalog); err != nil {
				t.Fatalf("Bind: %v", err)
			}
			test.mutate(&fixture.batch)
			dst := poisonedMaterialized()
			want := cloneMaterialized(dst)
			if err := explainer.Materialize(&dst, &fixture.batch, test.row, test.requestID); !errors.Is(err, ErrInvalidExplanationResult) {
				t.Fatalf("Materialize error = %v, want %v", err, ErrInvalidExplanationResult)
			}
			if !reflect.DeepEqual(dst, want) {
				t.Fatalf("failed Materialize mutated destination\ngot:  %+v\nwant: %+v", dst, want)
			}
		})
	}

	fixture := newExplainerFixture(t)
	var bound Explainer
	if err := bound.Bind(fixture.catalog); err != nil {
		t.Fatal(err)
	}
	var unbound Explainer
	var nilExplainer *Explainer
	var dst Materialized
	for name, call := range map[string]func() error{
		"unbound":   func() error { return unbound.Materialize(&dst, &fixture.batch, 0, math.MaxUint32) },
		"nil":       func() error { return nilExplainer.Materialize(&dst, &fixture.batch, 0, math.MaxUint32) },
		"nil dst":   func() error { return bound.Materialize(nil, &fixture.batch, 0, math.MaxUint32) },
		"nil batch": func() error { return bound.Materialize(&dst, nil, 0, math.MaxUint32) },
	} {
		want := ErrInvalidExplanationResult
		if name == "unbound" || name == "nil" {
			want = ErrInvalidExplanationCatalog
		}
		if err := call(); !errors.Is(err, want) {
			t.Fatalf("%s error = %v, want %v", name, err, want)
		}
	}
}

func boundedExplainerFixture(t testing.TB, issue bool) explainerFixture {
	t.Helper()
	var fixture explainerFixture
	catalog := &fixture.catalog
	catalog.PolicyName = appendExplainerSymbol(catalog, "p")
	catalog.PolicyVersion = appendExplainerSymbol(catalog, "v")
	outcome := appendExplainerSymbol(catalog, "Approve")
	catalog.Outcomes = OutcomeTable{Names: []schema.SymbolID{outcome}, Precedence: []uint8{1}, Terminal: []bool{true}}
	catalog.RequirementIDs = []schema.RequirementID{1}
	for range 4 {
		appendExplainerTemplate(&catalog.Templates, MaxRenderedTemplateBytes, explainerLiteral(string(make([]byte, MaxRenderedTemplateBytes))))
	}
	catalog.Explanations = ExplanationTable{
		RationaleTemplateIDs:  []schema.TemplateID{1},
		UncertaintyStarts:     []uint32{0},
		UncertaintyCounts:     []uint16{0},
		AssumptionTemplateIDs: []schema.TemplateID{2, 3, 4},
	}
	batch := &fixture.batch
	if err := batch.Reset(1); err != nil {
		t.Fatal(err)
	}
	batch.OutcomeIDs[0] = 1
	batch.RequirementOffsets[1] = 1
	batch.RequirementIDs = append(batch.RequirementIDs, 1)
	batch.DriverOffsets[1] = 1
	batch.DriverRequirements = append(batch.DriverRequirements, 1)
	batch.DriverClauses = append(batch.DriverClauses, 1)
	batch.DriverNodes = append(batch.DriverNodes, 1)
	batch.DriverReasons = append(batch.DriverReasons, 0)
	batch.DriverExplanations = append(batch.DriverExplanations, 1)
	if issue {
		kind := appendExplainerSymbol(catalog, "kind")
		state := appendExplainerSymbol(catalog, "state")
		catalog.EvidenceKindNames = []schema.SymbolID{kind}
		catalog.EvidenceStateNames = []schema.SymbolID{state}
		issueTemplate := appendExplainerTemplate(&catalog.Templates, 1, explainerLiteral("x"))
		catalog.EvidenceIssueNodeIDs = []schema.NodeID{1}
		catalog.EvidenceIssueTemplateIDs = make([]schema.TemplateID, EvidenceIssueTemplateCount)
		for i := range catalog.EvidenceIssueTemplateIDs {
			catalog.EvidenceIssueTemplateIDs[i] = issueTemplate
		}
		catalog.EvidenceSourceNodes = []schema.NodeID{1}
		catalog.EvidenceInstructionIDs = []schema.InstructionID{1}
		catalog.InstructionEvidenceKinds = []schema.EvidenceKindID{1}
		catalog.InstructionEvidenceStates = []schema.EvidenceStateID{1}
		batch.DriverReasons[0] = truth.ReasonMissing
		batch.ReasonOffsets[1] = 1
		batch.ReasonIDs = append(batch.ReasonIDs, truth.ReasonMissing)
		batch.ReasonNodes = append(batch.ReasonNodes, 1)
		batch.ReasonEvidenceIDs = append(batch.ReasonEvidenceIDs, 0)
		batch.ReasonEvidenceStates = append(batch.ReasonEvidenceStates, 0)
	}
	return fixture
}

func TestExplainerEnforcesCompleteOutputBoundAtomically(t *testing.T) {
	exact := boundedExplainerFixture(t, false)
	var explainer Explainer
	if err := explainer.Bind(exact.catalog); err != nil {
		t.Fatalf("Bind exact: %v", err)
	}
	var dst Materialized
	if err := explainer.Materialize(&dst, &exact.batch, 0, 1); err != nil {
		t.Fatalf("Materialize exact: %v", err)
	}
	if len(dst.Bytes) != MaxRenderedExplanationBytes {
		t.Fatalf("exact output bytes = %d, want %d", len(dst.Bytes), MaxRenderedExplanationBytes)
	}

	over := boundedExplainerFixture(t, true)
	if err := explainer.Bind(over.catalog); err != nil {
		t.Fatalf("Bind overflow catalog: %v", err)
	}
	dst = poisonedMaterialized()
	want := cloneMaterialized(dst)
	if err := explainer.Materialize(&dst, &over.batch, 0, 1); !errors.Is(err, ErrExplanationTooLarge) {
		t.Fatalf("overflow Materialize error = %v, want %v", err, ErrExplanationTooLarge)
	}
	if !reflect.DeepEqual(dst, want) {
		t.Fatal("oversized Materialize mutated destination")
	}
}

func TestExplainerPoisonedReuseIsDeterministicAndAllocationFree(t *testing.T) {
	fixture := newExplainerFixture(t)
	var explainer Explainer
	if err := explainer.Bind(fixture.catalog); err != nil {
		t.Fatal(err)
	}
	var dst Materialized
	if err := explainer.Materialize(&dst, &fixture.batch, 0, math.MaxUint32); err != nil {
		t.Fatal(err)
	}
	want := cloneMaterialized(dst)
	for i := range dst.Bytes {
		dst.Bytes[i] = 0xff
	}
	for i := range dst.EvidenceIssues {
		dst.EvidenceIssues[i] = TextRange{math.MaxUint32, math.MaxUint32}
	}
	for i := range dst.Remediations {
		dst.Remediations[i] = RenderedRemediation{Kind: RemediationInvalid}
	}
	if err := explainer.Materialize(&dst, &fixture.batch, 0, math.MaxUint32); err != nil {
		t.Fatalf("poisoned reuse: %v", err)
	}
	if !reflect.DeepEqual(dst, want) {
		t.Fatalf("poisoned reuse differs\ngot:  %+v\nwant: %+v", dst, want)
	}

	var materializeErr error
	if allocs := testing.AllocsPerRun(100, func() {
		materializeErr = explainer.Materialize(&dst, &fixture.batch, 0, math.MaxUint32)
	}); allocs != 0 {
		t.Fatalf("warm Materialize allocations = %g, want 0", allocs)
	}
	if materializeErr != nil {
		t.Fatal(materializeErr)
	}
}
