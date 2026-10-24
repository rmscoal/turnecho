package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LinkBinDir is the directory holding the managed turnecho symlink.
const LinkBinDir = ".local/bin"

// LinkName is the managed symlink name.
const LinkName = "turnecho"

// LinkPath returns the managed symlink path under home.
func LinkPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, LinkBinDir, LinkName), nil
}

// EnsureLink points the managed symlink at exe, returning a rollback
// that restores the previous target or removes a link this call created.
// Non-symlinks and links to other programs are refused, never replaced.
func EnsureLink(path, exe string) (Rollback, error) {
	if strings.Contains(exe, "go-build") {
		return nil, fmt.Errorf("refusing to link a temporary go run binary: build first with go build -o turnecho ./cmd/turnecho")
	}
	previous, err := os.Readlink(path)
	switch {
	case err == nil:
		if previous == exe {
			return func() error { return nil }, nil
		}
		if filepath.Base(previous) != LinkName {
			return nil, fmt.Errorf("link %s points at another program, remove it first", path)
		}
	case os.IsNotExist(err):
		previous = ""
	default:
		return nil, fmt.Errorf("inspecting link %s: %w", path, err)
	}
	if err := replaceLink(path, exe); err != nil {
		return nil, err
	}
	if previous == "" {
		return func() error { return removeIfLink(path, exe) }, nil
	}
	return func() error { return replaceLink(path, previous) }, nil
}

// replaceLink swaps the symlink atomically through a temporary link.
func replaceLink(path, target string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".turnecho-link-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	os.Remove(tmpPath)
	if err := os.Symlink(target, tmpPath); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// removeIfLink removes path only while it still points at target.
func removeIfLink(path, target string) error {
	current, err := os.Readlink(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if current != target {
		return fmt.Errorf("link %s changed during install, leaving it alone", path)
	}
	return os.Remove(path)
}
