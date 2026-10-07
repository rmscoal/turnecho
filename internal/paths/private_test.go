package paths

import (
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivatePathsRejectSymlinksAndNonRegularFiles(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(link); err == nil {
		t.Fatal("symlink directory accepted")
	}
	file := filepath.Join(target, "file")
	os.WriteFile(file, []byte("keep"), 0644)
	fileLink := filepath.Join(target, "file-link")
	os.Symlink(file, fileLink)
	if f, err := OpenPrivateFile(fileLink, os.O_RDWR); err == nil {
		f.Close()
		t.Fatal("symlink file accepted")
	}
	fifo := filepath.Join(target, "fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if f, err := OpenPrivateFile(fifo, os.O_RDWR); err == nil {
		f.Close()
		t.Fatal("FIFO accepted")
	}
	data, _ := os.ReadFile(file)
	if string(data) != "keep" {
		t.Fatal("target modified")
	}
}
