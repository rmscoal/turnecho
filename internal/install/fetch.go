package install

import (
	"archive/tar"
	"compress/bzip2"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
const ModelURL = "https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-en-v0_19.tar.bz2"

// PinnedModelSHA256 is the archive checksum CI verifies. Release paths
// always verify against it; an empty expected hash skips verification and
// is reserved for tests.
const PinnedModelSHA256 = "912804855a04745fa77a30be545b3f9a5d15c4d66db00b88cbcd4921df605ac7"

// RuntimeMarkerName marks directories this installer owns.
const RuntimeMarkerName = ".turnecho-runtime.json"

// RuntimeMarkerKind identifies ownership markers written by install.
const RuntimeMarkerKind = "turnecho-runtime"

// RuntimeMarker records which model release a runtime dir holds.
type RuntimeMarker struct {
	Name   string `json:"name"`
	Model  string `json:"model"`
	SHA256 string `json:"sha256"`
}

// downloadTimeout bounds a stalled model download.
const downloadTimeout = 10 * time.Minute

// ModelReady reports whether dest holds a validated model matching
// expectedSHA. An empty expectedSHA checks files only and is reserved
// for tests.
func ModelReady(dest, expectedSHA string) bool {
	if err := tts.ValidateModelDir(dest); err != nil {
		return false
	}
	marker, err := ReadRuntimeMarker(dest)
	if err != nil || marker.Name != RuntimeMarkerKind {
		return false
	}
	return expectedSHA == "" || marker.SHA256 == expectedSHA
}

// ReadRuntimeMarker parses the ownership marker in dir.
func ReadRuntimeMarker(dir string) (RuntimeMarker, error) {
	var marker RuntimeMarker
	raw, err := os.ReadFile(filepath.Join(dir, RuntimeMarkerName))
	if err != nil {
		return marker, err
	}
	if err := json.Unmarshal(raw, &marker); err != nil {
		return marker, fmt.Errorf("parsing runtime marker: %w", err)
	}
	return marker, nil
}

// StageModel downloads url, verifies expectedSHA when non-empty, extracts
// and validates into a fresh directory under workParent. It returns the
// staged model dir plus a discard func that removes the whole staging area,
// including the downloaded archive. The caller runs discard after
// ActivateModel moves the staged dir into place; staged contents already
// moved are unaffected. Staging never touches live state and takes no
// locks; the caller commits with ActivateModel. workParent should be the
// destination's parent so the later rename stays on one filesystem.
func StageModel(url, modelID, expectedSHA, workParent string) (staged string, discard func(), err error) {
	if err := os.MkdirAll(workParent, 0o755); err != nil {
		return "", nil, err
	}
	work, err := os.MkdirTemp(workParent, ".turnecho-stage-*")
	if err != nil {
		return "", nil, err
	}
	discard = func() { os.RemoveAll(work) }
	archive := filepath.Join(work, "kokoro.tar.bz2")
	sum, err := download(url, archive)
	if err != nil {
		discard()
		return "", nil, err
	}
	if expectedSHA != "" && hex.EncodeToString(sum[:]) != expectedSHA {
		discard()
		return "", nil, fmt.Errorf("model checksum mismatch for %s", url)
	}
	top, err := extract(archive, work)
	if err != nil {
		discard()
		return "", nil, err
	}
	if err := tts.ValidateModelDir(top); err != nil {
		discard()
		return "", nil, err
	}
	if err := writeModelRecord(top, modelID, sum); err != nil {
		discard()
		return "", nil, err
	}
	return top, discard, nil
}

// ActivateModel swaps staged into dest. The caller must hold the worker
// lock: activation mutates shared state. It returns commit, which drops
// the preserved previous model after overall success, and rollback, which
// restores the previous model or, for a fresh install, keeps the validated
// fetch as a cache. Refuses when another transaction's backup exists.
func ActivateModel(staged, dest string) (commit func() error, rollback Rollback, err error) {
	if err := guardDest(dest); err != nil {
		return nil, nil, err
	}
	previous := dest + ".previous"
	if _, err := os.Lstat(previous); err == nil {
		return nil, nil, fmt.Errorf("model backup %s exists from another transaction, refusing", previous)
	}
	nothing := func() error { return nil }
	if _, err := os.Lstat(dest); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, nil, err
		}
		if err := os.Rename(staged, dest); err != nil {
			return nil, nil, err
		}
		return nothing, nothing, nil
	}
	if err := os.Rename(dest, previous); err != nil {
		return nil, nil, err
	}
	if err := os.Rename(staged, dest); err != nil {
		if rerr := os.Rename(previous, dest); rerr != nil {
			return nil, nil, fmt.Errorf("%v; restoring previous model: %v", err, rerr)
		}
		return nil, nil, err
	}
	commit = func() error { return os.RemoveAll(previous) }
	rollback = func() error {
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
		return os.Rename(previous, dest)
	}
	return commit, rollback, nil
}

// writeModelRecord stores the archive hash and ownership marker beside a
// staged model so the whole unit moves together on activation.
func writeModelRecord(dir, modelID string, sum [32]byte) error {
	hash := hex.EncodeToString(sum[:])
	if err := os.WriteFile(filepath.Join(dir, "SOURCE.sha256"),
		[]byte(hash+"  kokoro.tar.bz2\n"), 0o600); err != nil {
		return err
	}
	marker, err := json.Marshal(RuntimeMarker{
		Name:   RuntimeMarkerKind,
		Model:  modelID,
		SHA256: hash,
	})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, RuntimeMarkerName), append(marker, '\n'), 0o600)
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

// decompress selects bzip2 or gzip by archive suffix.
func decompress(archive string, file io.Reader) (io.Reader, error) {
	switch {
	case strings.HasSuffix(archive, ".bz2"):
		return bzip2.NewReader(file), nil
	case strings.HasSuffix(archive, ".gz"):
		return gzip.NewReader(file)
	default:
		return nil, fmt.Errorf("unsupported archive format: %s", archive)
	}
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
	decompressed, err := decompress(archive, file)
	if err != nil {
		return "", err
	}
	reader := tar.NewReader(decompressed)
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
