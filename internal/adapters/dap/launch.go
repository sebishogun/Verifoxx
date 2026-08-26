// Package dap launches the local Delve DAP adapter used by editor clients.
package dap

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	DebugBuildFlags = "-tags=debug -gcflags='all=-N -l'"
	loopbackHost    = "127.0.0.1"
	maxArgumentSize = 4096
	maxArguments    = 64
	delveStopDelay  = 2 * time.Second
)

var (
	ErrInvalidLaunch = errors.New("dap: invalid launch configuration")
	ErrLaunchProcess = errors.New("dap: launch process")
)

// Config defines one local Delve server and its DAP launch request.
type Config struct {
	Stdout           io.Writer
	Stderr           io.Writer
	DelvePath        string
	WorkingDirectory string
	Target           string
	TargetArguments  []string
	Port             uint16
	Log              bool
}

// Configuration is the target launch body consumed by a DAP client.
type Configuration struct {
	Name             string   `json:"name"`
	Type             string   `json:"type"`
	Request          string   `json:"request"`
	Mode             string   `json:"mode"`
	Program          string   `json:"program"`
	WorkingDirectory string   `json:"cwd"`
	BuildFlags       string   `json:"buildFlags"`
	Arguments        []string `json:"args,omitempty"`
}

// Plan is a caller-owned immutable Delve process and target configuration.
type Plan struct {
	Executable       string
	WorkingDirectory string
	Address          string
	Arguments        []string
	Configuration    Configuration
}

// Launcher prepares and starts local Delve DAP processes.
type Launcher struct {
	selectPort     func(context.Context) (uint16, error)
	commandContext func(context.Context, string, ...string) *exec.Cmd
}

// Server owns a running Delve process. Wait may be called concurrently.
type Server struct {
	cancel  context.CancelFunc
	done    chan struct{}
	waitErr error
	Plan    Plan
}

// Plan validates config, selects a loopback port when needed, and constructs
// both sides of the DAP launch without starting a process.
func (launcher Launcher) Plan(ctx context.Context, config Config) (Plan, error) {
	if ctx == nil || !validConfig(config) {
		return Plan{}, ErrInvalidLaunch
	}
	if err := ctx.Err(); err != nil {
		return Plan{}, errors.Join(ErrInvalidLaunch, err)
	}
	port := config.Port
	if port == 0 {
		selectPort := launcher.selectPort
		if selectPort == nil {
			selectPort = selectLoopbackPort
		}
		selected, err := selectPort(ctx)
		if err != nil || selected == 0 {
			return Plan{}, errors.Join(ErrInvalidLaunch, err)
		}
		port = selected
	}
	address := net.JoinHostPort(loopbackHost, strconv.FormatUint(uint64(port), 10))
	arguments := make([]string, 0, 4)
	arguments = append(arguments, "dap", "--listen", address)
	if config.Log {
		arguments = append(arguments, "--log")
	}
	targetArguments := slices.Clone(config.TargetArguments)
	return Plan{
		Executable:       config.DelvePath,
		WorkingDirectory: config.WorkingDirectory,
		Address:          address,
		Arguments:        arguments,
		Configuration: Configuration{
			Name:             "Debug NornRune",
			Type:             "go",
			Request:          "launch",
			Mode:             "debug",
			Program:          config.Target,
			WorkingDirectory: config.WorkingDirectory,
			Arguments:        targetArguments,
			BuildFlags:       DebugBuildFlags,
		},
	}, nil
}

// Launch starts a Delve DAP process and binds its lifetime to ctx.
func (launcher Launcher) Launch(ctx context.Context, config Config) (*Server, error) {
	plan, err := launcher.Plan(ctx, config)
	if err != nil {
		return nil, err
	}
	launchCtx, cancel := context.WithCancel(ctx)
	commandContext := launcher.commandContext
	if commandContext == nil {
		commandContext = exec.CommandContext
	}
	command := commandContext(launchCtx, plan.Executable, plan.Arguments...)
	if command == nil {
		cancel()
		return nil, ErrLaunchProcess
	}
	var cancelRequested atomic.Bool
	command.Cancel = func() error {
		return interruptDelve(command, &cancelRequested)
	}
	command.WaitDelay = delveStopDelay
	command.Dir = plan.WorkingDirectory
	command.Stdout = config.Stdout
	command.Stderr = config.Stderr
	if err := command.Start(); err != nil {
		cancel()
		return nil, errors.Join(ErrLaunchProcess, err)
	}
	server := &Server{
		cancel: cancel,
		done:   make(chan struct{}),
		Plan:   plan,
	}
	go server.reap(launchCtx, command, &cancelRequested)
	return server, nil
}

// Wait blocks until Delve exits and reports context cancellation directly.
func (server *Server) Wait() error {
	if server == nil || server.done == nil {
		return ErrLaunchProcess
	}
	<-server.done
	return server.waitErr
}

// Close cancels Delve, waits for it to exit, and is safe to call repeatedly.
func (server *Server) Close() error {
	if server == nil || server.cancel == nil {
		return ErrLaunchProcess
	}
	server.cancel()
	err := server.Wait()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func interruptDelve(command *exec.Cmd, cancelRequested *atomic.Bool) error {
	if command == nil || command.Process == nil || cancelRequested == nil {
		return os.ErrProcessDone
	}
	err := command.Process.Signal(os.Interrupt)
	if err == nil {
		cancelRequested.Store(true)
	}
	return err
}

func (server *Server) reap(ctx context.Context, command *exec.Cmd, cancelRequested *atomic.Bool) {
	err := command.Wait()
	if err != nil && cancelRequested.Load() {
		if contextErr := ctx.Err(); contextErr != nil {
			err = contextErr
		}
	}
	server.waitErr = err
	close(server.done)
}

func validConfig(config Config) bool {
	if !validArgument(config.DelvePath) || !validArgument(config.WorkingDirectory) ||
		!validArgument(config.Target) || len(config.TargetArguments) > maxArguments {
		return false
	}
	for _, argument := range config.TargetArguments {
		if !validArgument(argument) {
			return false
		}
	}
	return true
}

func validArgument(value string) bool {
	return value != "" && len(value) <= maxArgumentSize && !strings.ContainsRune(value, 0)
}

func selectLoopbackPort(ctx context.Context) (uint16, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", net.JoinHostPort(loopbackHost, "0"))
	if err != nil {
		return 0, err
	}
	address, ok := listener.Addr().(*net.TCPAddr)
	port := 0
	if ok {
		port = address.Port
	}
	closeErr := listener.Close()
	if port < 1 || port > int(^uint16(0)) {
		return 0, ErrInvalidLaunch
	}
	if closeErr != nil {
		return 0, closeErr
	}
	return uint16(port), nil
}
