package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/rmscoal/turnecho/internal/config"
	"github.com/rmscoal/turnecho/internal/hosts"
	"github.com/rmscoal/turnecho/internal/paths"
	"github.com/rmscoal/turnecho/internal/queue"
	"github.com/rmscoal/turnecho/internal/speak"
	"github.com/rmscoal/turnecho/internal/worker"
)

// spawnWorker starts the background worker. Tests replace it.
var spawnWorker = worker.SpawnBackground

func newHookCommand() *cobra.Command {
	command := &cobra.Command{
		Use:    "hook",
		Short:  "Run a host hook.",
		Hidden: true,
	}
	command.PersistentFlags().String("host", "", "Force host parsing (codex or claude_code).")
	command.AddCommand(
		&cobra.Command{
			Use:   "prompt",
			Short: "Handle a UserPromptSubmit event.",
			RunE: func(cmd *cobra.Command, _ []string) error {
				// Hooks always succeed: every failure prints the host
				// default output so the agent response stays unchanged.
				forced, hasForced := forcedHost(cmd)
				handlePrompt(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), forced, hasForced)
				return nil
			},
		},
		&cobra.Command{
			Use:   "stop",
			Short: "Handle a Stop event.",
			RunE: func(cmd *cobra.Command, _ []string) error {
				forced, hasForced := forcedHost(cmd)
				handleStop(cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), forced, hasForced)
				return nil
			},
		},
	)
	return command
}

func forcedHost(cmd *cobra.Command) (string, bool) {
	if !cmd.Flags().Changed("host") {
		return "", false
	}
	forced, _ := cmd.Flags().GetString("host")
	return forced, true
}

// readPayload parses hook stdin, falling back to an empty payload.
func readPayload(stdin io.Reader, stderr io.Writer) map[string]any {
	content, err := io.ReadAll(stdin)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil
	}
	var raw any
	if err := json.Unmarshal(content, &raw); err != nil {
		fmt.Fprintln(stderr, err)
		return nil
	}
	payload, _ := raw.(map[string]any)
	return payload
}

func printDefault(stdout io.Writer) {
	fmt.Fprintln(stdout, hosts.DefaultOutput)
}

// handlePrompt injects the summary instruction for UserPromptSubmit events.
func handlePrompt(stdin io.Reader, stdout, stderr io.Writer, forced string, hasForced bool) {
	payload := readPayload(stdin, stderr)
	host := hosts.Detect(payload, forced, hasForced)
	var isPrompt bool
	switch host {
	case hosts.ClaudeCode:
		isPrompt = hosts.IsClaudePromptSubmit(payload)
	case hosts.Codex:
		isPrompt = hosts.IsCodexPromptSubmit(payload)
	default:
		printDefault(stdout)
		return
	}
	if !isPrompt {
		printDefault(stdout)
		return
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, err)
		printDefault(stdout)
		return
	}
	if !cfg.Enabled {
		printDefault(stdout)
		return
	}
	if host == hosts.ClaudeCode {
		fmt.Fprintln(stdout, hosts.RenderClaudePromptSubmit(speak.Instruction))
	} else {
		fmt.Fprintln(stdout, hosts.RenderCodexPromptSubmit(speak.Instruction))
	}
	// Warm the worker before the Stop event arrives.
	if err := spawnWorker(); err != nil {
		fmt.Fprintln(stderr, err)
	}
}

// handleStop validates the summary marker and queues one speech job.
func handleStop(stdin io.Reader, stdout, stderr io.Writer, forced string, hasForced bool) {
	payload := readPayload(stdin, stderr)
	host := hosts.Detect(payload, forced, hasForced)
	var event hosts.Event
	var ok bool
	switch host {
	case hosts.ClaudeCode:
		event, ok = hosts.ParseClaudeStop(payload)
	case hosts.Codex:
		event, ok = hosts.ParseCodexStop(payload)
	default:
		printDefault(stdout)
		return
	}
	if !ok {
		printDefault(stdout)
		return
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(stderr, err)
		printDefault(stdout)
		return
	}
	if !cfg.Enabled {
		printDefault(stdout)
		return
	}
	summary, valid := speak.ExtractSummary(event.Message)
	if !valid {
		printDefault(stdout)
		return
	}
	dbPath, err := paths.Database()
	if err != nil {
		fmt.Fprintln(stderr, err)
		printDefault(stdout)
		return
	}
	db, err := queue.Open(dbPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		printDefault(stdout)
		return
	}
	_, err = db.Insert(event.Host, event.SessionID, event.TurnID, summary, cfg.Voice, cfg.Speed)
	db.Close()
	if err != nil {
		fmt.Fprintln(stderr, err)
		printDefault(stdout)
		return
	}
	if err := spawnWorker(); err != nil {
		fmt.Fprintln(stderr, err)
	}
	printDefault(stdout)
}
