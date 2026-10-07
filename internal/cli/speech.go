package cli

import (
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/rmscoal/turnecho/internal/config"
	"github.com/rmscoal/turnecho/internal/player"
	"github.com/rmscoal/turnecho/internal/speak"
	"github.com/rmscoal/turnecho/internal/tts"
	"github.com/rmscoal/turnecho/internal/worker"
)

// TestPhrase is the spoken audio check.
const TestPhrase = "TurnEcho is configured and ready."

// openBackend loads models only for explicit speech and runtime checks.
// Tests replace it without downloading models or playing sound.
var openBackend = tts.Open

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
			if err := speakText(TestPhrase, cfg); err != nil {
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
			if output != "" {
				engine, err := openOwnedBackend(cfg.Model)
				if err != nil {
					return commandError(err)
				}
				defer engine.Close()
				var samples []int16
				for _, chunk := range speak.Chunks(args[0]) {
					part, err := engine.Synthesize(chunk, cfg.Voice, cfg.Speed)
					if err != nil {
						return commandError(err)
					}
					samples = append(samples, part...)
				}
				if len(samples) == 0 {
					return commandError(fmt.Errorf("speech text produced no audio"))
				}
				if err := tts.WriteWAV(output, samples); err != nil {
					return commandError(err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Wrote %s\n", output)
				return nil
			}
			if err := speakText(args[0], cfg); err != nil {
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
func speakText(text string, cfg config.Config) error {
	engine, err := openOwnedBackend(cfg.Model)
	if err != nil {
		return err
	}
	defer engine.Close()
	chunks := speak.Chunks(text)
	if len(chunks) == 0 {
		return fmt.Errorf("speech text must be nonempty")
	}
	for _, chunk := range chunks {
		samples, err := engine.Synthesize(chunk, cfg.Voice, cfg.Speed)
		if err != nil {
			return err
		}
		wav, err := writeSpokenWAV(samples)
		if err != nil {
			return err
		}
		err = play(wav)
		os.Remove(wav)
		if err != nil {
			return err
		}
	}
	return nil
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

// openOwnedBackend refuses manual speech while another process owns a model.
// Keep ownership through Close, including native teardown.
func openOwnedBackend(model string) (tts.Engine, error) {
	release, err := worker.HoldLock()
	if err != nil {
		return nil, err
	}
	engine, err := openBackend(model)
	if err != nil {
		release()
		_ = worker.ResumePending()
		return nil, err
	}
	return &ownedEngine{Engine: engine, release: release}, nil
}

type ownedEngine struct {
	tts.Engine
	release func()
}

func (e *ownedEngine) Close() {
	if e.release != nil {
		e.Engine.Close()
		e.release()
		e.release = nil
		if err := worker.ResumePending(); err != nil {
			fmt.Fprintf(os.Stderr, "turnecho: resume queued speech: %v\n", err)
		}
	}
}
