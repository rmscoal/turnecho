package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rmscoal/turnecho/internal/config"
	"github.com/rmscoal/turnecho/internal/install"
	"github.com/rmscoal/turnecho/internal/tts"
)

// selfcheckOpen loads a model dir without the worker lock. Upgrade runs the
// staged binary's selfcheck while holding the lock itself, so acquiring it
// here would deadlock and resuming workers would break the transaction.
var selfcheckOpen install.OpenFunc = tts.OpenDir

func newSelfcheckCommand() *cobra.Command {
	var modelDir, output, voice string
	var speed float64
	command := &cobra.Command{
		Use:    "selfcheck",
		Short:  "Verify a model directory (upgrade machinery).",
		Hidden: true,
		Args:   noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if modelDir == "" || output == "" {
				return usageError(cmd, fmt.Errorf("selfcheck requires --model-dir and --output"))
			}
			engine, err := selfcheckOpen(modelDir)
			if err != nil {
				return commandError(err)
			}
			defer engine.Close()
			samples, err := engine.Synthesize(TestPhrase, voice, speed)
			if err != nil {
				return commandError(err)
			}
			if len(samples) == 0 {
				return commandError(fmt.Errorf("TTS runtime produced no audio"))
			}
			if err := tts.WriteWAV(output, samples); err != nil {
				return commandError(err)
			}
			return printJSON(cmd.OutOrStdout(), struct {
				OK         bool   `json:"ok"`
				Samples    int    `json:"samples"`
				SampleRate int    `json:"sample_rate"`
				ModelDir   string `json:"model_dir"`
			}{OK: true, Samples: len(samples), SampleRate: tts.SampleRate, ModelDir: modelDir})
		},
	}
	command.Flags().StringVar(&modelDir, "model-dir", "", "Model directory to verify.")
	command.Flags().StringVar(&output, "output", "", "Write verification audio here.")
	command.Flags().StringVar(&voice, "voice", config.DefaultVoice, "Voice to synthesize.")
	command.Flags().Float64Var(&speed, "speed", 1, "Speech speed.")
	return command
}
