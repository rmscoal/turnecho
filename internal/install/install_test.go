package install

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rmscoal/turnecho/internal/tts"
)

func TestParseHost(t *testing.T) {
	for _, host := range Hosts() {
		got, err := ParseHost(string(host))
		if err != nil || got != host {
			t.Fatalf("ParseHost(%q) = %v, %v", host, got, err)
		}
	}
	if _, err := ParseHost("all"); err == nil {
		t.Fatal("ParseHost accepted all")
	}
	if _, err := ParseHost(""); err == nil {
		t.Fatal("ParseHost accepted empty")
	}
}

func TestDetectHostsEmptyPATH(t *testing.T) {
	t.Setenv("PATH", "/nonexistent-bin-dir")
	if got := DetectHosts(); len(got) != 0 {
		t.Fatalf("DetectHosts with empty PATH = %v", got)
	}
}

func TestRollbackAllRunsReverseAndCollects(t *testing.T) {
	var order []string
	ok := func(name string) Rollback {
		return func() error { order = append(order, name); return nil }
	}
	bad := errors.New("bad")
	undos := []Rollback{ok("first"), func() error { order = append(order, "bad"); return bad }, ok("last")}
	errs := RollbackAll(undos)
	if len(errs) != 1 || !errors.Is(errs[0], bad) {
		t.Fatalf("errors = %v", errs)
	}
	want := []string{"last", "bad", "first"}
	if !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	if errs := RollbackAll(nil); len(errs) != 0 {
		t.Fatalf("empty rollback errored: %v", errs)
	}
}

func TestResolveHostsRequiresDetectedCLI(t *testing.T) {
	t.Setenv("PATH", "/nonexistent-bin-dir")
	if _, err := ResolveHosts(nil); err == nil {
		t.Fatal("ResolveHosts accepted empty detection")
	}
	if _, err := ResolveHosts([]Host{HostCodex}); err == nil {
		t.Fatal("ResolveHosts accepted missing codex CLI")
	}
}

func TestResolveHostsSortsRequested(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"codex", "claude"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	got, err := ResolveHosts([]Host{HostCodex, HostClaude})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []Host{HostClaude, HostCodex}) {
		t.Fatalf("ResolveHosts = %v", got)
	}
}

func TestHomeModelDirLivesUnderRuntimes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := tts.HomeModelDir("kokoro")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "share", "turnecho", "runtimes", "kokoro-en-v0_19")
	if dir != want {
		t.Fatalf("dir = %q, want %q", dir, want)
	}
	if _, err := tts.HomeModelDir("unknown"); err == nil {
		t.Fatal("unknown model accepted")
	}
}
