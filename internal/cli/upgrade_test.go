package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rmscoal/turnecho/internal/install"
	"github.com/rmscoal/turnecho/internal/version"
)

func stubUpgradeRelease(t *testing.T, tag string, assets []install.ReleaseAsset) {
	t.Helper()
	previous := upgradeLatestRelease
	upgradeLatestRelease = func() (install.Release, error) {
		return install.Release{Tag: tag, Assets: assets}, nil
	}
	t.Cleanup(func() { upgradeLatestRelease = previous })
}

func stubUpgradeVersion(t *testing.T, ver string) {
	t.Helper()
	previous := version.Version
	version.Version = ver
	t.Cleanup(func() { version.Version = previous })
}

func platformBundleName() string {
	return fmt.Sprintf("turnecho-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
}

func fixtureBundle(t *testing.T, top string, files map[string]string) []byte {
	t.Helper()
	var raw bytes.Buffer
	compressed := gzip.NewWriter(&raw)
	writer := tar.NewWriter(compressed)
	for _, dir := range []string{top + "/", top + "/bin/"} {
		if err := writer.WriteHeader(&tar.Header{Name: dir, Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		if err := writer.WriteHeader(&tar.Header{Name: top + "/" + name, Mode: 0o755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func serveUpgradeBytes(t *testing.T, data []byte) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func validSelfcheckWAV(t *testing.T, path string) {
	t.Helper()
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	copy(header[8:12], "WAVE")
	header[24] = 0xC0
	header[25] = 0x5D
	if err := os.WriteFile(path, append(header, 0, 1, 0, 2), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeCheckReports(t *testing.T) {
	isolateHome(t)
	stubUpgradeVersion(t, "v1.0.0")
	stubUpgradeRelease(t, "v2.0.0", nil)
	code, stdout, _ := run(t, "", "upgrade", "--check")
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %q", code, stdout)
	}
	if !strings.Contains(stdout, "v1.0.0 -> v2.0.0") {
		t.Fatalf("stdout = %q", stdout)
	}

	stubUpgradeRelease(t, "v1.0.0", nil)
	code, stdout, _ = run(t, "", "upgrade")
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %q", code, stdout)
	}
	if !strings.Contains(stdout, "Already on the latest release") {
		t.Fatalf("stdout = %q", stdout)
	}

	stubUpgradeVersion(t, "v9.9.9")
	stubUpgradeRelease(t, "v2.0.0", nil)
	code, _, stderr := run(t, "", "upgrade", "--check")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr, "refusing to downgrade") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestUpgradeGuards(t *testing.T) {
	isolateHome(t)
	stubUpgradeVersion(t, "v1.0.0")
	stubUpgradeRelease(t, "v2.0.0", nil)
	previous := installExe
	installExe = func() (string, error) { return "/opt/homebrew/Cellar/turnecho/2.0/bin/turnecho", nil }
	t.Cleanup(func() { installExe = previous })
	if code, _, _ := run(t, "", "upgrade", "--check"); code != 1 {
		t.Fatalf("brew exit = %d, want 1", code)
	}

	installExe = func() (string, error) { return "/usr/local/bin/turnecho", nil }
	stubUpgradeVersion(t, "development")
	if code, _, _ := run(t, "", "upgrade", "--check"); code != 1 {
		t.Fatalf("dev exit = %d, want 1", code)
	}
}

func TestUpgradeDryRunPlans(t *testing.T) {
	isolateHome(t)
	stubUpgradeVersion(t, "v1.0.0")
	stubUpgradeRelease(t, "v2.0.0", []install.ReleaseAsset{
		{Name: platformBundleName(), URL: "https://x/bundle"},
		{Name: "SHA256SUMS", URL: "https://x/sums"},
	})
	code, stdout, _ := run(t, "", "upgrade", "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %q", code, stdout)
	}
	for _, want := range []string{"Target: v2.0.0", "https://x/bundle", "Release dir:", "Model: pinned by the release manifest"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func upgradeFixtureWorld(t *testing.T, selfcheck func(bin, modelDir, out string) error) (home string) {
	t.Helper()
	home = isolateHome(t)
	parent := t.TempDir()
	archive := fixtureTarball(t, parent, "kokoro-en-v0_19")
	modelRaw, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	modelSum := sha256.Sum256(modelRaw)
	modelURL := serveUpgradeBytes(t, modelRaw)

	manifest := fmt.Sprintf(`{"name": "turnecho", "version": "v2.0.0", "model": {"id": "kokoro-en-v0_19", "url": %q, "sha256": %q}}`,
		modelURL, hex.EncodeToString(modelSum[:]))
	bundle := fixtureBundle(t, "turnecho-v2", map[string]string{
		"bin/turnecho":  "#!/bin/sh\nexit 0\n",
		"lib/libx":      "native",
		"manifest.json": manifest,
	})
	bundleSum := sha256.Sum256(bundle)
	sums := []byte(hex.EncodeToString(bundleSum[:]) + "  release.tar.gz\n")
	stubUpgradeRelease(t, "v2.0.0", []install.ReleaseAsset{
		{Name: platformBundleName(), URL: serveUpgradeBytes(t, bundle)},
		{Name: "SHA256SUMS", URL: serveUpgradeBytes(t, sums)},
	})
	stubUpgradeVersion(t, "v1.0.0")
	previousSelfcheck := upgradeSelfcheck
	upgradeSelfcheck = func(bin, modelDir, out string) error {
		return selfcheck(bin, modelDir, out)
	}
	t.Cleanup(func() { upgradeSelfcheck = previousSelfcheck })
	previousExe := installExe
	installExe = func() (string, error) { return filepath.Join(t.TempDir(), "turnecho"), nil }
	t.Cleanup(func() { installExe = previousExe })
	return home
}

func TestUpgradeFullRun(t *testing.T) {
	home := upgradeFixtureWorld(t, func(bin, modelDir, out string) error {
		if _, err := os.Stat(bin); err != nil {
			t.Errorf("staged binary missing: %v", err)
		}
		if _, err := os.Stat(modelDir); err != nil {
			t.Errorf("staged model missing: %v", err)
		}
		validSelfcheckWAV(t, out)
		return nil
	})
	code, stdout, _ := run(t, "", "upgrade")
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %q", code, stdout)
	}
	if !strings.Contains(stdout, "Upgraded to v2.0.0.") {
		t.Fatalf("stdout = %q", stdout)
	}
	target, err := os.Readlink(filepath.Join(home, ".local", "bin", "turnecho"))
	if err != nil {
		t.Fatalf("link missing: %v", err)
	}
	if !strings.Contains(target, filepath.Join("releases", "v2.0.0")) {
		t.Fatalf("link = %q", target)
	}
}

func TestUpgradeSelfcheckFailureRollsBack(t *testing.T) {
	home := upgradeFixtureWorld(t, func(bin, modelDir, out string) error {
		return fmt.Errorf("staged binary crashed")
	})
	code, _, _ := run(t, "", "upgrade")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if _, err := os.Lstat(filepath.Join(home, ".local", "bin", "turnecho")); !os.IsNotExist(err) {
		t.Fatal("link left behind after failed upgrade")
	}
	entries, err := os.ReadDir(filepath.Join(home, ".local", "share", "turnecho", "releases"))
	if err == nil && len(entries) != 0 {
		t.Fatalf("release left behind: %v", entries)
	}
}
