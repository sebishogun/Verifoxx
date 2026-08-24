//go:build docker

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	verifoxxv1 "github.com/sebishogun/verifoxx/api/gen/verifoxx/v1"
	"github.com/sebishogun/verifoxx/internal/fixtures"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestComposeFullMode(t *testing.T) {
	repository := repositoryRoot(t)
	waitScript := filepath.Join(repository, "scripts", "wait-healthy.sh")
	if info, err := os.Stat(waitScript); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("health wait script is not executable: %v", err)
	}

	ports := freePorts(t, 3)
	httpPort := ports[0]
	grpcPort := ports[1]
	postgresPort := ports[2]
	project := fmt.Sprintf("verifoxx-e2e-%d", os.Getpid())
	environment := append(os.Environ(),
		"VERIFOXX_HTTP_PORT="+strconv.Itoa(httpPort),
		"VERIFOXX_GRPC_PORT="+strconv.Itoa(grpcPort),
		"POSTGRES_PORT="+strconv.Itoa(postgresPort),
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = runCompose(ctx, repository, environment, project,
			"down", "--volumes", "--remove-orphans", "--rmi", "local")
	})

	upContext, cancelUp := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancelUp()
	if output, err := runCompose(upContext, repository, environment, project,
		"up", "--detach", "--build", "--wait", "--wait-timeout", "420"); err != nil {
		t.Fatalf("docker compose up error = %v\n%s", err, output)
	}

	probeContext, cancelProbe := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelProbe()
	if output, err := runCompose(probeContext, repository, environment, project,
		"exec", "-T", "postgres", "pg_isready", "-U", "postgres", "-d", "verifoxx"); err != nil {
		t.Fatalf("PostgreSQL health error = %v\n%s", err, output)
	}
	if got := queryComposePostgres(t, repository, environment, project,
		"SELECT count(*) FROM public.verifoxx_schema_migrations"); got != "2" {
		t.Fatalf("applied migrations = %q, want 2", got)
	}

	healthURL := fmt.Sprintf("http://127.0.0.1:%d/readyz", httpPort)
	waitContext, cancelWait := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelWait()
	wait := exec.CommandContext(waitContext, waitScript, healthURL, "25")
	wait.Dir = repository
	if output, err := wait.CombinedOutput(); err != nil {
		t.Fatalf("wait for server health error = %v\n%s", err, output)
	}

	want, err := os.ReadFile(filepath.Join(repository, "testdata", "golden", "requests.json"))
	if err != nil {
		t.Fatalf("read golden results: %v", err)
	}
	envelope, err := json.Marshal(struct {
		Requests json.RawMessage `json:"requests"`
		Evidence json.RawMessage `json:"evidence"`
	}{Requests: json.RawMessage(fixtures.RequestsJSON()), Evidence: json.RawMessage(fixtures.EvidenceJSON())})
	if err != nil {
		t.Fatalf("encode HTTP evaluation envelope: %v", err)
	}
	httpContext, cancelHTTP := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelHTTP()
	httpRequest, err := http.NewRequestWithContext(httpContext, http.MethodPost,
		fmt.Sprintf("http://127.0.0.1:%d/v1/evaluate", httpPort), bytes.NewReader(envelope))
	if err != nil {
		t.Fatalf("create HTTP evaluation request: %v", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse, err := (&http.Client{Timeout: 30 * time.Second}).Do(httpRequest)
	if err != nil {
		t.Fatalf("HTTP evaluation error = %v", err)
	}
	httpBody, readErr := io.ReadAll(httpResponse.Body)
	closeErr := httpResponse.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read HTTP evaluation = (%v, %v)", readErr, closeErr)
	}
	if httpResponse.StatusCode != http.StatusOK || !bytes.Equal(httpBody, want) {
		t.Fatalf("HTTP evaluation = %s\n%s", httpResponse.Status, httpBody)
	}

	grpcContext, cancelGRPC := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelGRPC()
	connection, err := grpc.NewClient(
		net.JoinHostPort("127.0.0.1", strconv.Itoa(grpcPort)),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("create gRPC client: %v", err)
	}
	defer connection.Close()
	grpcResponse, err := verifoxxv1.NewPolicyServiceClient(connection).EvaluateBatch(grpcContext,
		&verifoxxv1.EvaluateBatchRequest{
			RequestsJson: []byte(fixtures.RequestsJSON()),
			EvidenceJson: []byte(fixtures.EvidenceJSON()),
		})
	if err != nil {
		t.Fatalf("gRPC evaluation error = %v", err)
	}
	if !bytes.Equal(grpcResponse.GetResultJson(), want) {
		t.Fatalf("gRPC evaluation differs from golden\n%s", grpcResponse.GetResultJson())
	}

	if got := queryComposePostgres(t, repository, environment, project, `
		SELECT (SELECT count(*) FROM verifoxx.evaluation_runs)::text
		       || '|' ||
		       (SELECT count(*) FROM verifoxx.evaluation_findings)::text
	`); got != "2|10" {
		t.Fatalf("persisted audit counts = %q, want 2|10", got)
	}
}

func TestWaitHealthyBoundsStalledResponse(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for stalled health response: %v", err)
	}
	holdConnection := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		<-holdConnection
	}()
	t.Cleanup(func() {
		close(holdConnection)
		_ = listener.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	started := time.Now()
	wait := exec.CommandContext(ctx, filepath.Join(repositoryRoot(t), "scripts", "wait-healthy.sh"),
		"http://"+listener.Addr().String(), "1")
	output, err := wait.CombinedOutput()
	if err == nil {
		t.Fatal("wait-healthy.sh succeeded for a stalled response")
	}
	if ctx.Err() != nil || time.Since(started) > 3*time.Second {
		t.Fatalf("wait-healthy.sh exceeded its own timeout: %v", err)
	}
	if !bytes.Contains(output, []byte("timed out waiting")) {
		t.Fatalf("wait-healthy.sh output = %q", output)
	}
}

func freePorts(t *testing.T, count int) []int {
	t.Helper()
	listeners := make([]net.Listener, 0, count)
	ports := make([]int, 0, count)
	for range count {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			for _, open := range listeners {
				_ = open.Close()
			}
			t.Fatalf("allocate local port: %v", err)
		}
		listeners = append(listeners, listener)
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
	}
	for _, listener := range listeners {
		if err := listener.Close(); err != nil {
			t.Fatalf("release local port: %v", err)
		}
	}
	return ports
}

func runCompose(
	ctx context.Context,
	repository string,
	environment []string,
	project string,
	arguments ...string,
) ([]byte, error) {
	commandArguments := make([]string, 0, len(arguments)+3)
	commandArguments = append(commandArguments, "compose", "--project-name", project)
	commandArguments = append(commandArguments, arguments...)
	command := exec.CommandContext(ctx, "docker", commandArguments...)
	command.Dir = repository
	command.Env = environment
	return command.CombinedOutput()
}

func queryComposePostgres(
	t *testing.T,
	repository string,
	environment []string,
	project string,
	query string,
) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	output, err := runCompose(ctx, repository, environment, project,
		"exec", "-T", "postgres", "psql", "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1",
		"-U", "postgres", "-d", "verifoxx", "-c", query)
	if err != nil {
		t.Fatalf("PostgreSQL query error = %v\n%s", err, output)
	}
	return strings.TrimSpace(string(output))
}
