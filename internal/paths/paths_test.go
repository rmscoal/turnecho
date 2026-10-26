package paths

import (
	"path/filepath"
	"testing"
)

func TestPathsFollowHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := map[string]func() (string, error){
		"config.json": ConfigFile,
		"config.lock": ConfigLock,
		"turnecho.db": Database,
		"worker.lock": WorkerLock,
		"worker.log":  WorkerLog,
		"player.pid":  PlayerPid,
	}
	for name, resolve := range cases {
		got, err := resolve()
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		want := filepath.Join(home, ".config", "turnecho", name)
		if got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}
