package cli

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/rmscoal/turnecho/internal/version"
)

// NewRoot builds the turnecho command tree.
func NewRoot() *cobra.Command {
	root := &cobra.Command{
		Use:     "turnecho",
		Short:   "Speak a short summary after an agent turn.",
		Version: version.Version,
		// Step 7 adds the interactive home menu on TTYs; until then bare
		// turnecho prints help and exits 0 so scripts never hang on input.
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return usageError(cmd, err)
	})
	root.AddCommand(
		newConfigCommand(),
		newEnableCommand(),
		newDisableCommand(),
		newVoicesCommand(),
		newModelsCommand(),
		newDoctorCommand(),
		newTestCommand(),
		newSayCommand(),
		newStopCommand(),
		newHookCommand(),
		newWorkerCommand(),
	)
	return root
}

// Run executes the CLI and returns the process exit code.
func Run(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	root := NewRoot()
	root.SetArgs(argv)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.Execute(); err != nil {
		exitErr := asExitError(err)
		fmt.Fprintln(stderr, exitErr.Message)
		return exitErr.Code
	}
	return ExitOK
}
