package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rmscoal/turnecho/internal/install"
	"github.com/rmscoal/turnecho/internal/tts"
)

func writeFakeHostCLI(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func stubInstallDeps(t *testing.T, url string) {
	t.Helper()
	previousURL, previousOpen, previousProbe, previousExe, previousRunner := installModelURL, installOpen, installProbe, installExe, installRunner
	installModelURL = url
	installOpen = func(string) (tts.Engine, error) { return tts.SilentBackend{}, nil }
	installProbe = func() (string, error) { return "/fake/afplay", nil }
	exe := filepath.Join(t.TempDir(), "turnecho")
	if err := os.WriteFile(exe, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	installExe = func() (string, error) { return exe, nil }
	installRunner = fakeHostRunner()
	t.Cleanup(func() {
		installModelURL, installOpen, installProbe, installExe, installRunner = previousURL, previousOpen, previousProbe, previousExe, previousRunner
	})
}

// fakeHosts pretends to be both host CLIs, recording registrations.
type fakeHosts struct {
	markets map[string]bool
	plugins map[string]bool
	failOn  string
}

func newFakeHosts() *fakeHosts {
	return &fakeHosts{markets: map[string]bool{}, plugins: map[string]bool{}}
}

func (f *fakeHosts) run(name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	if f.failOn != "" && strings.Contains(call, f.failOn) {
		return "", fmt.Errorf("fake failure: %s", call)
	}
	pluginKey, pluginList := "pluginId", `{"installed": [%s]}`
	if name == "claude" {
		pluginKey = "id"
		pluginList = `[%s]`
	}
	switch {
	case strings.Contains(call, "marketplace list"):
		var parts []string
		for market := range f.markets {
			parts = append(parts, `{"name": "`+market+`"}`)
		}
		if name == "codex" {
			return `{"marketplaces": [` + strings.Join(parts, ", ") + `]}`, nil
		}
		return `[` + strings.Join(parts, ", ") + `]`, nil
	case strings.Contains(call, "marketplace add"):
		f.markets[install.MarketplaceName] = true
		return "{}", nil
	case strings.Contains(call, "marketplace remove"):
		delete(f.markets, install.MarketplaceName)
		return "{}", nil
	case strings.Contains(call, "plugin list"):
		var parts []string
		for id := range f.plugins {
			parts = append(parts, `{"`+pluginKey+`": "`+id+`"}`)
		}
		return fmt.Sprintf(pluginList, strings.Join(parts, ", ")), nil
	case strings.Contains(call, "plugin add"), strings.Contains(call, "plugin install"):
		f.plugins[install.PluginID] = true
		return "{}", nil
	case strings.Contains(call, "plugin remove"), strings.Contains(call, "plugin uninstall"):
		delete(f.plugins, install.PluginID)
		return "{}", nil
	default:
		return "", fmt.Errorf("unexpected call: %s", call)
	}
}

// fakeHostRunner pretends both host CLIs start empty and accept everything.
func fakeHostRunner() install.Runner {
	return newFakeHosts().run
}

func fixtureTarball(t *testing.T, parent, name string) string {
	t.Helper()
	root := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(root, "espeak-ng-data"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"model.onnx", "voices.bin", "tokens.txt"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "kokoro.tar.bz2")
	if err := exec.Command("tar", "-cjf", out, "-C", parent, name).Run(); err != nil {
		t.Skip("system tar not available")
	}
	return out
}

func TestInstallDryRunPlans(t *testing.T) {
	home := isolateHome(t)
	bin := t.TempDir()
	writeFakeHostCLI(t, bin, "codex")
	writeFakeHostCLI(t, bin, "claude")
	t.Setenv("PATH", bin)

	code, stdout, _ := run(t, "", "install", "--dry-run")
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %q", code, stdout)
	}
	for _, want := range []string{"Hosts: claude, codex", "kokoro-en-v0_19", home, ".local/bin/turnecho", "Plugin: turnecho@turnecho via rmscoal/turnecho"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
}

func TestInstallRejectsUnknownHost(t *testing.T) {
	isolateHome(t)
	code, _, _ := run(t, "", "install", "--host", "siri")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	code, _, _ = run(t, "", "install", "--host", "all", "--host", "codex")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestInstallRequiresDetectedHost(t *testing.T) {
	isolateHome(t)
	t.Setenv("PATH", "/nonexistent-bin-dir")
	code, _, _ := run(t, "", "install", "--dry-run")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestInstallFetchesModelAndPreflights(t *testing.T) {
	home := isolateHome(t)
	bin := t.TempDir()
	writeFakeHostCLI(t, bin, "codex")
	t.Setenv("PATH", bin)
	parent := t.TempDir()
	archive := fixtureTarball(t, parent, "kokoro-en-v0_19")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, archive)
	}))
	t.Cleanup(server.Close)
	stubInstallDeps(t, server.URL)

	code, stdout, _ := run(t, "", "install", "--host", "codex")
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %q", code, stdout)
	}
	for _, want := range []string{"Model ready.", "Runtime check passed.", "Linked ", "Registered codex plugin.", "TurnEcho installed."} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	link := filepath.Join(home, ".local", "bin", "turnecho")
	if _, err := os.Readlink(link); err != nil {
		t.Fatalf("command link missing: %v", err)
	}
	model := filepath.Join(home, ".local", "share", "turnecho", "runtimes", "kokoro-en-v0_19", "model.onnx")
	if _, err := os.Stat(model); err != nil {
		t.Fatalf("model missing: %v", err)
	}
}

func TestInstallRollsBackLinkAndHosts(t *testing.T) {
	home := isolateHome(t)
	bin := t.TempDir()
	writeFakeHostCLI(t, bin, "codex")
	writeFakeHostCLI(t, bin, "claude")
	t.Setenv("PATH", bin)
	parent := t.TempDir()
	archive := fixtureTarball(t, parent, "kokoro-en-v0_19")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, archive)
	}))
	t.Cleanup(server.Close)
	stubInstallDeps(t, server.URL)
	previous := installRunner
	fakes := newFakeHosts()
	fakes.failOn = "claude plugin install"
	installRunner = fakes.run
	t.Cleanup(func() { installRunner = previous })

	code, _, _ := run(t, "", "install", "--host", "codex", "--host", "claude")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	link := filepath.Join(home, ".local", "bin", "turnecho")
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("link left behind after rollback")
	}
	if len(fakes.markets) != 0 || len(fakes.plugins) != 0 {
		t.Fatalf("host registration left behind: %+v", fakes)
	}
}
