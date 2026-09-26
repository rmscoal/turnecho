package cli

import (
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/rmscoal/turnecho/internal/config"
	"github.com/rmscoal/turnecho/internal/player"
	"github.com/rmscoal/turnecho/internal/tts"
)

// TestPhrase is the spoken audio check.
const TestPhrase = "TurnEcho is configured and ready."

// backend synthesizes speech. Step 6 swaps this stub for sherpa.
var backend tts.Backend = tts.SilentBackend{}

// play speaks one file through the OS player. Tests replace it.
var play = player.Play

func newTestCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "test",
		Short: "Speak a test phrase.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return mapConfigError(err)
			}
			if err := speakText(TestPhrase, cfg.Voice, cfg.Speed); err != nil {
				return commandError(err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "TurnEcho audio test completed.")
			return nil
		},
	}
}

func newSayCommand() *cobra.Command {
	var output string
	command := &cobra.Command{
		Use:   "say <text>",
		Short: "Speak text aloud or write it to a WAV file.",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return usageError(cmd, fmt.Errorf("accepts 1 arg(s), received %d", len(args)))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return mapConfigError(err)
			}
			samples, err := backend.Synthesize(args[0], cfg.Voice, cfg.Speed)
			if err != nil {
				return commandError(err)
			}
			if output != "" {
				if err := tts.WriteWAV(output, samples); err != nil {
					return commandError(err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s\n", output)
				return nil
			}
			wav, err := writeSpokenWAV(samples)
			if err != nil {
				return commandError(err)
			}
			defer os.Remove(wav)
			if err := play(wav); err != nil {
				return commandError(err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Spoke %d characters.\n", utf8.RuneCountInString(args[0]))
			return nil
		},
	}
	command.Flags().StringVar(&output, "output", "", "Write audio to a WAV file instead of playing it.")
	return command
}

func newStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Silence current speech.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			stopped, err := player.Stop()
			if err != nil {
				return commandError(err)
			}
			if stopped {
				fmt.Fprintln(cmd.OutOrStdout(), "Stopped playback.")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "Nothing is playing.")
			}
			return nil
		},
	}
}

// speakText synthesizes text and plays it through the OS player.
func speakText(text, voice string, speed float64) error {
	samples, err := backend.Synthesize(text, voice, speed)
	if err != nil {
		return err
	}
	wav, err := writeSpokenWAV(samples)
	if err != nil {
		return err
	}
	defer os.Remove(wav)
	return play(wav)
}

func writeSpokenWAV(samples []int16) (string, error) {
	temporary, err := os.CreateTemp("", "turnecho-*.wav")
	if err != nil {
		return "", err
	}
	path := temporary.Name()
	if err := temporary.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	if err := tts.WriteWAV(path, samples); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}
