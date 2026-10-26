package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rmscoal/turnecho/internal/install"
)

func newUninstallCommand() *cobra.Command {
	var hosts []string
	var dryRun bool
	command := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the link, runtimes, and host plugins.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			requested, err := parseHosts(hosts)
			if err != nil {
				return configErrorf("%s", err.Error())
			}
			exe, err := installExe()
			if err != nil {
				return commandError(err)
			}
			plan, err := install.PlanUninstall(requested, exe, installRunner)
			if err != nil {
				return commandError(err)
			}
			if dryRun {
				printUninstallPlan(cmd, plan)
				return nil
			}
			return runUninstall(cmd, plan, installRunner)
		},
	}
	command.Flags().StringSliceVar(&hosts, "host", []string{"all"}, "Host to remove: codex, claude (repeatable), or all.")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Print the plan without changing anything.")
	return command
}

func printUninstallPlan(cmd *cobra.Command, plan install.UninstallPlan) {
	out := cmd.OutOrStdout()
	for _, host := range plan.Hosts {
		fmt.Fprintf(out, "Host: %s\n", host)
	}
	if plan.RemoveLink {
		fmt.Fprintf(out, "Link: remove %s\n", plan.LinkPath)
	} else if plan.PreservedLink != "" {
		fmt.Fprintf(out, "Link: keep %s (not managed)\n", plan.PreservedLink)
	}
	for _, dir := range plan.Runtimes {
		fmt.Fprintf(out, "Runtime: remove %s\n", dir)
	}
	for _, dir := range plan.PreservedRuntimes {
		fmt.Fprintf(out, "Runtime: keep %s (not managed)\n", dir)
	}
}

func runUninstall(cmd *cobra.Command, plan install.UninstallPlan, run install.Runner) error {
	out := cmd.OutOrStdout()
	exe, err := installExe()
	if err != nil {
		return commandError(err)
	}
	report, err := plan.Execute(run, exe)
	if err != nil {
		return commandError(err)
	}
	for _, host := range report.RemovedHosts {
		fmt.Fprintf(out, "Removed %s plugin.\n", host)
	}
	if report.RemovedLink {
		fmt.Fprintf(out, "Removed %s.\n", plan.LinkPath)
	} else if plan.PreservedLink != "" {
		fmt.Fprintf(out, "Kept %s (not managed).\n", plan.PreservedLink)
	}
	for _, dir := range report.RemovedRuntimes {
		fmt.Fprintf(out, "Removed %s.\n", dir)
	}
	for _, dir := range plan.PreservedRuntimes {
		fmt.Fprintf(out, "Kept %s (not managed).\n", dir)
	}
	fmt.Fprintln(out, "TurnEcho uninstalled.")
	return nil
}
