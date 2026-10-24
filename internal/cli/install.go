package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/rmscoal/turnecho/internal/config"
	"github.com/rmscoal/turnecho/internal/install"
)

// Install dependencies replaced in tests to avoid downloads and audio.
var (
	installModelURL = install.ModelURL
	installOpen     = openOwnedBackend
	installProbe    = install.DefaultProbe
	installExe      = os.Executable
	installRunner   = install.DefaultRunner
)

func newInstallCommand() *cobra.Command {
	var hosts []string
	var update, dryRun bool
	command := &cobra.Command{
		Use:   "install",
		Short: "Install the model, link, and host plugins.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			requested, err := parseHosts(hosts)
			if err != nil {
				return configErrorf("%s", err.Error())
			}
			plan, err := install.ComputePlan(requested, update)
			if err != nil {
				return mapConfigError(err)
			}
			if dryRun {
				plan.Print(cmd.OutOrStdout())
				return nil
			}
			return runInstall(cmd, plan)
		},
	}
	command.Flags().StringSliceVar(&hosts, "host", []string{"all"}, "Host to install: codex, claude (repeatable), or all.")
	command.Flags().BoolVar(&update, "update", false, "Re-download the model even when present.")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Print the plan without changing anything.")
	return command
}

// parseHosts validates --host values. "all" means every detected host CLI
// and cannot combine with named hosts.
func parseHosts(values []string) ([]install.Host, error) {
	var hosts []install.Host
	seenAll := false
	for _, value := range values {
		if value == "all" {
			seenAll = true
			continue
		}
		host, err := install.ParseHost(value)
		if err != nil {
			return nil, err
		}
		hosts = append(hosts, host)
	}
	if seenAll {
		if len(hosts) > 0 {
			return nil, fmt.Errorf("host %q cannot combine with named hosts", "all")
		}
		return nil, nil
	}
	return hosts, nil
}

func runInstall(cmd *cobra.Command, plan install.Plan) error {
	// Model files are a cache, not owned state: a failed install keeps
	// them so the next run resumes without another large download.
	var undo []install.Rollback
	rollback := func() {
		for _, err := range install.RollbackAll(undo) {
			fmt.Fprintf(cmd.ErrOrStderr(), "turnecho: rollback failed: %v\n", err)
		}
	}
	if err := install.EnsureModel(installModelURL, plan.ModelDir, plan.Update); err != nil {
		return commandError(err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Model ready.")
	cfg, err := config.Load()
	if err != nil {
		return mapConfigError(err)
	}
	if err := install.Preflight(cfg.Model, cfg.Voice, cfg.Speed, installOpen, installProbe); err != nil {
		return commandError(err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Runtime check passed.")
	exe, err := installExe()
	if err != nil {
		return commandError(err)
	}
	undoLink, err := install.EnsureLink(plan.LinkPath, exe)
	if err != nil {
		return commandError(err)
	}
	undo = append(undo, undoLink)
	fmt.Fprintf(cmd.OutOrStdout(), "Linked %s.\n", plan.LinkPath)
	for _, host := range plan.Hosts {
		undoHost, err := install.EnsureHost(host, installRunner)
		if err != nil {
			rollback()
			return commandError(err)
		}
		undo = append(undo, undoHost)
		fmt.Fprintf(cmd.OutOrStdout(), "Registered %s plugin.\n", host)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "TurnEcho installed.")
	return nil
}
