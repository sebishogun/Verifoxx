package doccheck_test

import (
	"strings"
	"testing"
)

func TestCommunityHealthFilesExist(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"LICENSE",
		"NOTICE",
		"CONTRIBUTING.md",
		"CODE_OF_CONDUCT.md",
		"SECURITY.md",
		"SUPPORT.md",
		"docs/versioning.md",
		"docs/dependency-licenses.md",
		".github/ISSUE_TEMPLATE/bug_report.yml",
		".github/ISSUE_TEMPLATE/feature_request.yml",
		".github/ISSUE_TEMPLATE/config.yml",
		".github/PULL_REQUEST_TEMPLATE.md",
	} {
		readDocument(t, path)
	}
}

func TestCommunityHealthContent(t *testing.T) {
	t.Parallel()

	checks := map[string][]string{
		"LICENSE":         {"Apache License", "Version 2.0, January 2004"},
		"CONTRIBUTING.md": {"Developer Certificate of Origin", "go test -count=1 -timeout 60s ./...", "proto:gen", "0 allocs/op"},
		"SECURITY.md":     {"private vulnerability reporting", "supported versions", "policy decisions"},
		"SUPPORT.md":      {"GitHub Issues", "no guaranteed response time"},
		"docs/versioning.md": {
			"Semantic Versioning", "policy semantics", "generated API",
		},
		"docs/dependency-licenses.md": {
			"cel.dev/cel-go", "github.com/cedar-policy/cedar-go", "github.com/sebishogun/simd", "google.golang.org/protobuf",
		},
	}
	for path, required := range checks {
		content := strings.ToLower(readDocument(t, path))
		for _, text := range required {
			if !strings.Contains(content, strings.ToLower(text)) {
				t.Errorf("%s does not document %q", path, text)
			}
		}
	}
}

func TestReadmeLinksCommunityFiles(t *testing.T) {
	t.Parallel()

	readme := readDocument(t, "README.md")
	for _, link := range []string{
		"[License](LICENSE)",
		"[Contributing](CONTRIBUTING.md)",
		"[Security](SECURITY.md)",
		"[Support](SUPPORT.md)",
		"[Versioning](docs/versioning.md)",
		"[Dependency licenses](docs/dependency-licenses.md)",
	} {
		if !strings.Contains(readme, link) {
			t.Errorf("README.md does not link %q", link)
		}
	}
}
