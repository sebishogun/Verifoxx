package cli

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sebishogun/nornrune/internal/debug"
)

func TestDebugWorkerServesSemanticSocket(t *testing.T) {
	socketDirectory := t.TempDir()
	if err := os.Chmod(socketDirectory, 0o700); err != nil {
		t.Fatalf("Chmod(socket directory) error = %v", err)
	}
	socketPath := filepath.Join(socketDirectory, "debug.sock")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr bytes.Buffer
	root := newRoot(&stdout, &stderr, productTestDependencies())
	root.SetArgs([]string{"debug-worker", "--socket", socketPath})
	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		info, err := os.Lstat(socketPath)
		if err == nil {
			if info.Mode()&os.ModeSocket == 0 {
				t.Fatalf("debug socket mode = %v", info.Mode())
			}
			if info.Mode().Perm() == 0o600 {
				break
			}
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Lstat(debug socket) error = %v", err)
		}
		if time.Now().After(deadline) {
			select {
			case commandErr := <-done:
				t.Fatalf("debug-worker exited before listening: %v; stderr=%q", commandErr, stderr.String())
			default:
			}
			t.Fatal("debug-worker did not create its socket")
		}
		time.Sleep(5 * time.Millisecond)
	}

	dialCtx, dialCancel := context.WithTimeout(context.Background(), time.Second)
	client, err := debug.DialClient(dialCtx, socketPath, debug.DefaultTransportConfig())
	dialCancel()
	if err != nil {
		t.Fatalf("DialClient() error = %v", err)
	}
	stepCtx, stepCancel := context.WithTimeout(context.Background(), time.Second)
	state, err := client.StepInstruction(stepCtx)
	stepCancel()
	if err != nil {
		t.Fatalf("StepInstruction() error = %v", err)
	}
	if state.Instruction != 1 || state.NextInstruction != 2 {
		t.Fatalf("semantic state = instruction:%d next:%d", state.Instruction, state.NextInstruction)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Client.Close() error = %v", err)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("debug-worker shutdown error = %v; stderr=%q", err, stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("debug-worker did not stop after cancellation")
	}
	if _, err := os.Lstat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("debug socket remains after shutdown: %v", err)
	}
}

func TestDebugWorkerClassifiesSourceErrors(t *testing.T) {
	directory := t.TempDir()
	socketPath := filepath.Join(directory, "debug.sock")
	missingPolicy := filepath.Join(directory, "missing-policy.json")
	code, stdout, stderr := runCLI(t,
		"debug-worker", "--socket", socketPath, "--policy", missingPolicy,
	)
	if code != 1 || stdout != "" || !strings.Contains(stderr, "missing-policy.json") || strings.Contains(stderr, "Usage:") {
		t.Fatalf("debug-worker source error = code:%d stdout:%q stderr:%q", code, stdout, stderr)
	}
}

func TestDebugWorkerRequiresSocket(t *testing.T) {
	code, stdout, stderr := runCLI(t, "debug-worker")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "--socket is required") {
		t.Fatalf("debug-worker missing socket = code:%d stdout:%q stderr:%q", code, stdout, stderr)
	}
}

func TestPrepareDebugSocketCreatesOwnerOnlyDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("Chmod(root) error = %v", err)
	}
	directory := filepath.Join(root, "private")
	if err := prepareDebugSocket(filepath.Join(directory, "debug.sock")); err != nil {
		t.Fatalf("prepareDebugSocket() error = %v", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		t.Fatalf("Lstat(directory) error = %v", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("created directory mode = %v", info.Mode())
	}
}

func TestPrepareDebugSocketRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatalf("Chmod(root) error = %v", err)
	}
	permissive := filepath.Join(root, "permissive")
	if err := os.Mkdir(permissive, 0o700); err != nil {
		t.Fatalf("Mkdir(permissive) error = %v", err)
	}
	if err := os.Chmod(permissive, 0o755); err != nil {
		t.Fatalf("Chmod(permissive) error = %v", err)
	}
	existingPath := filepath.Join(root, "existing.sock")
	if err := os.WriteFile(existingPath, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(existing) error = %v", err)
	}
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatalf("Mkdir(target) error = %v", err)
	}
	symlink := filepath.Join(root, "symlink")
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	for name, path := range map[string]string{
		"empty":                "",
		"relative":             "debug.sock",
		"permissive directory": filepath.Join(permissive, "debug.sock"),
		"existing path":        existingPath,
		"symlink directory":    filepath.Join(symlink, "debug.sock"),
	} {
		t.Run(name, func(t *testing.T) {
			if err := prepareDebugSocket(path); !errors.Is(err, errInvalidDebugSocket) {
				t.Fatalf("prepareDebugSocket(%q) error = %v", path, err)
			}
		})
	}
}

func TestPrepareDebugSocketRemovesStaleSocket(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("Chmod(directory) error = %v", err)
	}
	path := filepath.Join(directory, "debug.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("Listen(unix) error = %v", err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatalf("listener.Close() error = %v", err)
	}
	if err := prepareDebugSocket(path); err != nil {
		t.Fatalf("prepareDebugSocket(stale) error = %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket remains: %v", err)
	}
}

func TestPrepareDebugSocketRejectsActiveSocket(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("Chmod(directory) error = %v", err)
	}
	path := filepath.Join(directory, "debug.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("Listen(unix) error = %v", err)
	}
	defer listener.Close()
	if err := prepareDebugSocket(path); !errors.Is(err, errInvalidDebugSocket) {
		t.Fatalf("prepareDebugSocket(active) error = %v", err)
	}
}
