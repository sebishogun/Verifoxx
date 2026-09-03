package doccheck_test

import (
	"strings"
	"testing"
)

func TestPolicyDiffGuideStatesExactProofBoundary(t *testing.T) {
	t.Parallel()
	content := strings.ToLower(readDocument(t, "docs/policy-diff.md"))
	required := []string{
		"finite", "closed", "4x4", "inconclusive", "counterexample", "native replay",
		"proof provider", "return promptly", "context is done", "exit code `3`", "exit code `4`",
		"expiry", "cold search", "0 b/op",
	}
	for _, phrase := range required {
		if !strings.Contains(content, phrase) {
			t.Errorf("policy diff guide does not cover %q", phrase)
		}
	}
	for _, forbidden := range []string{"universal equivalence", "unbounded proof", "smt proof"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("policy diff guide makes unsupported claim %q", forbidden)
		}
	}
}

func TestPolicyDiffIsLinkedFromCoreDocumentation(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"README.md", "docs/architecture.md", "docs/performance.md"} {
		if !strings.Contains(readDocument(t, path), "policy-diff.md") {
			t.Errorf("%s does not link the policy diff guide", path)
		}
	}
}
