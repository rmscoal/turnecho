package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/rmscoal/turnecho/internal/config"
	"github.com/rmscoal/turnecho/internal/install"
	"github.com/rmscoal/turnecho/internal/worker"
)

// Install dependencies replaced in tests to avoid downloads and audio.
var (
	installModelURL = install.ModelURL
	installModelSHA = install.PinnedModelSHA256
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
	var undo []install.Rollback
	rollback := func() {
		for _, err := range install.RollbackAll(undo) {
			fmt.Fprintf(cmd.ErrOrStderr(), "turnecho: rollback failed: %v\n", err)
		}
	}
	// Prepare the model without touching live state, then activate it and
	// link it under one worker lock so concurrent commands and speech
	// never observe a half-swapped installation.
	var commitModel func() error
	var staged string
	if !install.ModelReady(plan.ModelDir, installModelSHA) || plan.Update {
		var discardStaged func()
		var err error
		staged, discardStaged, err = install.StageModel(installModelURL, plan.Model, installModelSHA, filepath.Dir(plan.ModelDir))
		if err != nil {
			return commandError(err)
		}
		// Runs even after a successful activation: the staged dir has moved
		// by then, and this removes the leftover staging area instead.
		defer discardStaged()
	}
	release, err := worker.HoldLock()
	if err != nil {
		return commandError(speechBusy(err))
	}
	if staged != "" {
		var undoModel install.Rollback
		commitModel, undoModel, err = install.ActivateModel(staged, plan.ModelDir)
		if err != nil {
			release()
			return commandError(err)
		}
		undo = append(undo, undoModel)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Model ready.")
	exe, err := installExe()
	if err != nil {
		release()
		rollback()
		return commandError(err)
	}
	undoLink, err := install.EnsureLink(plan.LinkPath, exe)
	release()
	if err != nil {
		rollback()
		return commandError(err)
	}
	undo = append(undo, undoLink)
	fmt.Fprintf(cmd.OutOrStdout(), "Linked %s.\n", plan.LinkPath)
	cfg, err := config.Load()
	if err != nil {
		rollback()
		return mapConfigError(err)
	}
	if err := install.Preflight(cfg.Model, cfg.Voice, cfg.Speed, installOpen, installProbe); err != nil {
		rollback()
		return commandError(err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Runtime check passed.")
	for _, host := range plan.Hosts {
		undoHost, err := install.EnsureHost(host, installRunner)
		if err != nil {
			rollback()
			return commandError(err)
		}
		undo = append(undo, undoHost)
		fmt.Fprintf(cmd.OutOrStdout(), "Registered %s plugin.\n", host)
	}
	if commitModel != nil {
		if err := commitModel(); err != nil {
			rollback()
			return commandError(err)
		}
	}
	fmt.Fprintln(cmd.OutOrStdout(), "TurnEcho installed.")
	return nil
}

// speechBusy turns a contested worker lock into a retry message.
func speechBusy(err error) error {
	return fmt.Errorf("speech in progress, retry shortly: %v", err)
}
