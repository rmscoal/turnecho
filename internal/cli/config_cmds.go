package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/rmscoal/turnecho/internal/config"
)

func printJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func printConfig(writer io.Writer, cfg config.Config) {
	fmt.Fprintf(writer, "enabled: %s\n", strconv.FormatBool(cfg.Enabled))
	fmt.Fprintf(writer, "model: %s\n", cfg.Model)
	fmt.Fprintf(writer, "voice: %s\n", cfg.Voice)
	fmt.Fprintf(writer, "speed: %s\n", strconv.FormatFloat(cfg.Speed, 'g', -1, 64))
}

func mapConfigError(err error) *ExitError {
	var configErr *config.Error
	if errors.As(err, &configErr) {
		return configErrorf("%s", configErr.Error())
	}
	return commandError(err)
}

func newConfigCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration.",
	}
	command.AddCommand(
		newConfigShowCommand(),
		newConfigPathCommand(),
		newConfigSetCommand(),
		newConfigResetCommand(),
	)
	return command
}

func newConfigShowCommand() *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "show",
		Short: "Show configuration.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return mapConfigError(err)
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), cfg)
			}
			printConfig(cmd.OutOrStdout(), cfg)
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "Print machine-readable JSON.")
	return command
}

func newConfigPathCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Show config path.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := config.DefaultPath()
			if err != nil {
				return commandError(err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
}

func newConfigSetCommand() *cobra.Command {
	return &cobra.Command{
		// Step 7 adds value-less pickers; until then the value is required.
		Use:   "set <model|voice|speed> <value>",
		Short: "Set one value.",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return usageError(cmd, fmt.Errorf("accepts 2 arg(s), received %d", len(args)))
			}
			if !slices.Contains(config.SettableKeys, args[0]) {
				return usageError(cmd, fmt.Errorf("unknown config key %q (choose from: model, voice, speed)", args[0]))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			if key == "speed" {
				speed, parseErr := strconv.ParseFloat(value, 64)
				if parseErr != nil {
					return configErrorf("Speed must be a number.")
				}
				updated, err := config.Update(func(current *config.Config) {
					current.Speed = speed
				})
				if err != nil {
					return mapConfigError(err)
				}
				printConfig(cmd.OutOrStdout(), updated)
				return nil
			}
			updated, err := config.Update(func(current *config.Config) {
				if key == "model" {
					current.Model = value
				} else {
					current.Voice = value
				}
			})
			if err != nil {
				return mapConfigError(err)
			}
			printConfig(cmd.OutOrStdout(), updated)
			return nil
		},
	}
}

func newConfigResetCommand() *cobra.Command {
	var all bool
	command := &cobra.Command{
		Use:   "reset <enabled|model|voice|speed|--all>",
		Short: "Reset values.",
		Args: func(cmd *cobra.Command, args []string) error {
			if all && len(args) > 0 {
				return usageError(cmd, fmt.Errorf("pass a key or --all, not both"))
			}
			if !all && len(args) != 1 {
				return usageError(cmd, fmt.Errorf("pass a key or --all"))
			}
			if len(args) == 1 && !slices.Contains(config.ResettableKeys, args[0]) {
				return usageError(cmd, fmt.Errorf("unknown reset key %q (choose from: enabled, model, voice, speed)", args[0]))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			key := ""
			if !all {
				key = args[0]
			}
			updated, err := config.Reset(key)
			if err != nil {
				return mapConfigError(err)
			}
			printConfig(cmd.OutOrStdout(), updated)
			return nil
		},
	}
	command.Flags().BoolVar(&all, "all", false, "Reset every setting.")
	return command
}
