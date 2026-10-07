package paths

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// EnsurePrivateDir tightens existing application directories too. Reject a
// symlink or another user's directory before changing permissions.
func EnsurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("application directory is owned by another user: %s", path)
	}
	return unix.Fchmod(fd, 0700)
}

// OpenPrivateFile opens owned regular state files without following symlinks.
func OpenPrivateFile(path string, flags int) (*os.File, error) {
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		unix.Close(fd)
		return nil, err
	}
	if stat.Uid != uint32(os.Getuid()) || stat.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, fmt.Errorf("unsafe application state file: %s", path)
	}
	if err := unix.Fchmod(fd, 0600); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
