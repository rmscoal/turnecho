package config

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaultsWhenMissing(t *testing.T) {
	got, err := LoadFile(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := Defaults(); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestLoadStrict(t *testing.T) {
	valid := `{"schema_version":2,"enabled":false,"model":"kokoro","voice":"speaker-3","speed":1.5}`
	got, err := LoadFile(writeFile(t, valid))
	if err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if got.Enabled || got.Model != "kokoro" || got.Voice != "speaker-3" || got.Speed != 1.5 {
		t.Errorf("unexpected config: %+v", got)
	}

	cases := map[string]string{
		"not an object":  `[1,2]`,
		"broken json":    `{"enabled":`,
		"old schema":     `{"schema_version":1,"enabled":true,"model":"mini","voice":"Hugo","speed":1.0}`,
		"unknown key":    `{"schema_version":2,"enabled":true,"model":"kokoro","voice":"speaker-0","speed":1.0,"extra":1}`,
		"unknown keys":   `{"schema_version":2,"enabled":true,"model":"kokoro","voice":"speaker-0","speed":1.0,"zeta":1,"alpha":2}`,
		"missing key":    `{"schema_version":2,"enabled":true,"model":"kokoro","voice":"speaker-0"}`,
		"bool version":   `{"schema_version":true,"enabled":true,"model":"kokoro","voice":"speaker-0","speed":1.0}`,
		"non-bool onoff": `{"schema_version":2,"enabled":"yes","model":"kokoro","voice":"speaker-0","speed":1.0}`,
		"unknown model":  `{"schema_version":2,"enabled":true,"model":"mini","voice":"speaker-0","speed":1.0}`,
		"unknown voice":  `{"schema_version":2,"enabled":true,"model":"kokoro","voice":"Hugo","speed":1.0}`,
		"bool speed":     `{"schema_version":2,"enabled":true,"model":"kokoro","voice":"speaker-0","speed":true}`,
		"slow speed":     `{"schema_version":2,"enabled":true,"model":"kokoro","voice":"speaker-0","speed":0.1}`,
		"fast speed":     `{"schema_version":2,"enabled":true,"model":"kokoro","voice":"speaker-0","speed":3.0}`,
		"huge speed":     `{"schema_version":2,"enabled":true,"model":"kokoro","voice":"speaker-0","speed":1e999}`,
		"string speed":   `{"schema_version":2,"enabled":true,"model":"kokoro","voice":"speaker-0","speed":"fast"}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := LoadFile(writeFile(t, content))
			var configErr *Error
			if !errors.As(err, &configErr) {
				t.Errorf("got %v, want a config error", err)
			}
		})
	}
}

func TestSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	want := Config{SchemaVersion: SchemaVersion, Enabled: false, Model: "kokoro", Voice: "speaker-9", Speed: 0.5}
	if err := SaveFile(want, path); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %o, want 600", info.Mode().Perm())
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	bad := Defaults()
	bad.Voice = "Nobody"
	if err := SaveFile(bad, path); err == nil {
		t.Fatal("invalid config saved")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("invalid save created a file")
	}
}

func TestUpdateAndReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	updated, err := UpdateFile(path, func(current *Config) {
		current.Voice = "speaker-5"
		current.Speed = 1.25
	})
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if updated.Voice != "speaker-5" || updated.Speed != 1.25 || !updated.Enabled {
		t.Errorf("unexpected update: %+v", updated)
	}
	reset, err := ResetFile(path, "voice")
	if err != nil {
		t.Fatalf("reset failed: %v", err)
	}
	if reset.Voice != DefaultVoice || reset.Speed != 1.25 {
		t.Errorf("reset touched too much: %+v", reset)
	}
	all, err := ResetFile(path, "")
	if err != nil {
		t.Fatalf("reset all failed: %v", err)
	}
	if all != Defaults() {
		t.Errorf("reset all gave %+v, want defaults", all)
	}
	if _, err := ResetFile(path, "bogus"); err == nil {
		t.Error("unknown reset key accepted")
	}
}

func TestConcurrentUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	var group sync.WaitGroup
	for i := range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := UpdateFile(path, func(current *Config) {
				if i%2 == 0 {
					current.Speed = 1.5
				} else {
					current.Speed = 0.75
				}
			})
			if err != nil {
				t.Errorf("concurrent update failed: %v", err)
			}
		}()
	}
	group.Wait()
	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("final load failed: %v", err)
	}
	if got.Speed != 1.5 && got.Speed != 0.75 {
		t.Errorf("corrupt final speed: %v", got.Speed)
	}
}
