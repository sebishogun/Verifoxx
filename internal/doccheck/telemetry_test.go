package doccheck_test

import (
	"strings"
	"testing"
)

func TestTelemetryGuideDefinesStableNamesModesAndPrivacyBoundary(t *testing.T) {
	t.Parallel()
	content := strings.ToLower(readDocument(t, "docs/telemetry.md"))
	for _, phrase := range []string{
		"nornrune_evaluation_outcomes_total", "nornrune_evaluation_escalations_total",
		"nornrune_audit_outcomes_total", "nornrune_policy_reloads_total",
		"nornrune_service_queue_wait_seconds", "nornrune_service_active_admissions",
		"nornrune_telemetry_export_drops_total", "nornrune_shutdown_failures_total",
		"fixed cardinality", "cumulative", "request id", "evidence", "policy source",
		"credentials", "error string", "disabled", "counters-only", "prometheus", "otlp",
		"traceparent", "non-blocking", "drop", "shutdown", "readiness", "0 b/op",
		"late sampled span", "bounds only that caller's wait", "cleanup continues",
	} {
		if !strings.Contains(content, phrase) {
			t.Errorf("telemetry guide does not cover %q", phrase)
		}
	}
}

func TestTelemetryGuideIsLinkedFromCoreDocumentation(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"README.md", "docs/architecture.md", "docs/operations.md", "docs/performance.md"} {
		if !strings.Contains(readDocument(t, path), "telemetry.md") {
			t.Errorf("%s does not link the telemetry guide", path)
		}
	}
}

func TestPrometheusRulesCoverBoundedMultiWindowAlerts(t *testing.T) {
	t.Parallel()
	content := readDocument(t, "deploy/telemetry/prometheus-rules.yaml")
	required := []string{
		"nornrune_evaluation_decision_rate_change", "nornrune_evaluation_escalation_spike",
		"nornrune_audit_failures", "nornrune_service_queue_saturation",
		"nornrune_policy_reload_failures", "nornrune_shutdown_timeouts",
	}
	for _, alert := range required {
		if !strings.Contains(content, "alert: "+alert) {
			t.Errorf("recording rules do not define alert %q", alert)
		}
	}
	windows := 0
	for _, window := range []string{"5m", "15m", "30m", "1h"} {
		if strings.Contains(content, window) {
			windows++
		}
	}
	if windows < 2 {
		t.Errorf("alerts are not multi-window; found %d distinct windows", windows)
	}
	for _, forbidden := range []string{"request_id", "policy_name", "policy_hash", "user", "url", "evidence"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("alerting rules reference forbidden label %q", forbidden)
		}
	}
	if !strings.Contains(content, "severity") {
		t.Error("alerting rules do not carry a fixed severity label")
	}
	for _, failure := range []string{`outcome="required_failure"`, `outcome="optional_drop"`, "nornrune_audit_journal_failures_total"} {
		if !strings.Contains(content, failure) {
			t.Errorf("audit alert does not cover %q", failure)
		}
	}
	normalized := strings.Join(strings.Fields(content), " ")
	for _, expression := range []string{
		"rate(nornrune_evaluation_outcomes_total[5m]) / on() group_left clamp_min(sum(rate(nornrune_evaluation_outcomes_total[5m])), 1e-9)",
		"rate(nornrune_evaluation_outcomes_total[1h]) / on() group_left clamp_min(sum(rate(nornrune_evaluation_outcomes_total[1h])), 1e-9)",
		"and on() sum(rate(nornrune_evaluation_outcomes_total[5m])) > 0",
		"and on() sum(rate(nornrune_evaluation_outcomes_total[1h])) > 0",
		"nornrune_service_queue_depth >= 2 * nornrune_evaluation_workers",
	} {
		if !strings.Contains(normalized, expression) {
			t.Errorf("alerting rules do not contain %q", expression)
		}
	}
}
