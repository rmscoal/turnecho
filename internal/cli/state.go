package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rmscoal/turnecho/internal/config"
)

func newEnableCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "enable",
		Short: "Enable future summaries.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return setEnabled(cmd, true)
		},
	}
}

func newDisableCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "disable",
		Short: "Disable future summaries.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return setEnabled(cmd, false)
		},
	}
}

func setEnabled(cmd *cobra.Command, enabled bool) error {
	updated, err := config.Update(func(current *config.Config) {
		current.Enabled = enabled
	})
	if err != nil {
		return mapConfigError(err)
	}
	if updated.Enabled {
		fmt.Fprintln(cmd.OutOrStdout(), "TurnEcho enabled.")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "TurnEcho disabled.")
	}
	return nil
}
