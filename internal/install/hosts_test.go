package install

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeHost pretends to be a host CLI with a marketplace and plugin set.
type fakeHost struct {
	binary     string
	markets    map[string]bool
	plugins    map[string]bool
	calls      []string
	failOn     string
	marketJSON func() string
	pluginJSON func() string
}

func newCodexFake() *fakeHost {
	fake := &fakeHost{binary: "codex", markets: map[string]bool{}, plugins: map[string]bool{}}
	fake.marketJSON = func() string {
		var names []string
		for name := range fake.markets {
			names = append(names, `{"name": "`+name+`"}`)
		}
		return `{"marketplaces": [` + strings.Join(names, ", ") + `]}`
	}
	fake.pluginJSON = func() string {
		var ids []string
		for id := range fake.plugins {
			ids = append(ids, `{"pluginId": "`+id+`"}`)
		}
		return `{"installed": [` + strings.Join(ids, ", ") + `]}`
	}
	return fake
}

func newClaudeFake() *fakeHost {
	fake := &fakeHost{binary: "claude", markets: map[string]bool{}, plugins: map[string]bool{}}
	fake.marketJSON = func() string {
		var names []string
		for name := range fake.markets {
			names = append(names, `{"name": "`+name+`"}`)
		}
		return `[` + strings.Join(names, ", ") + `]`
	}
	fake.pluginJSON = func() string {
		var ids []string
		for id := range fake.plugins {
			ids = append(ids, `{"id": "`+id+`"}`)
		}
		return `[` + strings.Join(ids, ", ") + `]`
	}
	return fake
}

func (f *fakeHost) run(name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if f.failOn != "" && strings.Contains(call, f.failOn) {
		return "", errors.New("fake failure: " + call)
	}
	switch {
	case strings.Contains(call, "marketplace list"):
		return f.marketJSON(), nil
	case strings.Contains(call, "marketplace add"):
		f.markets[MarketplaceName] = true
		return "{}", nil
	case strings.Contains(call, "marketplace remove"):
		delete(f.markets, MarketplaceName)
		return "{}", nil
	case strings.Contains(call, "plugin list"):
		return f.pluginJSON(), nil
	case strings.Contains(call, "plugin add"), strings.Contains(call, "plugin install"):
		f.plugins[PluginID] = true
		return "{}", nil
	case strings.Contains(call, "plugin remove"), strings.Contains(call, "plugin uninstall"):
		delete(f.plugins, PluginID)
		return "{}", nil
	default:
		return "", fmt.Errorf("unexpected call: %s", call)
	}
}

func hostOf(t *testing.T, fake *fakeHost) Host {
	t.Helper()
	host, err := ParseHost(fake.binary)
	if err != nil {
		t.Fatal(err)
	}
	return host
}

func TestEnsureHostRegistersFresh(t *testing.T) {
	for _, fake := range []*fakeHost{newCodexFake(), newClaudeFake()} {
		undo, err := EnsureHost(hostOf(t, fake), fake.run)
		if err != nil {
			t.Fatalf("%s: %v", fake.binary, err)
		}
		if !fake.markets[MarketplaceName] || !fake.plugins[PluginID] {
			t.Fatalf("%s: not registered: %v", fake.binary, fake.calls)
		}
		if err := undo(); err != nil {
			t.Fatal(err)
		}
		if fake.markets[MarketplaceName] || fake.plugins[PluginID] {
			t.Fatalf("%s: rollback incomplete: %v", fake.binary, fake.calls)
		}
	}
}

func TestEnsureHostSkipsRegistered(t *testing.T) {
	for _, fake := range []*fakeHost{newCodexFake(), newClaudeFake()} {
		fake.markets[MarketplaceName] = true
		fake.plugins[PluginID] = true
		undo, err := EnsureHost(hostOf(t, fake), fake.run)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range fake.calls {
			if strings.Contains(call, " add ") || strings.Contains(call, " install ") {
				t.Fatalf("changed already-registered host: %v", fake.calls)
			}
		}
		if err := undo(); err != nil {
			t.Fatal(err)
		}
		if !fake.markets[MarketplaceName] || !fake.plugins[PluginID] {
			t.Fatal("no-op rollback removed pre-existing registration")
		}
	}
}

func TestEnsureHostRollsBackPartial(t *testing.T) {
	fake := newCodexFake()
	fake.failOn = "plugin add"
	if _, err := EnsureHost(hostOf(t, fake), fake.run); err == nil {
		t.Fatal("failed add accepted")
	}
	if fake.markets[MarketplaceName] {
		t.Fatalf("marketplace left behind: %v", fake.calls)
	}
	removed := false
	for _, call := range fake.calls {
		if strings.Contains(call, "marketplace remove") {
			removed = true
		}
	}
	if !removed {
		t.Fatalf("no marketplace rollback: %v", fake.calls)
	}
}

func TestEnsureHostRejectsUnknown(t *testing.T) {
	if _, err := EnsureHost("siri", newCodexFake().run); err == nil {
		t.Fatal("unknown host accepted")
	}
}
