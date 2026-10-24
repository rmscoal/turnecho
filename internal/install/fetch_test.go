package install

import (
	"archive/tar"
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureArchive packs dir as a .tar.bz2 using the system tar.
// Model archives are large upstream tarballs; tests use tiny equivalents.
func fixtureArchive(t *testing.T, parent, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("system tar not available")
	}
	out := filepath.Join(t.TempDir(), "kokoro.tar.bz2")
	cmd := exec.Command("tar", "-cjf", out, "-C", parent, dir)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return out
}

// fixtureModelTree writes the required model files under parent/name.
func fixtureModelTree(t *testing.T, parent, name string) string {
	t.Helper()
	root := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(root, "espeak-ng-data"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"model.onnx", "voices.bin", "tokens.txt"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func serveFile(t *testing.T, path string, hits *int) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits++
		http.ServeFile(w, r, path)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestEnsureModelDownloadsAndSkips(t *testing.T) {
	parent := t.TempDir()
	fixtureModelTree(t, parent, "kokoro-en-v0_19")
	archive := fixtureArchive(t, parent, "kokoro-en-v0_19")
	var hits int
	url := serveFile(t, archive, &hits)
	dest := filepath.Join(t.TempDir(), "runtimes", "kokoro-en-v0_19")

	if err := EnsureModel(url, dest, false); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("downloads = %d, want 1", hits)
	}
	if _, err := os.Stat(filepath.Join(dest, "SOURCE.sha256")); err != nil {
		t.Fatalf("missing hash record: %v", err)
	}
	if err := EnsureModel(url, dest, false); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("second run downloaded again: hits = %d", hits)
	}
}

func TestEnsureModelForceRedownloads(t *testing.T) {
	parent := t.TempDir()
	fixtureModelTree(t, parent, "kokoro-en-v0_19")
	archive := fixtureArchive(t, parent, "kokoro-en-v0_19")
	var hits int
	url := serveFile(t, archive, &hits)
	dest := filepath.Join(t.TempDir(), "kokoro-en-v0_19")

	if err := EnsureModel(url, dest, false); err != nil {
		t.Fatal(err)
	}
	if err := EnsureModel(url, dest, true); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("downloads = %d, want 2", hits)
	}
}

func TestEnsureModelRejectsIncomplete(t *testing.T) {
	parent := t.TempDir()
	bad := filepath.Join(parent, "m")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "model.onnx"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := fixtureArchive(t, parent, "m")
	var hits int
	url := serveFile(t, archive, &hits)
	dest := filepath.Join(t.TempDir(), "dest")

	if err := EnsureModel(url, dest, false); err == nil {
		t.Fatal("incomplete model accepted")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("incomplete model left a destination behind")
	}
}

// tarEntry is one member of a hand-built fixture archive.
type tarEntry struct {
	name string
	body string
	flag byte // tar.TypeReg, tar.TypeDir, or tar.TypeSymlink (body is the target)
}

func customTarball(t *testing.T, entries []tarEntry) string {
	t.Helper()
	var raw bytes.Buffer
	writer := tar.NewWriter(&raw)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Mode: 0o600}
		header.Typeflag = entry.flag
		if entry.flag == tar.TypeSymlink {
			header.Linkname = entry.body
		} else {
			header.Size = int64(len(entry.body))
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if entry.flag == tar.TypeReg {
			if _, err := writer.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("bzip2"); err != nil {
		t.Skip("bzip2 not available")
	}
	compress := exec.Command("bzip2", "-c")
	compress.Stdin = &raw
	compressed, err := compress.Output()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "custom.tar.bz2")
	if err := os.WriteFile(out, compressed, 0o600); err != nil {
		t.Fatal(err)
	}
	return out
}

func modelEntries(extra ...tarEntry) []tarEntry {
	entries := []tarEntry{
		{name: "kokoro-en-v0_19/", flag: tar.TypeDir},
		{name: "kokoro-en-v0_19/espeak-ng-data/", flag: tar.TypeDir},
		{name: "kokoro-en-v0_19/model.onnx", body: "model", flag: tar.TypeReg},
		{name: "kokoro-en-v0_19/voices.bin", body: "voices", flag: tar.TypeReg},
		{name: "kokoro-en-v0_19/tokens.txt", body: "tokens", flag: tar.TypeReg},
	}
	return append(entries, extra...)
}

func TestEnsureModelRejectsSymlink(t *testing.T) {
	archive := customTarball(t, modelEntries(tarEntry{
		name: "kokoro-en-v0_19/evil", body: "/etc/passwd", flag: tar.TypeSymlink,
	}))
	var hits int
	url := serveFile(t, archive, &hits)
	dest := filepath.Join(t.TempDir(), "dest")

	err := EnsureModel(url, dest, false)
	if err == nil || !strings.Contains(err.Error(), "link") {
		t.Fatalf("error = %v, want link rejection", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatal("symlink archive left a destination behind")
	}
}

func TestEnsureModelSkipsAppleDouble(t *testing.T) {
	archive := customTarball(t, modelEntries(
		tarEntry{name: "._kokoro-en-v0_19", body: "fork", flag: tar.TypeReg},
		tarEntry{name: "kokoro-en-v0_19/._model.onnx", body: "fork", flag: tar.TypeReg},
	))
	var hits int
	url := serveFile(t, archive, &hits)
	dest := filepath.Join(t.TempDir(), "dest")

	if err := EnsureModel(url, dest, false); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureModelRejectsHTTPError(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		http.Error(w, "gone", http.StatusGone)
	}))
	t.Cleanup(server.Close)
	dest := filepath.Join(t.TempDir(), "dest")

	err := EnsureModel(server.URL, dest, false)
	if err == nil || !strings.Contains(err.Error(), "410") {
		t.Fatalf("error = %v, want download status", err)
	}
	if hits != 1 {
		t.Fatalf("downloads = %d, want 1", hits)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatal("failed download left a destination behind")
	}
}

func TestEnsureModelRejectsGarbage(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "garbage.tar.bz2")
	if err := os.WriteFile(archive, []byte("not bzip2 data"), 0o600); err != nil {
		t.Fatal(err)
	}
	var hits int
	url := serveFile(t, archive, &hits)
	dest := filepath.Join(t.TempDir(), "dest")

	if err := EnsureModel(url, dest, false); err == nil {
		t.Fatal("garbage archive accepted")
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatal("garbage archive left a destination behind")
	}
}

func TestEnsureModelRefusesUnsafeDest(t *testing.T) {
	var hits int
	url := serveFile(t, t.TempDir(), &hits)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, dest := range []string{"/", home} {
		if err := EnsureModel(url, dest, true); err == nil {
			t.Fatalf("unsafe dest %q accepted", dest)
		}
	}
	if hits != 0 {
		t.Fatalf("unsafe dest downloaded: hits = %d", hits)
	}
}
