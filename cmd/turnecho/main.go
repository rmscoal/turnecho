// Command turnecho is the TurnEcho voice companion for coding agents.
package main

import (
	"os"

	"github.com/rmscoal/turnecho/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
