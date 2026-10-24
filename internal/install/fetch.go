package install

import (
	"archive/tar"
	"compress/bzip2"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rmscoal/turnecho/internal/tts"
)

// ModelURL is the official Kokoro release archive fetched by install.
// A pinned checksum will replace the recorded hash once Step 9 cuts a
// release; until then the hash is recorded for reference only.
const ModelURL = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-en-v0_19.tar.bz2"

// downloadTimeout bounds a stalled model download.
const downloadTimeout = 10 * time.Minute

// EnsureModel guarantees a validated model directory at dest,
// downloading from url unless the directory already validates.
// force re-downloads even when dest validates.
func EnsureModel(url, dest string, force bool) error {
	if err := guardDest(dest); err != nil {
		return err
	}
	if !force {
		if err := tts.ValidateModelDir(dest); err == nil {
			return nil
		}
	}
	work, err := os.MkdirTemp("", "turnecho-model-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	archive := filepath.Join(work, "kokoro.tar.bz2")
	sum, err := download(url, archive)
	if err != nil {
		return err
	}
	top, err := extract(archive, work)
	if err != nil {
		return err
	}
	if err := tts.ValidateModelDir(top); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.Rename(top, dest); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dest, "SOURCE.sha256"),
		[]byte(hex.EncodeToString(sum[:])+"  kokoro.tar.bz2\n"), 0o600)
}

// guardDest refuses installation paths that would endanger the home tree.
func guardDest(dest string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	switch filepath.Clean(dest) {
	case "", ".", "/", home:
		return fmt.Errorf("refusing to install model into unsafe directory: %s", dest)
	}
	return nil
}

// download fetches url to path, returning the archive SHA256.
func download(url, path string) ([32]byte, error) {
	var sum [32]byte
	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Get(url) // pinned release archive
	if err != nil {
		return sum, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return sum, fmt.Errorf("model download returned %s", resp.Status)
	}
	out, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return sum, err
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, hash), resp.Body); err != nil {
		out.Close()
		return sum, err
	}
	if err := out.Close(); err != nil {
		return sum, err
	}
	copy(sum[:], hash.Sum(nil))
	return sum, nil
}

// extract unpacks a .tar.bz2 archive under work and returns the single
// top-level directory, rejecting symlinks and path escapes.
func extract(archive, work string) (string, error) {
	file, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer file.Close()
	reader := tar.NewReader(bzip2.NewReader(file))
	var top string
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if header.Typeflag == tar.TypeSymlink || header.Typeflag == tar.TypeLink {
			return "", fmt.Errorf("model archive contains a link: %s", header.Name)
		}
		clean := filepath.Clean(header.Name)
		if strings.Contains(clean, "..") || filepath.IsAbs(clean) {
			return "", fmt.Errorf("model archive escapes its directory: %s", header.Name)
		}
		// Skip macOS resource-fork entries; they carry no model data.
		for _, part := range strings.Split(clean, string(os.PathSeparator)) {
			if strings.HasPrefix(part, "._") {
				clean = ""
				break
			}
		}
		if clean == "" {
			continue
		}
		target := filepath.Join(work, "model", clean)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return "", err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(out, reader); err != nil {
				out.Close()
				return "", err
			}
			if err := out.Close(); err != nil {
				return "", err
			}
		}
		rel, err := filepath.Rel(filepath.Join(work, "model"), target)
		if err != nil {
			return "", err
		}
		first := strings.Split(rel, string(os.PathSeparator))[0]
		if top == "" {
			top = filepath.Join(work, "model", first)
		}
	}
	if top == "" {
		return "", fmt.Errorf("model archive is empty")
	}
	return top, nil
}
