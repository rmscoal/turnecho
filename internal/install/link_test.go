package install

import (
	"os"
	"path/filepath"
	"testing"
)

func fakeExe(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readTarget(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

func TestEnsureLinkCreates(t *testing.T) {
	exe := fakeExe(t, "turnecho")
	link := filepath.Join(t.TempDir(), "bin", "turnecho")

	rollback, err := EnsureLink(link, exe)
	if err != nil {
		t.Fatal(err)
	}
	if got := readTarget(t, link); got != exe {
		t.Fatalf("link = %q, want %q", got, exe)
	}
	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("rollback left the created link behind")
	}
}

func TestEnsureLinkIdempotent(t *testing.T) {
	exe := fakeExe(t, "turnecho")
	link := filepath.Join(t.TempDir(), "turnecho")
	if _, err := EnsureLink(link, exe); err != nil {
		t.Fatal(err)
	}
	rollback, err := EnsureLink(link, exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	if got := readTarget(t, link); got != exe {
		t.Fatalf("link = %q, want %q", got, exe)
	}
}

func TestEnsureLinkReplacesAndRestores(t *testing.T) {
	oldExe := fakeExe(t, "turnecho")
	newDir := t.TempDir()
	newExe := filepath.Join(newDir, "turnecho")
	if err := os.WriteFile(newExe, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "turnecho")
	if err := os.Symlink(oldExe, link); err != nil {
		t.Fatal(err)
	}
	rollback, err := EnsureLink(link, newExe)
	if err != nil {
		t.Fatal(err)
	}
	if got := readTarget(t, link); got != newExe {
		t.Fatalf("link = %q, want %q", got, newExe)
	}
	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	if got := readTarget(t, link); got != oldExe {
		t.Fatalf("restored link = %q, want %q", got, oldExe)
	}
}

func TestEnsureLinkRefusesForeignTargets(t *testing.T) {
	link := filepath.Join(t.TempDir(), "turnecho")
	if err := os.Symlink("/usr/bin/other", link); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureLink(link, fakeExe(t, "turnecho")); err == nil {
		t.Fatal("foreign link accepted")
	}
	regular := filepath.Join(t.TempDir(), "regular")
	if err := os.WriteFile(regular, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureLink(regular, fakeExe(t, "turnecho")); err == nil {
		t.Fatal("regular file accepted")
	}
}

func TestEnsureLinkRefusesTempBinaries(t *testing.T) {
	link := filepath.Join(t.TempDir(), "turnecho")
	if _, err := EnsureLink(link, "/tmp/go-build123/b001/exe/turnecho"); err == nil {
		t.Fatal("temporary binary accepted")
	}
}
