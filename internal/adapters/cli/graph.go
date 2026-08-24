package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/sebishogun/verifoxx/internal/graphview"
)

var (
	errGraphOutputRequired = errors.New("graph output path is required")
	errGraphView           = errors.New("graph view must be ast or program")
	errGraphFormat         = errors.New("graph format must be dot, svg, or html")
)

func newGraphCommand(deps dependencies) *cobra.Command {
	var flags sourceFlags
	var view string
	var format string
	var output string
	var force bool
	cmd := &cobra.Command{
		Use:   "graph",
		Short: "Export policy semantic graphs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if output == "" {
				return usageError(errGraphOutputRequired)
			}
			if view != "ast" && view != "program" {
				return usageError(errGraphView)
			}
			if format != "dot" && format != "svg" && format != "html" {
				return usageError(errGraphFormat)
			}
			if deps.writeGraphFile == nil {
				return operationalError(errors.New("graph output is unavailable"))
			}

			inputs, err := loadSources(flags, cmd.InOrStdin(), deps, sourceAll)
			if err != nil {
				return classifyCommandError(err)
			}
			var engine engine
			policy, err := engine.decodePolicy(inputs.policy)
			if err != nil {
				return operationalError(err)
			}
			compiled, err := engine.lowerPolicy(policy)
			if err != nil {
				return operationalError(err)
			}
			if _, err := engine.decodeBatch(compiled, inputs.requests, inputs.evidence); err != nil {
				return operationalError(err)
			}

			astGraph, err := buildASTGraph(policy.document, compiled)
			if err != nil {
				return operationalError(err)
			}
			programGraph, err := buildProgramGraph(compiled)
			if err != nil {
				return operationalError(err)
			}

			var renderer graphview.Renderer
			var rendered []byte
			switch format {
			case "dot":
				if view == "program" {
					rendered, err = renderer.AppendDOT(rendered, &programGraph)
				} else {
					rendered, err = renderer.AppendDOT(rendered, &astGraph)
				}
			case "svg":
				if view == "program" {
					rendered, err = renderer.AppendSVG(rendered, &programGraph)
				} else {
					rendered, err = renderer.AppendSVG(rendered, &astGraph)
				}
			case "html":
				rendered, err = renderer.AppendHTMLView(rendered, &astGraph, &programGraph, view == "program")
			}
			if err != nil {
				return operationalError(err)
			}
			return operationalError(deps.writeGraphFile(output, rendered, force))
		},
	}
	cmd.Flags().StringVar(&view, "view", "ast", "initial graph view (ast or program)")
	cmd.Flags().StringVar(&format, "format", "svg", "output format (dot, svg, or html)")
	cmd.Flags().StringVar(&output, "output", "", "output file path")
	cmd.Flags().BoolVar(&force, "force", false, "replace an existing output file")
	bindSourceFlags(cmd, &flags, sourceAll)
	return cmd
}

func writeAtomicGraphFile(path string, data []byte, force bool) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create graph output: %w", err)
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			_ = temporary.Close()
		}
		_ = os.Remove(temporaryPath)
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure graph output: %w", err)
	}
	if err := writeComplete(temporary, data); err != nil {
		return fmt.Errorf("write graph output: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync graph output: %w", err)
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return fmt.Errorf("close graph output: %w", err)
	}
	closed = true

	if force {
		err = os.Rename(temporaryPath, path)
	} else {
		err = os.Link(temporaryPath, path)
	}
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("graph output %q exists", path)
		}
		return fmt.Errorf("install graph output: %w", err)
	}
	if !force {
		if err := os.Remove(temporaryPath); err != nil {
			return fmt.Errorf("remove graph temporary output: %w", err)
		}
	}

	dir, err := os.Open(directory)
	if err != nil {
		return fmt.Errorf("open graph output directory: %w", err)
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return fmt.Errorf("sync graph output directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close graph output directory: %w", closeErr)
	}
	return nil
}
