package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type doctorProbe struct {
	label      string
	executable string
	arguments  []string
}

var doctorProbes = [...]doctorProbe{
	{label: "Go 1.27", executable: "go", arguments: []string{"version"}},
	{label: "Docker", executable: "docker", arguments: []string{"--version"}},
	{label: "Docker Compose", executable: "docker", arguments: []string{"compose", "version"}},
	{label: "Delve", executable: "dlv", arguments: []string{"version"}},
	{label: "Buf", executable: "buf", arguments: []string{"--version"}},
	{label: "protoc", executable: "protoc", arguments: []string{"--version"}},
	{label: "benchstat", executable: "benchstat", arguments: []string{"-h"}},
	{label: "PostgreSQL client", executable: "psql", arguments: []string{"--version"}},
}

func runDoctor(ctx context.Context, deps dependencies) error {
	if ctx == nil || deps.getwd == nil || deps.readFile == nil || deps.lookPath == nil || deps.runner == nil || deps.stdout == nil {
		return errWorkflowUnavailable
	}
	workingDirectory, err := deps.getwd()
	if err != nil {
		return errors.Join(ErrRepositoryRoot, err)
	}
	repository, err := findRepositoryRoot(workingDirectory, deps.readFile)
	if err != nil {
		return err
	}
	failed := false
	for _, probe := range doctorProbes {
		if _, err := deps.lookPath(probe.executable); err != nil {
			failed = true
			if _, writeErr := fmt.Fprintf(deps.stdout, "%s\tmissing executable %s\n", probe.label, probe.executable); writeErr != nil {
				return writeErr
			}
			continue
		}

		var version bytes.Buffer
		probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := deps.runner.Run(probeCtx, repository, commandSpec{
			executable: probe.executable,
			arguments:  probe.arguments,
			timeout:    10 * time.Second,
		}, nil, &version, &version)
		cancel()
		if err != nil {
			failed = true
			if _, writeErr := fmt.Fprintf(deps.stdout, "%s\tprobe failed: %v\n", probe.label, err); writeErr != nil {
				return writeErr
			}
			continue
		}
		result := strings.TrimSpace(version.String())
		if result == "" {
			result = "available"
		}
		if probe.executable == "go" && result != "available" && !strings.Contains(result, "go1.27") {
			failed = true
			result = "unsupported version: " + result
		}
		if _, err := fmt.Fprintf(deps.stdout, "%s\t%s\n", probe.label, result); err != nil {
			return err
		}
	}
	if failed {
		return ErrDoctorFailed
	}
	return nil
}
