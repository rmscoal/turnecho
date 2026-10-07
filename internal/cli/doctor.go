package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/rmscoal/turnecho/internal/config"
	"github.com/rmscoal/turnecho/internal/paths"
	"github.com/rmscoal/turnecho/internal/player"
	"github.com/rmscoal/turnecho/internal/queue"
	"github.com/rmscoal/turnecho/internal/tts"
)

// doctorReport is the machine-readable runtime health payload.
type doctorReport struct {
	AudioSampleRate int     `json:"audio_sample_rate"`
	ConfigPath      string  `json:"config_path"`
	Model           string  `json:"model"`
	ModelID         string  `json:"model_id"`
	Player          string  `json:"player"`
	Speed           float64 `json:"speed"`
	Status          string  `json:"status"`
	Voice           string  `json:"voice"`
}

func newDoctorCommand() *cobra.Command {
	var asJSON bool
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Check local runtime.",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load()
			if err != nil {
				return mapConfigError(err)
			}
			configPath, err := config.DefaultPath()
			if err != nil {
				return commandError(err)
			}
			dbPath, err := paths.Database()
			if err != nil {
				return commandError(err)
			}
			db, err := queue.Open(dbPath)
			if err != nil {
				return commandError(err)
			}
			db.Close()
			playerPath, err := player.Probe()
			if err != nil {
				return commandError(err)
			}
			engine, err := openBackend(cfg.Model)
			if err != nil {
				return commandError(err)
			}
			defer engine.Close()
			samples, err := engine.Synthesize(TestPhrase, cfg.Voice, cfg.Speed)
			if err != nil {
				return commandError(err)
			}
			if len(samples) == 0 {
				return commandError(fmt.Errorf("TTS runtime produced no audio"))
			}
			if asJSON {
				return printJSON(cmd.OutOrStdout(), doctorReport{
					AudioSampleRate: tts.SampleRate,
					ConfigPath:      configPath,
					Model:           cfg.Model,
					ModelID:         config.Models[cfg.Model],
					Player:          playerPath,
					Speed:           cfg.Speed,
					Status:          "ok",
					Voice:           cfg.Voice,
				})
			}
			fmt.Fprintln(cmd.OutOrStdout(), "TurnEcho configuration and audio runtime are ready.")
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "Print machine-readable JSON.")
	return command
}
