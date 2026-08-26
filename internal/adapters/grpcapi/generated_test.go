package grpcapi

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

const bufImage = "bufbuild/buf:1.72.0@sha256:65bd496a89c762ad7151ca9e7d885a45dacb3671a8e8ec39738b9f844d3405ea"

func TestGeneratedCodeIsCurrent(t *testing.T) {
	if os.Getenv("VERIFOXX_CHECK_GENERATED") != "1" {
		t.Skip("set VERIFOXX_CHECK_GENERATED=1 to run the containerized drift check")
	}
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() did not return the test source path")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../../.."))
	workspace := t.TempDir()
	inputs := []string{
		"buf.yaml",
		"buf.gen.yaml",
		"buf.frontend.gen.yaml",
		"api/proto/verifoxx/v1/verifoxx.proto",
		"frontend/proto/options.proto",
		"testdata/frontends/proto/policy.proto",
	}
	for _, relative := range inputs {
		copyGeneratedCheckFile(t, repository, workspace, relative)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	user := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
	buildFrontendPlugin(t, ctx, repository, workspace)
	runBufCheck(t, ctx, workspace, user, "lint")
	runBufCheck(t, ctx, workspace, user, "generate")
	runBufCheck(t, ctx, workspace, user, "generate", "--template", "buf.frontend.gen.yaml")

	generated := []string{
		"api/gen/verifoxx/v1/verifoxx.pb.go",
		"api/gen/verifoxx/v1/verifoxx_grpc.pb.go",
		"frontend/proto/options.pb.go",
		"testdata/frontends/proto/policy.pb.go",
		"testdata/frontends/proto/policy_verifoxx.pb.go",
	}
	for _, relative := range generated {
		want, err := os.ReadFile(filepath.Join(repository, relative))
		if err != nil {
			t.Fatalf("read checked-in %s: %v", relative, err)
		}
		got, err := os.ReadFile(filepath.Join(workspace, relative))
		if err != nil {
			t.Fatalf("read generated %s: %v", relative, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is stale; regenerate with the pinned buf.gen.yaml", relative)
		}
	}
}

func buildFrontendPlugin(t *testing.T, ctx context.Context, repository, workspace string) {
	t.Helper()
	destination := filepath.Join(workspace, ".verifoxx/tools/protoc-gen-verifoxx")
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatalf("create frontend plugin directory: %v", err)
	}
	command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", destination, "./cmd/protoc-gen-verifoxx")
	command.Dir = repository
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build frontend plugin error = %v\n%s", err, output)
	}
}

func runBufCheck(t *testing.T, ctx context.Context, workspace, user string, arguments ...string) {
	t.Helper()
	command := exec.CommandContext(ctx, "docker", "run", "--rm", "--user", user, "-e", "HOME=/tmp",
		"-v", workspace+":/workspace", "-w", "/workspace", bufImage)
	command.Args = append(command.Args, arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("containerized buf %v error = %v\n%s", arguments, err, output)
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("containerized buf %v deadline = %v", arguments, err)
	}
}

func copyGeneratedCheckFile(t *testing.T, sourceRoot, destinationRoot, relative string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sourceRoot, relative))
	if err != nil {
		t.Fatalf("read generation input %s: %v", relative, err)
	}
	destination := filepath.Join(destinationRoot, relative)
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatalf("create generation input directory for %s: %v", relative, err)
	}
	if err := os.WriteFile(destination, data, 0o644); err != nil {
		t.Fatalf("write generation input %s: %v", relative, err)
	}
}

func TestBufImageIsPinned(t *testing.T) {
	if want := "@sha256:"; !bytes.Contains([]byte(bufImage), []byte(want)) {
		t.Fatalf("buf image %q does not contain %q", bufImage, want)
	}
}
