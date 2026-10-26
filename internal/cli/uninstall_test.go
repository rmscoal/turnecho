package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rmscoal/turnecho/internal/install"
)

func TestUninstallDryRunPlans(t *testing.T) {
	home := isolateHome(t)
	bin := t.TempDir()
	writeFakeHostCLI(t, bin, "codex")
	t.Setenv("PATH", bin)
	link := filepath.Join(home, ".local", "bin", "turnecho")
	exe := filepath.Join(t.TempDir(), "turnecho")
	if err := os.WriteFile(exe, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	rt := filepath.Join(home, ".local", "share", "turnecho", "runtimes", "kokoro-en-v0_19")
	if err := os.MkdirAll(rt, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := `{"name": "` + install.RuntimeMarkerKind + `", "model": "kokoro", "sha256": "x"}` + "\n"
	if err := os.WriteFile(filepath.Join(rt, install.RuntimeMarkerName), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}

	previous := installExe
	installExe = func() (string, error) { return exe, nil }
	t.Cleanup(func() { installExe = previous })
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}

	code, stdout, _ := run(t, "", "uninstall", "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %q", code, stdout)
	}
	for _, want := range []string{"Host: codex", "Link: remove", "Runtime: remove"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatal("dry-run removed the link")
	}
}
