package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

type execRunner struct{}

func (execRunner) Run(ctx context.Context, directory string, spec commandSpec, stdin io.Reader, stdout, stderr io.Writer) error {
	if ctx == nil || directory == "" || spec.executable == "" || spec.timeout <= 0 {
		return errWorkflowUnavailable
	}
	commandCtx, cancel := context.WithTimeout(ctx, spec.timeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, spec.executable, spec.arguments...)
	command.Dir = directory
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = 2 * time.Second
	if len(spec.environment) != 0 {
		command.Env = make([]string, 0, len(os.Environ())+len(spec.environment))
		command.Env = append(command.Env, os.Environ()...)
		command.Env = append(command.Env, spec.environment...)
	}
	err := command.Run()
	if commandCtx.Err() != nil {
		return errors.Join(commandCtx.Err(), err)
	}
	return err
}

// ExecuteContext runs one devx command with caller-owned process I/O.
func ExecuteContext(ctx context.Context, arguments []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if ctx == nil {
		return context.Canceled
	}
	root := newRoot(dependencies{
		stdin: stdin, stdout: stdout, stderr: stderr,
		getwd: os.Getwd, readFile: os.ReadFile, lookPath: exec.LookPath, stat: os.Stat, removeAll: os.RemoveAll,
		runner: execRunner{}, menu: huhMenu{}, confirm: huhConfirmation{},
	})
	root.SetArgs(arguments)
	return root.ExecuteContext(ctx)
}

// Execute runs one devx command with a background context.
func Execute(arguments []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return ExecuteContext(context.Background(), arguments, stdin, stdout, stderr)
}
