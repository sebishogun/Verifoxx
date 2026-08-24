package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

func newMigrationCommand(deps dependencies) *cobra.Command {
	var wait bool
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply embedded PostgreSQL migrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.migrate == nil {
				return operationalError(errors.New("migration runtime is unavailable"))
			}
			if err := deps.migrate(cmd.Context()); err != nil {
				return operationalError(err)
			}
			if wait {
				<-cmd.Context().Done()
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&wait, "wait", false, "remain healthy until process cancellation")
	return cmd
}

func newMigrationHealthCommand(deps dependencies) *cobra.Command {
	return &cobra.Command{
		Use:    "migration-healthcheck",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.migrationHealth == nil {
				return operationalError(errors.New("migration health runtime is unavailable"))
			}
			return operationalError(deps.migrationHealth(cmd.Context()))
		},
	}
}

func newServeCommand(deps dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Serve the HTTP and gRPC policy APIs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.serve == nil {
				return operationalError(errors.New("service runtime is unavailable"))
			}
			return operationalError(deps.serve(cmd.Context()))
		},
	}
}

func newHealthcheckCommand(deps dependencies) *cobra.Command {
	return &cobra.Command{
		Use:    "healthcheck",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deps.healthcheck == nil {
				return operationalError(errors.New("healthcheck runtime is unavailable"))
			}
			return operationalError(deps.healthcheck(cmd.Context()))
		},
	}
}
