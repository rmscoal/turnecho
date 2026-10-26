package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ReleaseManifestName is the bundle descriptor at a release unit's root.
const ReleaseManifestName = "manifest.json"

// ReleaseManifestKind identifies turnecho release bundles.
const ReleaseManifestKind = "turnecho"

// ReleaseModel pins the model a release unit speaks with.
type ReleaseModel struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// ReleaseManifest describes one installed release unit.
type ReleaseManifest struct {
	Name    string       `json:"name"`
	Version string       `json:"version"`
	Model   ReleaseModel `json:"model"`
	Libs    []string     `json:"libs"`
}

// ReadReleaseManifest parses and validates the bundle descriptor in dir.
func ReadReleaseManifest(dir string) (ReleaseManifest, error) {
	var manifest ReleaseManifest
	raw, err := os.ReadFile(filepath.Join(dir, ReleaseManifestName))
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return manifest, fmt.Errorf("parsing release manifest: %w", err)
	}
	if manifest.Name != ReleaseManifestKind || manifest.Version == "" {
		return manifest, fmt.Errorf("not a turnecho release: %s", dir)
	}
	return manifest, nil
}
