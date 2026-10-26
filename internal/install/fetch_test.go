package install

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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

func archiveSHA(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func TestStageAndActivateFresh(t *testing.T) {
	parent := t.TempDir()
	fixtureModelTree(t, parent, "kokoro-en-v0_19")
	archive := fixtureArchive(t, parent, "kokoro-en-v0_19")
	var hits int
	url := serveFile(t, archive, &hits)
	workParent := t.TempDir()
	dest := filepath.Join(t.TempDir(), "runtimes", "kokoro-en-v0_19")

	staged, discard, err := StageModel(url, "kokoro", archiveSHA(t, archive), workParent)
	if err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Fatalf("downloads = %d, want 1", hits)
	}
	commit, rollback, err := ActivateModel(staged, dest)
	if err != nil {
		discard()
		t.Fatal(err)
	}
	discard()
	if entries, err := filepath.Glob(filepath.Join(workParent, ".turnecho-stage-*")); err != nil || len(entries) != 0 {
		t.Fatalf("staging litter = %v, %v; want none", entries, err)
	}
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	if !ModelReady(dest, archiveSHA(t, archive)) {
		t.Fatal("fresh install not ready")
	}
	if _, err := os.Stat(filepath.Join(dest, "SOURCE.sha256")); err != nil {
		t.Fatalf("missing hash record: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, RuntimeMarkerName)); err != nil {
		t.Fatalf("missing marker: %v", err)
	}
}

func TestModelReadyGatesCache(t *testing.T) {
	parent := t.TempDir()
	fixtureModelTree(t, parent, "kokoro-en-v0_19")
	archive := fixtureArchive(t, parent, "kokoro-en-v0_19")
	var hits int
	url := serveFile(t, archive, &hits)
	workParent := t.TempDir()
	dest := filepath.Join(t.TempDir(), "dest")
	sha := archiveSHA(t, archive)

	if ModelReady(dest, sha) {
		t.Fatal("missing dir reported ready")
	}
	staged, discard, err := StageModel(url, "kokoro", sha, workParent)
	if err != nil {
		t.Fatal(err)
	}
	defer discard()
	commit, _, err := ActivateModel(staged, dest)
	if err != nil {
		t.Fatal(err)
	}
	if err := commit(); err != nil {
		t.Fatal(err)
	}
	if !ModelReady(dest, sha) {
		t.Fatal("installed model not ready")
	}
	if ModelReady(dest, "deadbeef") {
		t.Fatal("stale hash reported ready")
	}
	if !ModelReady(dest, "") {
		t.Fatal("empty expected hash should check files only")
	}
	// A legacy dir without a marker is never a cache hit.
	if err := os.Remove(filepath.Join(dest, RuntimeMarkerName)); err != nil {
		t.Fatal(err)
	}
	if ModelReady(dest, sha) {
		t.Fatal("markerless dir reported ready")
	}
	if hits != 1 {
		t.Fatalf("downloads = %d, want 1", hits)
	}
}

func TestStageModelRejectsBadChecksum(t *testing.T) {
	parent := t.TempDir()
	fixtureModelTree(t, parent, "kokoro-en-v0_19")
	archive := fixtureArchive(t, parent, "kokoro-en-v0_19")
	var hits int
	url := serveFile(t, archive, &hits)

	_, _, err := StageModel(url, "kokoro", "deadbeef", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("error = %v, want checksum mismatch", err)
	}
}

func TestStageModelDiscardCleansWorkArea(t *testing.T) {
	parent := t.TempDir()
	fixtureModelTree(t, parent, "kokoro-en-v0_19")
	archive := fixtureArchive(t, parent, "kokoro-en-v0_19")
	var hits int
	url := serveFile(t, archive, &hits)
	workParent := t.TempDir()

	if _, discard, err := StageModel(url, "kokoro", archiveSHA(t, archive), workParent); err != nil {
		t.Fatal(err)
	} else {
		discard()
	}
	if entries, err := filepath.Glob(filepath.Join(workParent, ".turnecho-stage-*")); err != nil || len(entries) != 0 {
		t.Fatalf("staging litter = %v, %v; want none", entries, err)
	}
}

func TestActivateModelReplaceAndRollback(t *testing.T) {
	workParent := t.TempDir()
	newTree := fixtureModelTree(t, t.TempDir(), "kokoro-en-v0_19")
	dest := filepath.Join(t.TempDir(), "dest")
	oldTree := fixtureModelTree(t, t.TempDir(), "kokoro-en-v0_19")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldTree, dest); err != nil {
		t.Fatal(err)
	}
	// Point the old model at distinct content.
	if err := os.WriteFile(filepath.Join(dest, "model.onnx"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	staging, err := os.MkdirTemp(workParent, ".turnecho-stage-*")
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(staging, "model")
	if err := os.Rename(newTree, staged); err != nil {
		t.Fatal(err)
	}

	_, rollback, err := ActivateModel(staged, dest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dest, "model.onnx"))
	if err != nil || string(raw) != "fixture" {
		t.Fatalf("new model not live: %q %v", raw, err)
	}
	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(dest, "model.onnx"))
	if err != nil || string(raw) != "old" {
		t.Fatalf("old model not restored: %q %v", raw, err)
	}

	// Repeat and commit: the backup must be cleaned up.
	staging2, err := os.MkdirTemp(workParent, ".turnecho-stage-*")
	if err != nil {
		t.Fatal(err)
	}
	newTree2 := fixtureModelTree(t, t.TempDir(), "kokoro-en-v0_19")
	staged2 := filepath.Join(staging2, "model")
	if err := os.Rename(newTree2, staged2); err != nil {
		t.Fatal(err)
	}
	commit2, _, err := ActivateModel(staged2, dest)
	if err != nil {
		t.Fatal(err)
	}
	if err := commit2(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dest + ".previous"); !os.IsNotExist(err) {
		t.Fatal("backup left behind after commit")
	}
}

func TestActivateModelRefusesExistingBackup(t *testing.T) {
	workParent := t.TempDir()
	newTree := fixtureModelTree(t, t.TempDir(), "kokoro-en-v0_19")
	dest := filepath.Join(t.TempDir(), "dest")
	if err := os.MkdirAll(dest+".previous", 0o755); err != nil {
		t.Fatal(err)
	}
	staging, err := os.MkdirTemp(workParent, ".turnecho-stage-*")
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(staging, "model")
	if err := os.Rename(newTree, staged); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ActivateModel(staged, dest); err == nil {
		t.Fatal("existing backup accepted")
	}
}

func TestActivateModelRefusesUnsafeDest(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, dest := range []string{"/", home} {
		if _, _, err := ActivateModel(t.TempDir(), dest); err == nil {
			t.Fatalf("unsafe dest %q accepted", dest)
		}
	}
}

func TestStageModelRejectsIncomplete(t *testing.T) {
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
	workParent := t.TempDir()

	if _, _, err := StageModel(url, "kokoro", "", workParent); err == nil {
		t.Fatal("incomplete model accepted")
	}
	leftovers, err := filepath.Glob(filepath.Join(workParent, ".turnecho-stage-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("staging left behind: %v", leftovers)
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

func TestStageModelRejectsSymlink(t *testing.T) {
	archive := customTarball(t, modelEntries(tarEntry{
		name: "kokoro-en-v0_19/evil", body: "/etc/passwd", flag: tar.TypeSymlink,
	}))
	var hits int
	url := serveFile(t, archive, &hits)

	_, _, err := StageModel(url, "kokoro", "", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "link") {
		t.Fatalf("error = %v, want link rejection", err)
	}
}

func TestStageModelSkipsAppleDouble(t *testing.T) {
	archive := customTarball(t, modelEntries(
		tarEntry{name: "._kokoro-en-v0_19", body: "fork", flag: tar.TypeReg},
		tarEntry{name: "kokoro-en-v0_19/._model.onnx", body: "fork", flag: tar.TypeReg},
	))
	var hits int
	url := serveFile(t, archive, &hits)

	staged, discard, err := StageModel(url, "kokoro", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer discard()
	if _, err := os.Stat(filepath.Join(staged, "model.onnx")); err != nil {
		t.Fatalf("staged model missing: %v", err)
	}
}

func TestStageModelRejectsHTTPError(t *testing.T) {
	var hits int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		http.Error(w, "gone", http.StatusGone)
	}))
	t.Cleanup(server.Close)

	_, _, err := StageModel(server.URL, "kokoro", "", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "410") {
		t.Fatalf("error = %v, want download status", err)
	}
	if hits != 1 {
		t.Fatalf("downloads = %d, want 1", hits)
	}
}

func TestStageModelRejectsGarbage(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "garbage.tar.bz2")
	if err := os.WriteFile(archive, []byte("not bzip2 data"), 0o600); err != nil {
		t.Fatal(err)
	}
	var hits int
	url := serveFile(t, archive, &hits)

	if _, _, err := StageModel(url, "kokoro", "", t.TempDir()); err == nil {
		t.Fatal("garbage archive accepted")
	}
}
