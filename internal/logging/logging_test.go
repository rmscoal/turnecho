package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNewDefaultsToInfo(t *testing.T) {
	t.Setenv(LevelEnv, "")
	var output bytes.Buffer
	logger := New(&output)
	if logger.Enabled(nil, slog.LevelDebug) {
		t.Error("debug enabled by default")
	}
	if !logger.Enabled(nil, slog.LevelInfo) {
		t.Error("info disabled by default")
	}
	logger.Info("hello", "key", "value")
	logged := output.String()
	for _, want := range []string{"level=INFO", "msg=hello", "key=value"} {
		if !strings.Contains(logged, want) {
			t.Errorf("log %q missing %q", logged, want)
		}
	}
}

func TestNewReadsLevelEnv(t *testing.T) {
	t.Setenv(LevelEnv, "debug")
	if !New(&bytes.Buffer{}).Enabled(nil, slog.LevelDebug) {
		t.Error("debug level ignored")
	}
	t.Setenv(LevelEnv, "bogus")
	logger := New(&bytes.Buffer{})
	if logger.Enabled(nil, slog.LevelDebug) || !logger.Enabled(nil, slog.LevelInfo) {
		t.Error("invalid level did not fall back to info")
	}
}
