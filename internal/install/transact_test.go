package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rmscoal/turnecho/internal/worker"
)

// fixtureRelease builds a .tar.gz bundle and its SHA256SUMS body.
func fixtureRelease(t *testing.T, top string, files map[string]string) (bundle, sums []byte) {
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
	sum := sha256.Sum256(raw.Bytes())
	return raw.Bytes(), []byte(hex.EncodeToString(sum[:]) + "  release.tar.gz\n")
}

type fixtureWorld struct {
	home      string
	link      string
	model     string
	api       string
	bundleURL string
	sumsURL   string
	modelURL  string
	modelSHA  string
}

// seedUpgradeWorld builds a v1 installation plus a v2 release fixture.
func seedUpgradeWorld(t *testing.T, failSelfcheck string) *fixtureWorld {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Live v1 release and model.
	v1 := filepath.Join(home, ".local", "share", "turnecho", "releases", "v1.0.0")
	if err := os.MkdirAll(filepath.Join(v1, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v1, "bin", "turnecho"), []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v1, "manifest.json"), []byte(`{"name": "turnecho", "version": "v1.0.0"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".local", "bin", "turnecho")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(v1, "bin", "turnecho"), link); err != nil {
		t.Fatal(err)
	}
	modelParent := t.TempDir()
	modelTree := fixtureModelTree(t, modelParent, "kokoro-en-v0_19")
	model, err := ModelDirForID("kokoro-en-v0_19")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(model), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(modelTree, model); err != nil {
		t.Fatal(err)
	}
	marker := `{"name": "turnecho-runtime", "model": "kokoro", "sha256": "old"}` + "\n"
	if err := os.WriteFile(filepath.Join(model, RuntimeMarkerName), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(model, "SOURCE.sha256"), []byte("old  kokoro.tar.bz2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// v2 model fixture served over HTTP.
	modelFixtureParent := t.TempDir()
	fixtureModelTree(t, modelFixtureParent, "kokoro-en-v0_19")
	modelArchive := fixtureArchive(t, modelFixtureParent, "kokoro-en-v0_19")
	var modelHits int
	modelURL := serveFile(t, modelArchive, &modelHits)
	modelRaw, err := os.ReadFile(modelArchive)
	if err != nil {
		t.Fatal(err)
	}
	modelSum := sha256.Sum256(modelRaw)

	// v2 release fixture served over HTTP.
	binScript := "#!/bin/sh\nexit 0\n"
	if failSelfcheck != "" {
		binScript = "#!/bin/sh\necho " + failSelfcheck + " >&2\nexit 1\n"
	}
	bundle, sums := fixtureRelease(t, "turnecho-v2", map[string]string{
		"bin/turnecho":  binScript,
		"lib/libx":      "native",
		"manifest.json": fmt.Sprintf(`{"name": "turnecho", "version": "v2.0.0", "model": {"id": "kokoro-en-v0_19", "url": %q, "sha256": %q}}`, modelURL, hex.EncodeToString(modelSum[:])),
	})
	var bundleHits int
	bundleURL := serveBytes(t, bundle, &bundleHits)
	var sumsHits int
	sumsURL := serveBytes(t, sums, &sumsHits)
	_ = bundleHits
	_ = sumsHits

	return &fixtureWorld{
		home: home, link: link, model: model,
		modelURL: modelURL, modelSHA: hex.EncodeToString(modelSum[:]),
		bundleURL: bundleURL, sumsURL: sumsURL,
	}
}

func upgradePlanFor(world *fixtureWorld) UpgradePlan {
	return UpgradePlan{
		Target:     "v2.0.0",
		Bundle:     ReleaseAsset{Name: "release.tar.gz", URL: world.bundleURL},
		Sums:       ReleaseAsset{Name: "SHA256SUMS", URL: world.sumsURL},
		ReleaseDir: filepath.Join(world.home, ".local", "share", "turnecho", "releases", "v2.0.0"),
		LinkPath:   world.link,
	}
}

// validSelfcheck pretends the staged binary synthesizes real audio.
func validSelfcheck(t *testing.T) func(bin, modelDir, out string) error {
	t.Helper()
	return func(bin, modelDir, out string) error {
		if _, err := os.Stat(bin); err != nil {
			return fmt.Errorf("staged binary missing: %v", err)
		}
		if _, err := os.Stat(modelDir); err != nil {
			return fmt.Errorf("staged model missing: %v", err)
		}
		return os.WriteFile(out, append(wavHeader(24000, 4), 0, 1, 0, 2), 0o600)
	}
}

func upgradeDeps(selfcheck func(bin, modelDir, out string) error) UpgradeDeps {
	return UpgradeDeps{Selfcheck: selfcheck, Lock: worker.HoldLock}
}

func TestRunUpgradeEndToEnd(t *testing.T) {
	world := seedUpgradeWorld(t, "")
	plan := upgradePlanFor(world)
	if err := RunUpgrade(plan, upgradeDeps(validSelfcheck(t))); err != nil {
		t.Fatal(err)
	}
	target, err := filepath.EvalSymlinks(world.link)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(plan.ReleaseDir, "bin", "turnecho"))
	if err != nil {
		t.Fatal(err)
	}
	if target != want {
		t.Fatalf("link = %q, want %q", target, want)
	}
	if _, err := os.Stat(filepath.Join(world.home, ".local", "share", "turnecho", "releases", "v1.0.0")); !os.IsNotExist(err) {
		t.Fatal("old release not pruned")
	}
	raw, err := os.ReadFile(filepath.Join(world.model, RuntimeMarkerName))
	if err != nil || !strings.Contains(string(raw), world.modelSHA) {
		t.Fatalf("model not refreshed: %q %v", raw, err)
	}
	if _, err := os.Stat(world.model + ".previous"); !os.IsNotExist(err) {
		t.Fatal("model backup left behind")
	}
}

func TestRunUpgradeSelfcheckFailureChangesNothing(t *testing.T) {
	world := seedUpgradeWorld(t, "")
	plan := upgradePlanFor(world)
	broken := func(bin, modelDir, out string) error {
		return fmt.Errorf("staged binary crashed")
	}
	if err := RunUpgrade(plan, upgradeDeps(broken)); err == nil {
		t.Fatal("broken staged release accepted")
	}
	target, err := filepath.EvalSymlinks(world.link)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(target, filepath.Join("releases", "v1.0.0", "bin", "turnecho")) {
		t.Fatalf("link moved: %q", target)
	}
	entries, err := os.ReadDir(filepath.Join(world.home, ".local", "share", "turnecho", "releases"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "v1.0.0" {
		t.Fatalf("releases disturbed: %v", entries)
	}
}

func TestRunUpgradeStaleTransactionAborts(t *testing.T) {
	world := seedUpgradeWorld(t, "")
	plan := upgradePlanFor(world)
	mutating := validSelfcheck(t)
	deps := upgradeDeps(func(bin, modelDir, out string) error {
		// A concurrent command repoints the link mid-transaction.
		os.Remove(world.link)
		if err := os.Symlink(filepath.Join(world.home, "elsewhere"), world.link); err != nil {
			return err
		}
		return mutating(bin, modelDir, out)
	})
	if err := RunUpgrade(plan, deps); err == nil || !strings.Contains(err.Error(), "changed during upgrade") {
		t.Fatalf("error = %v, want stale abort", err)
	}
	raw, err := os.ReadFile(filepath.Join(world.model, RuntimeMarkerName))
	if err != nil || strings.Contains(string(raw), world.modelSHA) {
		t.Fatalf("model touched by stale transaction: %q", raw)
	}
}

func TestRunUpgradeRefusesDivergentExistingRelease(t *testing.T) {
	world := seedUpgradeWorld(t, "")
	v2 := filepath.Join(world.home, ".local", "share", "turnecho", "releases", "v2.0.0")
	if err := os.MkdirAll(filepath.Join(v2, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(v2, "bin", "turnecho"), []byte("different"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := upgradePlanFor(world)
	if err := RunUpgrade(plan, upgradeDeps(validSelfcheck(t))); err == nil {
		t.Fatal("divergent existing release accepted")
	}
	target, err := filepath.EvalSymlinks(world.link)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(target, filepath.Join("releases", "v1.0.0", "bin", "turnecho")) {
		t.Fatalf("link moved: %q", target)
	}
}

// seedReleaseUnit creates a minimal on-disk release unit and returns its exe.
func seedReleaseUnit(t *testing.T, dir, version string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "bin", "turnecho")
	if err := os.WriteFile(exe, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"name": "turnecho", "version": %q}`+"\n", version)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestPrunePreviousReleaseRemovesSupersededUnit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	releases, err := ReleaseRoot()
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(releases, "v1.0.0")
	seedReleaseUnit(t, old, "v1.0.0")
	current := filepath.Join(releases, "v2.0.0")
	seedReleaseUnit(t, current, "v2.0.0")
	previousLink, err := filepath.EvalSymlinks(filepath.Join(old, "bin", "turnecho"))
	if err != nil {
		t.Fatal(err)
	}

	if err := prunePreviousRelease(previousLink, current); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(old); !os.IsNotExist(err) {
		t.Fatal("superseded unit left behind")
	}
	if _, err := os.Stat(filepath.Join(current, "bin", "turnecho")); err != nil {
		t.Fatalf("current release removed: %v", err)
	}
}

func TestPrunePreviousReleaseKeepsCurrentThroughSymlinkedHome(t *testing.T) {
	real := t.TempDir()
	aliased := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(real, aliased); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", aliased)
	releases, err := ReleaseRoot()
	if err != nil {
		t.Fatal(err)
	}
	current := filepath.Join(releases, "v2.0.0")
	exe := seedReleaseUnit(t, current, "v2.0.0")
	previousLink, err := filepath.EvalSymlinks(exe)
	if err != nil {
		t.Fatal(err)
	}

	if err := prunePreviousRelease(previousLink, current); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(exe); err != nil {
		t.Fatalf("current release removed through symlinked home: %v", err)
	}
}
