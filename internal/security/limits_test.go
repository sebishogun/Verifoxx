package security

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestDefaultLimitsAreValidAndBounded(t *testing.T) {
	t.Parallel()

	limits := DefaultLimits()
	if err := limits.Validate(); err != nil {
		t.Fatalf("DefaultLimits().Validate() error = %v", err)
	}
	if limits.MaxPolicyBytes != MaximumPolicyBytes || limits.MaxASTDepth != MaximumASTDepth ||
		limits.MaxASTNodes != MaximumASTNodes || limits.MaxEvidenceRecords != MaximumEvidenceRecords {
		t.Fatalf("DefaultLimits() fixed ceilings = %+v", limits)
	}
	if limits.MaxRequestBytes <= 0 || limits.MaxRequestBytes > MaximumRequestBytes ||
		limits.MaxBatchRows <= 0 || limits.MaxBatchRows > MaximumBatchRows ||
		limits.MaxOutputBytes <= 0 || limits.MaxOutputBytes > MaximumOutputBytes ||
		limits.RequestTimeout <= 0 || limits.RequestTimeout > MaximumRequestTimeout {
		t.Fatalf("DefaultLimits() configurable bounds = %+v", limits)
	}
}

func TestLimitsRejectUnsafeDimensions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mutate func(*Limits)
		name   string
	}{
		{name: "policy bytes disabled", mutate: func(value *Limits) { value.MaxPolicyBytes = 0 }},
		{name: "policy bytes above ceiling", mutate: func(value *Limits) { value.MaxPolicyBytes = MaximumPolicyBytes + 1 }},
		{name: "AST depth disabled", mutate: func(value *Limits) { value.MaxASTDepth = 0 }},
		{name: "AST depth above ceiling", mutate: func(value *Limits) { value.MaxASTDepth = MaximumASTDepth + 1 }},
		{name: "AST nodes disabled", mutate: func(value *Limits) { value.MaxASTNodes = 0 }},
		{name: "AST nodes above ceiling", mutate: func(value *Limits) { value.MaxASTNodes = MaximumASTNodes + 1 }},
		{name: "batch rows disabled", mutate: func(value *Limits) { value.MaxBatchRows = 0 }},
		{name: "batch rows above ceiling", mutate: func(value *Limits) { value.MaxBatchRows = MaximumBatchRows + 1 }},
		{name: "evidence disabled", mutate: func(value *Limits) { value.MaxEvidenceRecords = 0 }},
		{name: "evidence above ceiling", mutate: func(value *Limits) { value.MaxEvidenceRecords = MaximumEvidenceRecords + 1 }},
		{name: "request bytes disabled", mutate: func(value *Limits) { value.MaxRequestBytes = 0 }},
		{name: "request bytes above ceiling", mutate: func(value *Limits) { value.MaxRequestBytes = MaximumRequestBytes + 1 }},
		{name: "output bytes disabled", mutate: func(value *Limits) { value.MaxOutputBytes = 0 }},
		{name: "output bytes above ceiling", mutate: func(value *Limits) { value.MaxOutputBytes = MaximumOutputBytes + 1 }},
		{name: "deadline disabled", mutate: func(value *Limits) { value.RequestTimeout = 0 }},
		{name: "deadline above ceiling", mutate: func(value *Limits) { value.RequestTimeout = MaximumRequestTimeout + time.Nanosecond }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := DefaultLimits()
			test.mutate(&limits)
			if err := limits.Validate(); !errors.Is(err, ErrInvalidLimits) {
				t.Fatalf("Limits.Validate() error = %v, want %v", err, ErrInvalidLimits)
			}
		})
	}
}

func TestRedactLogAttrRemovesStructuredSecrets(t *testing.T) {
	t.Parallel()

	const secret = "private-row-value"
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{ReplaceAttr: RedactLogAttr}))
	logger.Info("evaluate",
		slog.String("request_id", "R1"),
		slog.String("requests_json", secret),
		slog.Group("credentials", slog.String("password", secret)),
		slog.Int("rows", 5),
	)
	encoded := output.String()
	if strings.Contains(encoded, secret) {
		t.Fatalf("redacted log contains secret: %s", encoded)
	}
	if !strings.Contains(encoded, `"requests_json":"`+RedactedValue+`"`) ||
		!strings.Contains(encoded, `"password":"`+RedactedValue+`"`) {
		t.Fatalf("redacted log = %s", encoded)
	}
	if !strings.Contains(encoded, `"request_id":"R1"`) || !strings.Contains(encoded, `"rows":5`) {
		t.Fatalf("redaction removed safe metadata: %s", encoded)
	}
}

func TestContainsProtectedRowsRecognizesAuditContainers(t *testing.T) {
	t.Parallel()

	safe := []byte(`{"id":"R1","action":{"dataset":"protected_dataset","output":"individual_records"},"evidence_refs":["E1"]}`)
	if ContainsProtectedRows(safe) {
		t.Fatal("metadata-only request was classified as protected rows")
	}
	for _, payload := range [][]byte{
		[]byte(`{"id":"R1","rows":[{"email":"private-row-value"}]}`),
		[]byte(`{"id":"R1","RoWs":[{"email":"private-row-value"}]}`),
		[]byte(`{"id":"R1","dataset_\u0072ows":[{"email":"private-row-value"}]}`),
		[]byte(`{"id":"E1","attributes":{"protected_records":[{"name":"private-row-value"}]}}`),
	} {
		if !ContainsProtectedRows(payload) {
			t.Fatalf("ContainsProtectedRows(%s) = false", payload)
		}
	}
}

func TestContainsProtectedRowsDoesNotAllocate(t *testing.T) {
	payload := []byte(`{"id":"R1","dataset_rows":[{"email":"private-row-value"}]}`)
	var protected bool
	if allocations := testing.AllocsPerRun(100, func() {
		protected = ContainsProtectedRows(payload)
	}); allocations != 0 {
		t.Fatalf("ContainsProtectedRows() allocations = %v, want 0", allocations)
	}
	if !protected {
		t.Fatal("ContainsProtectedRows() lost result during allocation check")
	}
}
