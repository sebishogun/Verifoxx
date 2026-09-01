package doccheck_test

import (
	"strings"
	"testing"
)

func TestWASMGuideDefinesPortableSecurityAndCompatibilityBoundary(t *testing.T) {
	t.Parallel()
	content := strings.ToLower(readDocument(t, "docs/wasm.md"))
	for _, phrase := range []string{
		"abi version", "schema version", "sha-256", "little-endian", "memory ownership",
		"fuel", "cancellation", "wazero", "node", "browser", "scalar", "0 b/op",
		"envoy", "istio", "cloudflare", "not certified", "scripts/check-wasm.sh",
	} {
		if !strings.Contains(content, phrase) {
			t.Errorf("WebAssembly guide does not cover %q", phrase)
		}
	}
}

func TestWASMGuideIsLinkedFromCoreDocumentation(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"README.md", "docs/architecture.md", "docs/performance.md"} {
		if !strings.Contains(readDocument(t, path), "wasm.md") {
			t.Errorf("%s does not link the WebAssembly guide", path)
		}
	}
}
