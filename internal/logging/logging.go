// Package logging builds TurnEcho's structured loggers.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

// LevelEnv names the environment variable overriding the log level.
const LevelEnv = "TURNECHO_LOG_LEVEL"

// New returns a text logger writing to output.
func New(output io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{Level: parseLevel()}))
}

func parseLevel() slog.Level {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToUpper(strings.TrimSpace(os.Getenv(LevelEnv))))); err != nil {
		return slog.LevelInfo
	}
	return level
}
