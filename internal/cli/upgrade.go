package cli

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/rmscoal/turnecho/internal/install"
	"github.com/rmscoal/turnecho/internal/version"
	"github.com/rmscoal/turnecho/internal/worker"
)

// Upgrade dependencies replaced in tests to avoid network and audio.
var (
	upgradeLatestRelease = install.LatestRelease
	upgradeSelfcheck     = defaultSelfcheckRunner
	upgradeLock          = worker.HoldLock
)

func newUpgradeCommand() *cobra.Command {
	var check, dryRun bool
	command := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade to the latest release.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe, err := installExe()
			if err != nil {
				return commandError(err)
			}
			if install.IsBrewBinary(exe) {
				return commandError(fmt.Errorf("refusing to replace a Homebrew-managed installation: run brew upgrade turnecho"))
			}
			if version.Version == "development" {
				return commandError(fmt.Errorf("development build has no release to compare against: reinstall from a release or rebuild from source"))
			}
			release, err := upgradeLatestRelease()
			if err != nil {
				return commandError(err)
			}
			cmp, err := install.CompareVersions(version.Version, release.Tag)
			switch {
			case err != nil:
				return commandError(fmt.Errorf("cannot order %s against %s: %v", version.Version, release.Tag, err))
			case cmp == 0:
				fmt.Fprintf(cmd.OutOrStdout(), "Already on the latest release (%s).\n", version.Version)
				return nil
			case cmp < 0:
				return commandError(fmt.Errorf("installed %s is newer than latest %s, refusing to downgrade", version.Version, release.Tag))
			}
			if check {
				fmt.Fprintf(cmd.OutOrStdout(), "Upgrade available: %s -> %s.\n", version.Version, release.Tag)
				return nil
			}
			plan, err := install.PlanUpgrade(version.Version, release)
			if err != nil {
				return commandError(err)
			}
			if dryRun {
				plan.Print(cmd.OutOrStdout())
				return nil
			}
			return runUpgrade(cmd, plan)
		},
	}
	command.Flags().BoolVar(&check, "check", false, "Report the latest release without changing anything.")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Print the plan without changing anything.")
	return command
}

func runUpgrade(cmd *cobra.Command, plan install.UpgradePlan) error {
	deps := install.UpgradeDeps{Selfcheck: upgradeSelfcheck, Lock: upgradeLock}
	if err := install.RunUpgrade(plan, deps); err != nil {
		return commandError(err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Upgraded to %s.\n", plan.Target)
	return nil
}

// defaultSelfcheckRunner executes the staged binary's selfcheck, then
// validates its WAV output without trusting it.
func defaultSelfcheckRunner(bin, modelDir, out string) error {
	cmd := exec.Command(bin, "selfcheck", "--model-dir", modelDir, "--output", out)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("selfcheck %s: %v: %s", bin, err, combined)
	}
	return install.ValidateWAV(out)
}
