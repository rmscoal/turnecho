package install

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ReleasesURL is the GitHub API endpoint listing turnecho releases.
const ReleasesURL = "https://api.github.com/repos/rmscoal/turnecho/releases/latest"

// releaseTimeout bounds release metadata requests.
const releaseTimeout = 30 * time.Second

// Release describes one published turnecho release.
type Release struct {
	Tag    string
	Assets []ReleaseAsset
}

// ReleaseAsset is one downloadable file attached to a release.
type ReleaseAsset struct {
	Name string
	URL  string
}

// fetchReleases is the API base replaced in tests.
var fetchReleases = ReleasesURL

// LatestRelease returns the newest published release.
func LatestRelease() (Release, error) {
	client := &http.Client{Timeout: releaseTimeout}
	resp, err := client.Get(fetchReleases)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("release check returned %s", resp.Status)
	}
	var listed struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listed); err != nil {
		return Release{}, fmt.Errorf("parsing release metadata: %w", err)
	}
	if listed.TagName == "" {
		return Release{}, fmt.Errorf("release has no tag")
	}
	release := Release{Tag: listed.TagName}
	for _, asset := range listed.Assets {
		if asset.Name == "" || asset.BrowserDownloadURL == "" {
			return Release{}, fmt.Errorf("release asset is missing a name or URL")
		}
		release.Assets = append(release.Assets, ReleaseAsset{Name: asset.Name, URL: asset.BrowserDownloadURL})
	}
	return release, nil
}

// CompareVersions orders two release tags. It returns 1 when target is
// newer, -1 when older, and 0 when equal. Non-semver tags are unordered.
func CompareVersions(current, target string) (int, error) {
	a, err := parseVersion(current)
	if err != nil {
		return 0, fmt.Errorf("current version %q: %v", current, err)
	}
	b, err := parseVersion(target)
	if err != nil {
		return 0, fmt.Errorf("target version %q: %v", target, err)
	}
	for i := range a {
		if b[i] != a[i] {
			if b[i] > a[i] {
				return 1, nil
			}
			return -1, nil
		}
	}
	return 0, nil
}

func parseVersion(tag string) ([3]int, error) {
	var version [3]int
	parts := strings.Split(strings.TrimPrefix(tag, "v"), ".")
	if len(parts) != 3 {
		return version, fmt.Errorf("want vX.Y.Z")
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return version, fmt.Errorf("want vX.Y.Z")
		}
		version[i] = n
	}
	return version, nil
}

// SelectAsset picks the bundle matching this platform, or reports that the
// release ships nothing compatible. Expected layout, set by Step 9 release
// packaging: turnecho-<os>-<arch>.tar.gz plus SHA256SUMS.
func SelectAsset(release Release) (bundle, checksums ReleaseAsset, err error) {
	want := fmt.Sprintf("turnecho-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	for _, asset := range release.Assets {
		switch asset.Name {
		case want:
			bundle = asset
		case "SHA256SUMS":
			checksums = asset
		}
	}
	if bundle.URL == "" {
		return bundle, checksums, fmt.Errorf("release %s ships no %s bundle", release.Tag, want)
	}
	if checksums.URL == "" {
		return bundle, checksums, fmt.Errorf("release %s ships no checksums", release.Tag)
	}
	return bundle, checksums, nil
}
