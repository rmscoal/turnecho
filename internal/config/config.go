// Package config loads and stores TurnEcho's local configuration.
//
// The v2 schema is fresh: stale v1 files fail strict validation with a clear
// error instead of being migrated.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/rmscoal/turnecho/internal/paths"
)

// SchemaVersion is the only accepted configuration schema version.
const SchemaVersion = 2

// Models maps user-facing model names to pinned release ids.
var Models = map[string]string{
	"kokoro": "kokoro-en-v0_19",
}

// ModelNames lists the supported models in sorted order.
func ModelNames() []string {
	names := make([]string, 0, len(Models))
	for name := range Models {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DefaultModel is used when no configuration file exists.
const DefaultModel = "kokoro"

// Voices lists the selectable Kokoro speakers.
//
// These stable identifiers map directly to the pinned model's speaker IDs.
// The default remains speaker-0 until a listening comparison chooses another.
var Voices = []string{
	"speaker-0",
	"speaker-1",
	"speaker-2",
	"speaker-3",
	"speaker-4",
	"speaker-5",
	"speaker-6",
	"speaker-7",
	"speaker-8",
	"speaker-9",
	"speaker-10",
}

// DefaultVoice is used when no configuration file exists.
const DefaultVoice = "speaker-0"

// DefaultEnabled keeps speech on for fresh installs.
const DefaultEnabled = true

// DefaultSpeed is used when no configuration file exists.
const DefaultSpeed = 1.0

// MinSpeed and MaxSpeed bound the speech rate multiplier.
const (
	MinSpeed = 0.5
	MaxSpeed = 2.0
)

// ResettableKeys are the fields config reset accepts.
var ResettableKeys = []string{"enabled", "model", "voice", "speed"}

// SettableKeys are the fields config set accepts.
var SettableKeys = []string{"model", "voice", "speed"}

// Error reports invalid configuration. The CLI maps it to exit code 2.
type Error struct {
	Reason string
}

// Error implements the error interface.
func (e *Error) Error() string {
	return e.Reason
}

func configError(format string, args ...any) *Error {
	return &Error{Reason: fmt.Sprintf(format, args...)}
}

// Config is the validated user-controlled behavior.
type Config struct {
	Enabled       bool    `json:"enabled"`
	Model         string  `json:"model"`
	SchemaVersion int     `json:"schema_version"`
	Speed         float64 `json:"speed"`
	Voice         string  `json:"voice"`
}

// Defaults returns the configuration used when no file exists.
func Defaults() Config {
	return Config{
		Enabled:       DefaultEnabled,
		Model:         DefaultModel,
		SchemaVersion: SchemaVersion,
		Speed:         DefaultSpeed,
		Voice:         DefaultVoice,
	}
}

// Validate checks a typed configuration.
func Validate(cfg Config) (Config, error) {
	if cfg.SchemaVersion != SchemaVersion {
		return Config{}, configError(
			"Unsupported configuration schema version: %d", cfg.SchemaVersion)
	}
	if _, ok := Models[cfg.Model]; !ok {
		return Config{}, configError(
			"Unsupported model '%s'. Choose from: %s",
			cfg.Model, strings.Join(ModelNames(), ", "))
	}
	if !slices.Contains(Voices, cfg.Voice) {
		return Config{}, configError(
			"Unsupported voice '%s'. Choose from: %s",
			cfg.Voice, strings.Join(Voices, ", "))
	}
	if math.IsNaN(cfg.Speed) || math.IsInf(cfg.Speed, 0) ||
		cfg.Speed < MinSpeed || cfg.Speed > MaxSpeed {
		return Config{}, configError(
			"Configuration field 'speed' must be between %g and %g.",
			MinSpeed, MaxSpeed)
	}
	return cfg, nil
}

func fromPayload(payload map[string]any) (Config, error) {
	known := []string{"schema_version", "enabled", "model", "voice", "speed"}

	var unknown []string
	for key := range payload {
		if !slices.Contains(known, key) {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return Config{}, configError("Unknown configuration field(s): %s", strings.Join(unknown, ", "))
	}

	var missing []string
	for _, key := range known {
		if _, ok := payload[key]; !ok {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return Config{}, configError("Missing configuration field(s): %s", strings.Join(missing, ", "))
	}

	version, err := payloadInt(payload, "schema_version")
	if err != nil {
		return Config{}, err
	}
	enabled, err := payloadBool(payload, "enabled")
	if err != nil {
		return Config{}, err
	}
	model, err := payloadString(payload, "model")
	if err != nil {
		return Config{}, err
	}
	voice, err := payloadString(payload, "voice")
	if err != nil {
		return Config{}, err
	}
	speed, err := payloadNumber(payload, "speed")
	if err != nil {
		return Config{}, err
	}

	return Validate(Config{
		Enabled:       enabled,
		Model:         model,
		SchemaVersion: version,
		Speed:         speed,
		Voice:         voice,
	})
}

// payloadInt reads an integer field decoded from JSON (always a float64).
func payloadInt(payload map[string]any, key string) (int, error) {
	value, ok := payload[key].(float64)
	if !ok || value != math.Trunc(value) {
		return 0, configError("Configuration field '%s' must be an integer.", key)
	}
	return int(value), nil
}

func payloadBool(payload map[string]any, key string) (bool, error) {
	value, ok := payload[key].(bool)
	if !ok {
		return false, configError("Configuration field '%s' must be a boolean.", key)
	}
	return value, nil
}

func payloadString(payload map[string]any, key string) (string, error) {
	value, ok := payload[key].(string)
	if !ok {
		return "", configError("Configuration field '%s' must be a string.", key)
	}
	return value, nil
}

func payloadNumber(payload map[string]any, key string) (float64, error) {
	value, ok := payload[key].(float64)
	if !ok {
		return 0, configError("Configuration field '%s' must be a number.", key)
	}
	return value, nil
}

// DefaultPath returns the configuration file path.
func DefaultPath() (string, error) {
	return paths.ConfigFile()
}

// Load reads the default configuration file.
func Load() (Config, error) {
	path, err := DefaultPath()
	if err != nil {
		return Config{}, err
	}
	return LoadFile(path)
}

// LoadFile reads strict configuration, using defaults only when no file exists.
func LoadFile(path string) (Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Defaults(), nil
		}
		return Config{}, configError("Cannot read TurnEcho configuration: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(content, &payload); err != nil {
		return Config{}, configError("Cannot read TurnEcho configuration: %v", err)
	}
	return fromPayload(payload)
}

func lockPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), paths.ConfigLockName)
}

func withLock(configPath string, run func() error) error {
	if err := paths.EnsurePrivateDir(filepath.Dir(configPath)); err != nil {
		return err
	}
	lockFile, err := paths.OpenPrivateFile(lockPath(configPath), os.O_CREATE|os.O_RDWR)
	if err != nil {
		return err
	}
	defer lockFile.Close()
	if err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
	return run()
}

func writeUnvalidated(config Config, configPath string) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(config); err != nil {
		return err
	}
	return writeFileAtomic(configPath, buffer.Bytes())
}

// writeFileAtomic persists data through a temp file, so a crash never leaves
// a half-written configuration behind.
func writeFileAtomic(configPath string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(configPath), ".config.json.")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	closed := false
	defer func() {
		if !closed {
			temporary.Close()
		}
	}()

	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	closed = true
	return os.Rename(temporaryPath, configPath)
}

// Save validates and atomically writes the default configuration.
func Save(config Config) error {
	path, err := DefaultPath()
	if err != nil {
		return err
	}
	return SaveFile(config, path)
}

// SaveFile validates and atomically writes one configuration file.
func SaveFile(config Config, path string) error {
	validated, err := Validate(config)
	if err != nil {
		return err
	}
	return withLock(path, func() error {
		return writeUnvalidated(validated, path)
	})
}

// Update reads, mutates, and persists the default configuration under one lock.
func Update(change func(*Config)) (Config, error) {
	path, err := DefaultPath()
	if err != nil {
		return Config{}, err
	}
	return UpdateFile(path, change)
}

// UpdateFile reads, mutates, and persists one configuration file under one lock.
func UpdateFile(path string, change func(*Config)) (Config, error) {
	var updated Config
	err := withLock(path, func() error {
		current, err := LoadFile(path)
		if err != nil {
			return err
		}
		change(&current)
		validated, err := Validate(current)
		if err != nil {
			return err
		}
		updated = validated
		return writeUnvalidated(validated, path)
	})
	if err != nil {
		return Config{}, err
	}
	return updated, nil
}

// Reset restores one setting (or everything when key is empty).
func Reset(key string) (Config, error) {
	path, err := DefaultPath()
	if err != nil {
		return Config{}, err
	}
	return ResetFile(path, key)
}

// ResetFile restores one setting (or everything when key is empty) in one file.
func ResetFile(path, key string) (Config, error) {
	if key != "" && !slices.Contains(ResettableKeys, key) {
		return Config{}, configError("Unknown reset field: %s", key)
	}
	var updated Config
	err := withLock(path, func() error {
		if key == "" {
			updated = Defaults()
		} else {
			current, err := LoadFile(path)
			if err != nil {
				return err
			}
			resetKey(&current, key, Defaults())
			updated = current
		}
		return writeUnvalidated(updated, path)
	})
	if err != nil {
		return Config{}, err
	}
	return updated, nil
}

// resetKey restores one setting to its default value.
func resetKey(current *Config, key string, defaults Config) {
	switch key {
	case "enabled":
		current.Enabled = defaults.Enabled
	case "model":
		current.Model = defaults.Model
	case "voice":
		current.Voice = defaults.Voice
	case "speed":
		current.Speed = defaults.Speed
	}
}
