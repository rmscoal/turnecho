package install

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func fakeExeFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "turnecho")
	if err := os.WriteFile(path, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func managedLink(t *testing.T, home, exe string) string {
	t.Helper()
	link := filepath.Join(home, ".local", "bin", "turnecho")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func markedRuntime(t *testing.T, home, name string) string {
	t.Helper()
	dir := filepath.Join(home, ".local", "share", "turnecho", "runtimes", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	marker := `{"name": "turnecho-runtime", "model": "kokoro", "sha256": "abc"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, RuntimeMarkerName), []byte(marker), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPlanUninstallInventories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/nonexistent-bin-dir")
	exe := fakeExeFile(t)
	managedLink(t, home, exe)
	marked := markedRuntime(t, home, "kokoro-en-v0_19")
	unknown := filepath.Join(home, ".local", "share", "turnecho", "runtimes", "other")
	if err := os.MkdirAll(unknown, 0o755); err != nil {
		t.Fatal(err)
	}

	plan, err := PlanUninstall(nil, exe, newCodexFake().run)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Hosts) != 0 {
		t.Fatalf("hosts = %v, want none without CLIs", plan.Hosts)
	}
	if !plan.RemoveLink {
		t.Fatal("owned link not slated for removal")
	}
	if !slices.Equal(plan.Runtimes, []string{marked}) {
		t.Fatalf("runtimes = %v", plan.Runtimes)
	}
	if !slices.Equal(plan.PreservedRuntimes, []string{unknown}) {
		t.Fatalf("preserved = %v", plan.PreservedRuntimes)
	}
}

func TestExecuteUninstallRemovesAndRerunsClean(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/nonexistent-bin-dir")
	exe := fakeExeFile(t)
	link := managedLink(t, home, exe)
	marked := markedRuntime(t, home, "kokoro-en-v0_19")

	plan, err := PlanUninstall(nil, exe, newCodexFake().run)
	if err != nil {
		t.Fatal(err)
	}
	report, err := plan.Execute(newCodexFake().run, exe)
	if err != nil {
		t.Fatal(err)
	}
	if !report.RemovedLink {
		t.Fatal("link removal not reported")
	}
	if !slices.Equal(report.RemovedRuntimes, []string{marked}) {
		t.Fatalf("removed runtimes = %v", report.RemovedRuntimes)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("link left behind")
	}

	// Rerun against the cleaned machine: nothing to do, no error.
	plan, err = PlanUninstall(nil, exe, newCodexFake().run)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RemoveLink || len(plan.Runtimes) != 0 {
		t.Fatalf("rerun plan not empty: %+v", plan)
	}
	if _, err := plan.Execute(newCodexFake().run, exe); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteUninstallPreservesForeignLink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/nonexistent-bin-dir")
	exe := fakeExeFile(t)
	other, err := os.CreateTemp(t.TempDir(), "other")
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
	link := filepath.Join(home, ".local", "bin", "turnecho")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other.Name(), link); err != nil {
		t.Fatal(err)
	}

	plan, err := PlanUninstall(nil, exe, newCodexFake().run)
	if err != nil {
		t.Fatal(err)
	}
	if plan.RemoveLink || plan.PreservedLink != link {
		t.Fatalf("foreign link misjudged: %+v", plan)
	}
	report, err := plan.Execute(newCodexFake().run, exe)
	if err != nil {
		t.Fatal(err)
	}
	if report.RemovedLink {
		t.Fatal("foreign link removed")
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("foreign link disturbed: %v", err)
	}
}

func TestExecuteUninstallRefusesSwappedLink(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/nonexistent-bin-dir")
	exe := fakeExeFile(t)
	link := managedLink(t, home, exe)
	markedRuntime(t, home, "kokoro-en-v0_19")

	plan, err := PlanUninstall(nil, exe, newCodexFake().run)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.RemoveLink {
		t.Fatal("owned link not slated")
	}
	other, err := os.Create(t.TempDir() + "/other")
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other.Name(), link); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Execute(newCodexFake().run, exe); err == nil {
		t.Fatal("swapped link accepted")
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatal("swapped link disturbed")
	}
}

func TestExecuteUninstallStopsOnHostFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	exe := fakeExeFile(t)
	link := managedLink(t, home, exe)
	markedRuntime(t, home, "kokoro-en-v0_19")

	fake := newCodexFake()
	fake.markets[MarketplaceName] = true
	fake.plugins[PluginID] = true
	fake.failOn = "plugin remove"

	plan, err := PlanUninstall([]Host{HostCodex}, exe, fake.run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Execute(fake.run, exe); err == nil {
		t.Fatal("host failure accepted")
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatal("link removed despite host failure")
	}
	if _, err := os.Stat(markedRuntime(t, home, "kokoro-en-v0_19")); err != nil {
		t.Fatalf("runtime removed despite host failure: %v", err)
	}
}

func TestResolvePresentHostsSkipsAbsent(t *testing.T) {
	t.Setenv("PATH", "/nonexistent-bin-dir")
	if got := ResolvePresentHosts(nil); len(got) != 0 {
		t.Fatalf("detected = %v", got)
	}
	if got := ResolvePresentHosts([]Host{HostCodex}); len(got) != 0 {
		t.Fatalf("absent CLI kept: %v", got)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got := ResolvePresentHosts([]Host{HostCodex, HostClaude}); !slices.Equal(got, []Host{HostCodex}) {
		t.Fatalf("present = %v", got)
	}
}
