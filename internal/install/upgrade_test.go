package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// fixtureBundle builds a .tar.gz release bundle with the given top entries.
func fixtureBundle(t *testing.T, top string, files map[string]string) (archive []byte) {
	t.Helper()
	var raw bytes.Buffer
	compressed := gzip.NewWriter(&raw)
	writer := tar.NewWriter(compressed)
	write := func(name, body string, mode int64) {
		t.Helper()
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write(top+"/", "", 0o755)
	dirs := map[string]bool{}
	for name := range files {
		for dir := filepath.Dir(top + "/" + name); dir != "." && dir != top && !dirs[dir]; dir = filepath.Dir(dir) {
			dirs[dir] = true
		}
	}
	for dir := range dirs {
		write(dir+"/", "", 0o755)
	}
	for name, body := range files {
		write(top+"/"+name, body, 0o755)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func serveBytes(t *testing.T, data []byte, hits *int) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*hits++
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func sumsFor(t *testing.T, name string, data []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(data)
	return []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
}

func TestStageReleaseVerifies(t *testing.T) {
	bundle := fixtureBundle(t, "turnecho-test", map[string]string{
		"bin/turnecho":  "#!/bin/sh\n",
		"manifest.json": `{"name": "turnecho", "version": "v9.9.9"}`,
	})
	var bundleHits, sumsHits int
	bundleURL := serveBytes(t, bundle, &bundleHits)
	sumsURL := serveBytes(t, sumsFor(t, "release.tar.gz", bundle), &sumsHits)

	workParent := t.TempDir()
	staged, discard, err := StageRelease(ReleaseAsset{Name: "release.tar.gz", URL: bundleURL},
		ReleaseAsset{Name: "SHA256SUMS", URL: sumsURL}, workParent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(staged, "manifest.json")); err != nil {
		discard()
		t.Fatalf("staged bundle incomplete: %v", err)
	}
	discard()
	if entries, err := filepath.Glob(filepath.Join(workParent, ".turnecho-release-*")); err != nil || len(entries) != 0 {
		t.Fatalf("staging litter = %v, %v; want none", entries, err)
	}
}

func TestStageReleaseRejectsBadChecksum(t *testing.T) {
	bundle := fixtureBundle(t, "turnecho-test", map[string]string{"bin/turnecho": "#!/bin/sh\n"})
	var hits int
	bundleURL := serveBytes(t, bundle, &hits)
	sumsURL := serveBytes(t, []byte("deadbeef  release.tar.gz\n"), &hits)

	if _, _, err := StageRelease(ReleaseAsset{Name: "release.tar.gz", URL: bundleURL},
		ReleaseAsset{Name: "SHA256SUMS", URL: sumsURL}, t.TempDir()); err == nil {
		t.Fatal("tampered bundle accepted")
	}
}

func TestPrepareReleaseDirNoClobber(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	staged := filepath.Join(t.TempDir(), "staged")
	if err := os.MkdirAll(filepath.Join(staged, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "bin", "turnecho"), []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	dest, err := PrepareReleaseDir(staged, "v9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "bin", "turnecho")); err != nil {
		t.Fatalf("release not moved: %v", err)
	}

	// Identical content adopts the existing dir and drops staging.
	staged2 := filepath.Join(t.TempDir(), "staged2")
	if err := os.MkdirAll(filepath.Join(staged2, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged2, "bin", "turnecho"), []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	adopted, err := PrepareReleaseDir(staged2, "v9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	if adopted != dest {
		t.Fatalf("adopted = %q, want %q", adopted, dest)
	}

	// Different content refuses without touching either side.
	staged3 := filepath.Join(t.TempDir(), "staged3")
	if err := os.MkdirAll(filepath.Join(staged3, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged3, "bin", "turnecho"), []byte("evil"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareReleaseDir(staged3, "v9.9.9"); err == nil {
		t.Fatal("divergent release accepted")
	}
	raw, err := os.ReadFile(filepath.Join(dest, "bin", "turnecho"))
	if err != nil || string(raw) != "new" {
		t.Fatalf("live release disturbed: %q %v", raw, err)
	}
}

func TestReadLiveState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	state := ReadLiveState(filepath.Join(home, "missing"), filepath.Join(home, "missing-model"))
	if state != (LiveState{}) {
		t.Fatalf("empty machine reported state: %+v", state)
	}

	model := filepath.Join(home, ".local", "share", "turnecho", "runtimes", "m")
	if err := os.MkdirAll(model, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := `{"name": "turnecho-runtime", "model": "kokoro", "sha256": "abc"}` + "\n"
	if err := os.WriteFile(filepath.Join(model, RuntimeMarkerName), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(home, ".local", "share", "turnecho", "releases", "v9.9.9")
	if err := os.MkdirAll(filepath.Join(release, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(release, "bin", "turnecho")
	if err := os.WriteFile(exe, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "turnecho", "version": "v9.9.9"}` + "\n"
	if err := os.WriteFile(filepath.Join(release, ReleaseManifestName), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".local", "bin", "turnecho")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	state = ReadLiveState(link, model)
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}
	if state.LinkTarget != resolved || state.ReleaseVersion != "v9.9.9" || state.ModelSHA != "abc" {
		t.Fatalf("state = %+v", state)
	}
}

func TestValidateWAV(t *testing.T) {
	good := filepath.Join(t.TempDir(), "good.wav")
	if err := os.WriteFile(good, append(wavHeader(24000, 4), 0, 1, 0, 2), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWAV(good); err != nil {
		t.Fatal(err)
	}
	silent := filepath.Join(t.TempDir(), "silent.wav")
	if err := os.WriteFile(silent, append(wavHeader(24000, 4), 0, 0, 0, 0), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWAV(silent); err == nil {
		t.Fatal("silence accepted")
	}
	badRate := filepath.Join(t.TempDir(), "rate.wav")
	if err := os.WriteFile(badRate, append(wavHeader(16000, 4), 0, 1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWAV(badRate); err == nil {
		t.Fatal("wrong rate accepted")
	}
	if err := ValidateWAV(filepath.Join(t.TempDir(), "missing.wav")); err == nil {
		t.Fatal("missing file accepted")
	}
}

func wavHeader(rate, dataBytes int) []byte {
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	copy(header[8:12], "WAVE")
	header[24] = byte(rate)
	header[25] = byte(rate >> 8)
	header[26] = byte(rate >> 16)
	header[27] = byte(rate >> 24)
	copy(header[36:40], "data")
	return header
}

func TestIsBrewBinary(t *testing.T) {
	if !IsBrewBinary("/opt/homebrew/Cellar/turnecho/1.0/bin/turnecho") {
		t.Fatal("cellar path not detected")
	}
	if IsBrewBinary("/usr/local/bin/turnecho") {
		t.Fatal("plain path flagged")
	}
}

func TestModelDirForID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := ModelDirForID("kokoro-en-v0_19")
	if err != nil || dir != filepath.Join(home, ".local", "share", "turnecho", "runtimes", "kokoro-en-v0_19") {
		t.Fatalf("dir = %q, %v", dir, err)
	}
	if _, err := ModelDirForID("../evil"); err == nil {
		t.Fatal("path escape accepted")
	}
	if _, err := ModelDirForID(""); err == nil {
		t.Fatal("empty id accepted")
	}
}

func TestReadReleaseManifest(t *testing.T) {
	dir := t.TempDir()
	body := `{"name": "turnecho", "version": "v9.9.9", "model": {"id": "m", "url": "u", "sha256": "s"}, "libs": ["a"]}`
	if err := os.WriteFile(filepath.Join(dir, ReleaseManifestName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadReleaseManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "v9.9.9" || manifest.Model.ID != "m" || len(manifest.Libs) != 1 {
		t.Fatalf("manifest = %+v", manifest)
	}
	if _, err := ReadReleaseManifest(t.TempDir()); err == nil {
		t.Fatal("missing manifest accepted")
	}
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, ReleaseManifestName), []byte(`{"name": "other"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadReleaseManifest(bad); err == nil {
		t.Fatal("foreign manifest accepted")
	}
	if err := os.WriteFile(filepath.Join(bad, ReleaseManifestName), []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadReleaseManifest(bad); err == nil {
		t.Fatal("broken manifest accepted")
	}
}

func TestListedParsersRejectGarbage(t *testing.T) {
	for _, tc := range []struct {
		name string
		fn   func(string) ([]string, error)
	}{
		{"codex markets", codexMarketNames},
		{"codex plugins", codexPluginIDs},
		{"claude markets", claudeMarketNames},
		{"claude plugins", claudePluginIDs},
	} {
		if _, err := tc.fn(`{{{`); err == nil {
			t.Fatalf("%s accepted garbage", tc.name)
		}
	}
}
