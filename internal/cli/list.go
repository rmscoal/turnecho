package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rmscoal/turnecho/internal/config"
)

func newVoicesCommand() *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "voices",
		Short: "List supported voices.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				return printJSON(cmd.OutOrStdout(), map[string][]string{"voices": config.Voices})
			}
			for _, voice := range config.Voices {
				suffix := ""
				if voice == config.DefaultVoice {
					suffix = " (default)"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s%s\n", voice, suffix)
			}
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "Print machine-readable JSON.")
	return command
}

func newModelsCommand() *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "models",
		Short: "List supported models.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if asJSON {
				return printJSON(cmd.OutOrStdout(), struct {
					Default string            `json:"default"`
					Models  map[string]string `json:"models"`
				}{Default: config.DefaultModel, Models: config.Models})
			}
			for _, name := range config.ModelNames() {
				suffix := ""
				if name == config.DefaultModel {
					suffix = " (default)"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s%s: %s\n", name, suffix, config.Models[name])
			}
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "Print machine-readable JSON.")
	return command
}
