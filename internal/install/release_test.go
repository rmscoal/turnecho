package install

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

func serveRelease(t *testing.T, body string, status int) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func TestReleasesURLPointsAtAPI(t *testing.T) {
	if !strings.HasPrefix(ReleasesURL, "https://api.github.com/repos/rmscoal/turnecho/releases/") {
		t.Fatalf("ReleasesURL = %q", ReleasesURL)
	}
}

func TestLatestReleaseParses(t *testing.T) {
	previous := fetchReleases
	fetchReleases = serveRelease(t, `{"tag_name": "v2.1.0", "assets": [
		{"name": "turnecho-darwin-arm64.tar.gz", "browser_download_url": "https://x/bundle"},
		{"name": "SHA256SUMS", "browser_download_url": "https://x/sums"}]}`, http.StatusOK)
	t.Cleanup(func() { fetchReleases = previous })

	release, err := LatestRelease()
	if err != nil {
		t.Fatal(err)
	}
	if release.Tag != "v2.1.0" || len(release.Assets) != 2 {
		t.Fatalf("release = %+v", release)
	}
}

func TestLatestReleaseRejectsBadPayloads(t *testing.T) {
	previous := fetchReleases
	t.Cleanup(func() { fetchReleases = previous })
	for name, body := range map[string]string{
		"missing tag":   `{"assets": []}`,
		"missing asset": `{"tag_name": "v2.0.0", "assets": [{"name": "x"}]}`,
		"not JSON":      `{{{`,
	} {
		fetchReleases = serveRelease(t, body, http.StatusOK)
		if _, err := LatestRelease(); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	fetchReleases = serveRelease(t, "gone", http.StatusGone)
	if _, err := LatestRelease(); err == nil {
		t.Fatal("HTTP error accepted")
	}
}

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		current, target string
		want            int
	}{
		{"v2.0.0", "v2.1.0", 1},
		{"v2.1.0", "v2.1.0", 0},
		{"v2.1.0", "v2.0.0", -1},
		{"2.0.0", "v2.0.1", 1},
		{"v1.9.9", "v2.0.0", 1},
	} {
		got, err := CompareVersions(tc.current, tc.target)
		if err != nil || got != tc.want {
			t.Fatalf("CompareVersions(%q, %q) = %d, %v", tc.current, tc.target, got, err)
		}
	}
	for _, tc := range [][2]string{
		{"development", "v2.0.0"},
		{"v2.0.0", "nightly"},
		{"v2.0", "v2.0.0"},
		{"v2.0.0.1", "v2.0.0"},
	} {
		if _, err := CompareVersions(tc[0], tc[1]); err == nil {
			t.Fatalf("CompareVersions(%q, %q) accepted", tc[0], tc[1])
		}
	}
}

func TestSelectAssetMatchesPlatform(t *testing.T) {
	_, _, err := SelectAsset(Release{Tag: "v2.1.0", Assets: []ReleaseAsset{
		{Name: "turnecho-plan9-386.tar.gz", URL: "https://x/other"},
		{Name: "SHA256SUMS", URL: "https://x/sums"},
	}})
	if err == nil || !strings.Contains(err.Error(), "no turnecho-") {
		t.Fatalf("wrong-platform release accepted: %v", err)
	}
	_, _, err = SelectAsset(Release{Tag: "v2.1.0", Assets: []ReleaseAsset{
		{Name: fmt.Sprintf("turnecho-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH), URL: "https://x/bundle"},
	}})
	if err == nil || !strings.Contains(err.Error(), "no checksums") {
		t.Fatalf("checksumless release accepted: %v", err)
	}
	bundle, sums, err := SelectAsset(Release{Tag: "v2.1.0", Assets: []ReleaseAsset{
		{Name: "turnecho-plan9-386.tar.gz", URL: "https://x/other"},
		{Name: fmt.Sprintf("turnecho-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH), URL: "https://x/bundle"},
		{Name: "SHA256SUMS", URL: "https://x/sums"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.URL != "https://x/bundle" || sums.URL != "https://x/sums" {
		t.Fatalf("bundle = %+v, sums = %+v", bundle, sums)
	}
}
