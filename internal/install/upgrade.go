package install

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReleaseRoot returns the versioned release directory.
func ReleaseRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "turnecho", "releases"), nil
}

// ModelDirForID resolves a model runtime dir by release model id.
func ModelDirForID(id string) (string, error) {
	root, err := RuntimeRoot()
	if err != nil {
		return "", err
	}
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, "..") {
		return "", fmt.Errorf("invalid model id %q", id)
	}
	return filepath.Join(root, id), nil
}

// IsBrewBinary reports a Homebrew-managed installation.
func IsBrewBinary(exe string) bool {
	return strings.Contains(exe, "/Cellar/")
}

// StageRelease downloads the bundle, verifies it against checksums, and
// extracts it under workParent. It returns the staged release dir plus a
// discard func removing the whole staging area, including the downloaded
// files. The caller runs discard after PrepareReleaseDir moves the staged
// dir into place. It touches no live state and takes no lock.
func StageRelease(bundle, sums ReleaseAsset, workParent string) (staged string, discard func(), err error) {
	work, err := os.MkdirTemp(workParent, ".turnecho-release-*")
	if err != nil {
		return "", nil, err
	}
	discard = func() { os.RemoveAll(work) }
	bundlePath := filepath.Join(work, "release.tar.gz")
	if _, err := download(bundle.URL, bundlePath); err != nil {
		discard()
		return "", nil, err
	}
	sumsPath := filepath.Join(work, "SHA256SUMS")
	if _, err := download(sums.URL, sumsPath); err != nil {
		discard()
		return "", nil, err
	}
	if err := verifyBundle(bundlePath, sumsPath); err != nil {
		discard()
		return "", nil, err
	}
	top, err := extract(bundlePath, work)
	if err != nil {
		discard()
		return "", nil, err
	}
	return top, discard, nil
}

// verifyBundle checks the downloaded bundle against its checksums file.
func verifyBundle(bundlePath, sumsPath string) error {
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	base := filepath.Base(bundlePath)
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if filepath.Base(name) != base {
			continue
		}
		if fields[0] != hex.EncodeToString(sum[:]) {
			return fmt.Errorf("bundle checksum mismatch for %s", base)
		}
		return nil
	}
	return fmt.Errorf("checksums omit %s", base)
}

// PrepareReleaseDir moves staged into releases/<tag>, or adopts an existing
// identical directory as a crash retry. Anything else already in the way
// refuses without modification.
func PrepareReleaseDir(staged, tag string) (string, error) {
	root, err := ReleaseRoot()
	if err != nil {
		return "", err
	}
	dest := filepath.Join(root, tag)
	if _, err := os.Lstat(dest); os.IsNotExist(err) {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return "", err
		}
		if err := os.Rename(staged, dest); err != nil {
			return "", err
		}
		return dest, nil
	} else if err != nil {
		return "", err
	}
	identical, err := dirsIdentical(staged, dest)
	if err != nil {
		return "", err
	}
	if !identical {
		return "", fmt.Errorf("release %s exists and differs, refusing to overwrite %s", tag, dest)
	}
	if err := os.RemoveAll(staged); err != nil {
		return "", err
	}
	return dest, nil
}

// dirsIdentical compares two trees file by file, including contents.
func dirsIdentical(a, b string) (bool, error) {
	hashes := func(root string) (map[string]string, error) {
		out := map[string]string{}
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(raw)
			out[rel] = hex.EncodeToString(sum[:])
			return nil
		})
		return out, err
	}
	first, err := hashes(a)
	if err != nil {
		return false, err
	}
	second, err := hashes(b)
	if err != nil {
		return false, err
	}
	if len(first) != len(second) {
		return false, nil
	}
	for name, sum := range first {
		if second[name] != sum {
			return false, nil
		}
	}
	return true, nil
}

// LiveState is the observable installation state re-read under the lock.
type LiveState struct {
	LinkTarget     string
	ReleaseVersion string
	ModelSHA       string
}

// ReadLiveState captures the link target, active release version, and model
// marker hash. Missing pieces stay empty; nothing here fails when the
// machine was never installed.
func ReadLiveState(linkPath, modelDir string) LiveState {
	var state LiveState
	if target, err := filepath.EvalSymlinks(linkPath); err == nil {
		state.LinkTarget = target
		if manifest, err := ReadReleaseManifest(filepath.Dir(filepath.Dir(target))); err == nil {
			state.ReleaseVersion = manifest.Version
		}
	}
	if marker, err := ReadRuntimeMarker(modelDir); err == nil && marker.Name == RuntimeMarkerKind {
		state.ModelSHA = marker.SHA256
	}
	return state
}

// ValidateWAV checks upgrade selfcheck output without trusting it: RIFF
// header, 24 kHz mono 16-bit layout, and at least one nonzero sample.
func ValidateWAV(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(raw) <= 44 || string(raw[:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return fmt.Errorf("not a WAV file: %s", path)
	}
	if binary.LittleEndian.Uint32(raw[24:28]) != 24000 {
		return fmt.Errorf("unexpected sample rate in %s", path)
	}
	for _, sample := range raw[44:] {
		if sample != 0 {
			return nil
		}
	}
	return fmt.Errorf("selfcheck produced silence: %s", path)
}

// ReleaseExe returns the staged or installed release binary path.
func ReleaseExe(releaseDir string) string {
	return filepath.Join(releaseDir, "bin", LinkName)
}
