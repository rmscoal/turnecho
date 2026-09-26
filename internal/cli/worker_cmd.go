package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/rmscoal/turnecho/internal/paths"
	"github.com/rmscoal/turnecho/internal/queue"
	"github.com/rmscoal/turnecho/internal/worker"
)

func newWorkerCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "worker",
		Short:  "Speak queued summaries.",
		Hidden: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			dbPath, err := paths.Database()
			if err != nil {
				return commandError(err)
			}
			db, err := queue.Open(dbPath)
			if err != nil {
				return commandError(err)
			}
			defer db.Close()
			if err := worker.Process(worker.Dependencies{
				Queue:   db,
				Backend: backend,
				Play:    play,
			}); err != nil {
				if errors.Is(err, worker.ErrAlreadyRunning) {
					return nil
				}
				return commandError(err)
			}
			return nil
		},
	}
}
