// Package cli is TurnEcho's command tree.
package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// Exit codes, keeping v1 parity.
const (
	ExitOK     = 0
	ExitFailed = 1
	ExitConfig = 2
)

// ExitError is a CLI failure with its process exit code.
type ExitError struct {
	Code    int
	Message string
}

// Error implements the error interface.
func (e *ExitError) Error() string {
	return e.Message
}

func asExitError(err error) *ExitError {
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr
	}
	return commandError(err)
}

// configErrorf reports invalid configuration or config command usage.
func configErrorf(format string, args ...any) *ExitError {
	return &ExitError{Code: ExitConfig, Message: "TurnEcho configuration error: " + fmt.Sprintf(format, args...)}
}

// commandError reports a failed command.
func commandError(err error) *ExitError {
	return &ExitError{Code: ExitFailed, Message: "TurnEcho command failed: " + err.Error()}
}

// commandErrorf reports a failed command.
func commandErrorf(format string, args ...any) *ExitError {
	return &ExitError{Code: ExitFailed, Message: "TurnEcho command failed: " + fmt.Sprintf(format, args...)}
}

// usageError reports invalid command usage with exit code 2.
func usageError(cmd *cobra.Command, err error) *ExitError {
	return &ExitError{Code: ExitConfig, Message: "Error: " + err.Error() + "\n\n" + cmd.UsageString()}
}

// noArgs rejects positional arguments with exit code 2.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usageError(cmd, fmt.Errorf("accepts 0 arg(s), received %d", len(args)))
	}
	return nil
}
