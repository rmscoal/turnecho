// Package paths resolves TurnEcho's local data files.
//
// Every path derives from the user's home directory at call time so tests
// can redirect them by overriding HOME.
package paths

import (
	"os"
	"path/filepath"
)

// ConfigDirName is the application directory under ~/.config.
const ConfigDirName = "turnecho"

// ConfigFileName is the JSON configuration file.
const ConfigFileName = "config.json"

// ConfigLockName serializes configuration reads and writes.
const ConfigLockName = "config.lock"

// DatabaseFileName is the SQLite queue database.
const DatabaseFileName = "turnecho.db"

// WorkerLockName serializes worker processes across hosts and sessions.
const WorkerLockName = "worker.lock"

// WorkerLogName collects detached worker output.
const WorkerLogName = "worker.log"

// PlayerPidName records the active playback process for turnecho stop.
const PlayerPidName = "player.pid"

// ConfigDir returns ~/.config/turnecho.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", ConfigDirName), nil
}

func joinConfigDir(name string) (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// ConfigFile returns the configuration file path.
func ConfigFile() (string, error) {
	return joinConfigDir(ConfigFileName)
}

// ConfigLock returns the configuration lock file path.
func ConfigLock() (string, error) {
	return joinConfigDir(ConfigLockName)
}

// Database returns the queue database path.
func Database() (string, error) {
	return joinConfigDir(DatabaseFileName)
}

// WorkerLock returns the worker lock file path.
func WorkerLock() (string, error) {
	return joinConfigDir(WorkerLockName)
}

// WorkerLog returns the worker log file path.
func WorkerLog() (string, error) {
	return joinConfigDir(WorkerLogName)
}

// PlayerPid returns the playback pid file path.
func PlayerPid() (string, error) {
	return joinConfigDir(PlayerPidName)
}
