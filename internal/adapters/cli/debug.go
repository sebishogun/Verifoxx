package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/sebishogun/nornrune/internal/debug"
)

var (
	errMissingDebugSocket = errors.New("--socket is required")
	errInvalidDebugSocket = errors.New("debug socket must be unused in an owner-only absolute directory")
	errDebugSocketActive  = errors.New("debug socket already has an active listener")
)

const debugSocketProbeTimeout = 100 * time.Millisecond

func newDebugWorkerCommand(deps dependencies) *cobra.Command {
	var flags sourceFlags
	var socketPath string
	cmd := &cobra.Command{
		Use:   "debug-worker",
		Short: "Serve one retained semantic debug session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) (returnErr error) {
			if socketPath == "" {
				return usageError(errMissingDebugSocket)
			}
			inputs, err := loadSources(flags, cmd.InOrStdin(), deps, sourceAll)
			if err != nil {
				return classifyCommandError(err)
			}
			var pipeline engine
			compiled, err := pipeline.compilePolicy(inputs.policy)
			if err != nil {
				return operationalError(err)
			}
			batch, err := pipeline.decodeBatch(compiled, inputs.requests, inputs.evidence)
			if err != nil {
				return operationalError(err)
			}
			if err := prepareDebugSocket(socketPath); err != nil {
				return usageError(err)
			}
			session, err := debug.NewSession(compiled, batch, debugWorkerConfig())
			if err != nil {
				return operationalError(pipelineFailure("start debug session", err))
			}
			defer func() {
				closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := session.Close(closeCtx); returnErr == nil && err != nil {
					returnErr = operationalError(pipelineFailure("close debug session", err))
				}
			}()

			listener, err := net.Listen("unix", socketPath)
			if err != nil {
				return operationalError(pipelineFailure("listen on debug socket", err))
			}
			defer listener.Close()
			defer os.Remove(socketPath)
			if err := os.Chmod(socketPath, 0o600); err != nil {
				return operationalError(pipelineFailure("protect debug socket", err))
			}
			server, err := debug.NewServer(session, debug.DefaultTransportConfig())
			if err != nil {
				return operationalError(pipelineFailure("start debug transport", err))
			}
			if _, err := io.WriteString(cmd.OutOrStdout(), "semantic socket: "+socketPath+"\n"); err != nil {
				return operationalError(err)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if err := server.Serve(ctx, listener); err != nil {
				return operationalError(pipelineFailure("serve debug transport", err))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&socketPath, "socket", "", "absolute owner-only Unix socket path")
	bindSourceFlags(cmd, &flags, sourceAll)
	return cmd
}

func debugWorkerConfig() debug.Config {
	return debug.Config{
		CommandDepth:       16,
		MaxBreakpoints:     256,
		MaxWatches:         256,
		CheckpointInterval: 16,
		MaxCheckpoints:     128,
	}
}

func prepareDebugSocket(path string) error {
	if !filepath.IsAbs(path) || filepath.Base(path) == "." || filepath.Base(path) == string(filepath.Separator) {
		return errInvalidDebugSocket
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return errors.Join(errInvalidDebugSocket, err)
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.Join(errInvalidDebugSocket, err)
	}
	socketInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || socketInfo.Mode()&os.ModeSocket == 0 {
		return errors.Join(errInvalidDebugSocket, err)
	}
	connection, dialErr := net.DialTimeout("unix", path, debugSocketProbeTimeout)
	if dialErr == nil {
		_ = connection.Close()
		return errors.Join(errInvalidDebugSocket, errDebugSocketActive)
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
		return errors.Join(errInvalidDebugSocket, dialErr)
	}
	currentInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !os.SameFile(socketInfo, currentInfo) {
		return errors.Join(errInvalidDebugSocket, err)
	}
	if err := os.Remove(path); err != nil {
		return errors.Join(errInvalidDebugSocket, err)
	}
	return nil
}
