package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rmscoal/turnecho/internal/config"
	"github.com/rmscoal/turnecho/internal/hosts"
	"github.com/rmscoal/turnecho/internal/paths"
	"github.com/rmscoal/turnecho/internal/queue"
	"github.com/rmscoal/turnecho/internal/speak"
	"github.com/rmscoal/turnecho/internal/tts"
	"github.com/rmscoal/turnecho/internal/version"
	"github.com/rmscoal/turnecho/internal/worker"
)

func run(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func isolateHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func stubSpawn(t *testing.T) *int {
	t.Helper()
	calls := 0
	previous := spawnWorker
	spawnWorker = func() error { calls++; return nil }
	t.Cleanup(func() { spawnWorker = previous })
	return &calls
}

func stubPlay(t *testing.T) *[]string {
	t.Helper()
	var played []string
	previous := play
	play = func(path string) error { played = append(played, path); return nil }
	t.Cleanup(func() { play = previous })
	return &played
}

func stubTTS(t *testing.T) {
	t.Helper()
	previous := openBackend
	openBackend = func(string) (tts.Engine, error) { return tts.SilentBackend{}, nil }
	t.Cleanup(func() { openBackend = previous })
}

type speechEngine struct {
	texts  []string
	closes int
	fail   error
}

func (e *speechEngine) Synthesize(text, _ string, _ float64) ([]int16, error) {
	e.texts = append(e.texts, text)
	return []int16{1, -1}, e.fail
}

func (e *speechEngine) Close() { e.closes++ }

func useEngine(t *testing.T, engine tts.Engine, err error) {
	t.Helper()
	previous := openBackend
	openBackend = func(string) (tts.Engine, error) { return engine, err }
	t.Cleanup(func() { openBackend = previous })
}

func TestSpeechChunksCloseAndCleanUp(t *testing.T) {
	isolateHome(t)
	engine := &speechEngine{}
	useEngine(t, engine, nil)
	played := stubPlay(t)
	code, _, stderr := run(t, "", "say", "First sentence. Second sentence.")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d error=%s", code, stderr)
	}
	if strings.Join(engine.texts, "|") != "First sentence.|Second sentence." || engine.closes != 1 || len(*played) != 2 {
		t.Fatalf("texts=%v closes=%d played=%d", engine.texts, engine.closes, len(*played))
	}
	for _, path := range *played {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("temporary WAV retained: %s", path)
		}
	}
}

func TestSpeechFailureClosesModel(t *testing.T) {
	isolateHome(t)
	engine := &speechEngine{fail: errors.New("inference failed")}
	useEngine(t, engine, nil)
	played := stubPlay(t)
	code, _, stderr := run(t, "", "test")
	if code != ExitFailed || !strings.Contains(stderr, "inference failed") || engine.closes != 1 || len(*played) != 0 {
		t.Fatalf("code=%d error=%s closes=%d played=%d", code, stderr, engine.closes, len(*played))
	}
}

func TestSpeechCommandsRejectMissingRuntime(t *testing.T) {
	home := isolateHome(t)
	installFakePlayer(t)
	useEngine(t, nil, errors.New("Kokoro runtime missing"))
	for _, args := range [][]string{{"doctor", "--json"}, {"test"}, {"say", "Hello", "--output", filepath.Join(home, "out.wav")}} {
		code, stdout, stderr := run(t, "", args...)
		if code != ExitFailed || stdout != "" || !strings.Contains(stderr, "Kokoro runtime missing") {
			t.Fatalf("args=%v code=%d out=%s error=%s", args, code, stdout, stderr)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "out.wav")); !os.IsNotExist(err) {
		t.Fatal("missing runtime wrote WAV")
	}
}

func TestConfigShowDefaults(t *testing.T) {
	isolateHome(t)
	code, stdout, stderr := run(t, "", "config", "show")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	want := "enabled: true\nmodel: kokoro\nvoice: speaker-0\nspeed: 1\n"
	if stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestConfigShowJSONGolden(t *testing.T) {
	isolateHome(t)
	code, stdout, stderr := run(t, "", "config", "show", "--json")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	want := `{"enabled":true,"model":"kokoro","schema_version":2,"speed":1,"voice":"speaker-0"}` + "\n"
	if stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestConfigPath(t *testing.T) {
	home := isolateHome(t)
	code, stdout, _ := run(t, "", "config", "path")
	if code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	want := filepath.Join(home, ".config", "turnecho", "config.json") + "\n"
	if stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
}

func TestConfigSetAndReset(t *testing.T) {
	isolateHome(t)
	code, stdout, _ := run(t, "", "config", "set", "voice", "speaker-4")
	if code != ExitOK {
		t.Fatalf("code=%d out=%q", code, stdout)
	}
	if !strings.Contains(stdout, "voice: speaker-4") {
		t.Errorf("set output missing voice: %q", stdout)
	}
	code, _, _ = run(t, "", "config", "set", "speed", "1.5")
	if code != ExitOK {
		t.Fatalf("set speed code=%d", code)
	}
	code, stdout, _ = run(t, "", "config", "show")
	if code != ExitOK || !strings.Contains(stdout, "speed: 1.5") {
		t.Errorf("show after set: code=%d out=%q", code, stdout)
	}
	code, stdout, _ = run(t, "", "config", "reset", "voice")
	if code != ExitOK || !strings.Contains(stdout, "voice: speaker-0") {
		t.Errorf("reset voice: code=%d out=%q", code, stdout)
	}
	if !strings.Contains(stdout, "speed: 1.5") {
		t.Errorf("reset voice touched speed: %q", stdout)
	}
	code, stdout, _ = run(t, "", "config", "reset", "--all")
	if code != ExitOK || !strings.Contains(stdout, "speed: 1") {
		t.Errorf("reset all: code=%d out=%q", code, stdout)
	}
}

func TestConfigSetErrors(t *testing.T) {
	isolateHome(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown voice", []string{"config", "set", "voice", "Nobody"}, "Unsupported voice"},
		{"unknown model", []string{"config", "set", "model", "mini"}, "Unsupported model"},
		{"bad speed", []string{"config", "set", "speed", "fast"}, "Speed must be a number"},
		{"slow speed", []string{"config", "set", "speed", "0.1"}, "must be between"},
		{"missing value", []string{"config", "set", "voice"}, "accepts 2 arg(s)"},
		{"bad key", []string{"config", "set", "enabled", "true"}, "unknown config key"},
		{"reset needs target", []string{"config", "reset"}, "pass a key or --all"},
		{"reset bad key", []string{"config", "reset", "bogus"}, "unknown reset key"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := run(t, "", tc.args...)
			if code != ExitConfig {
				t.Errorf("code=%d, want %d", code, ExitConfig)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr %q missing %q", stderr, tc.want)
			}
		})
	}
}

func TestConfigCorruptFile(t *testing.T) {
	home := isolateHome(t)
	path := filepath.Join(home, ".config", "turnecho", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := run(t, "", "config", "show")
	if code != ExitConfig {
		t.Errorf("code=%d, want %d", code, ExitConfig)
	}
	if !strings.Contains(stderr, "TurnEcho configuration error") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestEnableDisable(t *testing.T) {
	isolateHome(t)
	_, stdout, _ := run(t, "", "disable")
	if stdout != "TurnEcho disabled.\n" {
		t.Errorf("disable gave %q", stdout)
	}
	_, stdout, _ = run(t, "", "enable")
	if stdout != "TurnEcho enabled.\n" {
		t.Errorf("enable gave %q", stdout)
	}
}

func TestVoicesGolden(t *testing.T) {
	isolateHome(t)
	_, stdout, _ := run(t, "", "voices", "--json")
	var decoded struct {
		Voices []string `json:"voices"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("voices output is not JSON: %v", err)
	}
	if len(decoded.Voices) != len(config.Voices) || decoded.Voices[0] != config.DefaultVoice {
		t.Errorf("unexpected voices: %q", decoded.Voices)
	}
	_, stdout, _ = run(t, "", "voices")
	if !strings.HasPrefix(stdout, "speaker-0 (default)\n") {
		t.Errorf("human voices missing default marker: %q", stdout)
	}
}

func TestModelsGolden(t *testing.T) {
	isolateHome(t)
	_, stdout, _ := run(t, "", "models", "--json")
	want := `{"default":"kokoro","models":{"kokoro":"kokoro-en-v0_19"}}` + "\n"
	if stdout != want {
		t.Errorf("got %q, want %q", stdout, want)
	}
	_, stdout, _ = run(t, "", "models")
	if stdout != "kokoro (default): kokoro-en-v0_19\n" {
		t.Errorf("human models gave %q", stdout)
	}
}

func playerName() string {
	if runtime.GOOS == "darwin" {
		return "afplay"
	}
	return "aplay"
}

func installFakePlayer(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, playerName()), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return bin
}

func TestDoctor(t *testing.T) {
	stubTTS(t)
	home := isolateHome(t)
	bin := installFakePlayer(t)
	code, stdout, stderr := run(t, "", "doctor", "--json")
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	var decoded struct {
		Status     string  `json:"status"`
		ConfigPath string  `json:"config_path"`
		Model      string  `json:"model"`
		ModelID    string  `json:"model_id"`
		Voice      string  `json:"voice"`
		Speed      float64 `json:"speed"`
		Rate       int     `json:"audio_sample_rate"`
		Player     string  `json:"player"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("doctor output is not JSON: %v", err)
	}
	if decoded.Status != "ok" || decoded.Model != "kokoro" || decoded.ModelID != "kokoro-en-v0_19" {
		t.Errorf("unexpected doctor payload: %+v", decoded)
	}
	if decoded.ConfigPath != filepath.Join(home, ".config", "turnecho", "config.json") {
		t.Errorf("config path = %q", decoded.ConfigPath)
	}
	if decoded.Player != filepath.Join(bin, playerName()) {
		t.Errorf("player = %q", decoded.Player)
	}
	if decoded.Rate != tts.SampleRate {
		t.Errorf("rate = %d", decoded.Rate)
	}
	code, stdout, _ = run(t, "", "doctor")
	if code != ExitOK || stdout != "TurnEcho configuration and audio runtime are ready.\n" {
		t.Errorf("human doctor: code=%d out=%q", code, stdout)
	}
}

func TestDoctorWithoutPlayer(t *testing.T) {
	isolateHome(t)
	t.Setenv("PATH", t.TempDir())
	code, _, stderr := run(t, "", "doctor")
	if code != ExitFailed {
		t.Errorf("code=%d, want %d", code, ExitFailed)
	}
	if !strings.Contains(stderr, playerName()) {
		t.Errorf("stderr %q does not name the player", stderr)
	}
}

func TestAudioCommands(t *testing.T) {
	stubTTS(t)
	isolateHome(t)
	played := stubPlay(t)
	code, stdout, _ := run(t, "", "test")
	if code != ExitOK || stdout != "TurnEcho audio test completed.\n" {
		t.Errorf("test: code=%d out=%q", code, stdout)
	}
	if len(*played) != 1 {
		t.Fatalf("test played %d files", len(*played))
	}
	code, stdout, _ = run(t, "", "say", "hello")
	if code != ExitOK || stdout != "Spoke 5 characters.\n" {
		t.Errorf("say: code=%d out=%q", code, stdout)
	}
	if len(*played) != 2 {
		t.Errorf("say did not play: %d files", len(*played))
	}
	code, _, _ = run(t, "", "say")
	if code != ExitConfig {
		t.Errorf("say without text code=%d, want %d", code, ExitConfig)
	}
}

func TestSayToFile(t *testing.T) {
	stubTTS(t)
	home := isolateHome(t)
	output := filepath.Join(home, "out.wav")
	code, stdout, stderr := run(t, "", "say", "hello", "--output", output)
	if code != ExitOK || stderr != "" {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	if stdout != "Wrote "+output+"\n" {
		t.Errorf("say output = %q", stdout)
	}
	content, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != 44+2*tts.SampleRate || string(content[0:4]) != "RIFF" {
		t.Errorf("invalid wav: %d bytes", len(content))
	}
}

func TestStopIdle(t *testing.T) {
	isolateHome(t)
	code, stdout, _ := run(t, "", "stop")
	if code != ExitOK || stdout != "Nothing is playing.\n" {
		t.Errorf("stop: code=%d out=%q", code, stdout)
	}
}

func promptPayload(hostFields string) string {
	return `{"hook_event_name":"UserPromptSubmit",` + hostFields + `}`
}

func stopPayload(message string) string {
	escaped, _ := json.Marshal(message)
	return `{"hook_event_name":"Stop","session_id":"s1","turn_id":"t1","last_assistant_message":` + string(escaped) + `}`
}

func expectedEnvelope(t *testing.T) string {
	t.Helper()
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.Encode(struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}{
		HookSpecificOutput: struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		}{HookEventName: "UserPromptSubmit", AdditionalContext: speak.Instruction},
	})
	return buffer.String()
}

func claimOnly(t *testing.T) *queue.Job {
	t.Helper()
	dbPath, err := paths.Database()
	if err != nil {
		t.Fatal(err)
	}
	db, err := queue.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	job, err := db.Claim()
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestHookPrompt(t *testing.T) {
	isolateHome(t)
	spawns := stubSpawn(t)
	code, stdout, stderr := run(t, "", "hook", "prompt")
	if code != ExitOK || stdout != "{}\n" || stderr == "" {
		t.Errorf("empty stdin: code=%d out=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = run(t, promptPayload(`"session_id":"s1"`), "hook", "prompt")
	if code != ExitOK || stdout != expectedEnvelope(t) || stderr != "" {
		t.Errorf("valid prompt: code=%d out=%q stderr=%q", code, stdout, stderr)
	}
	if *spawns != 1 {
		t.Errorf("spawned %d workers, want 1", *spawns)
	}
	code, stdout, stderr = run(t, promptPayload(`"session_id":"s1","transcript_path":"/tmp/x"`), "hook", "prompt")
	if code != ExitOK || stdout != expectedEnvelope(t) || stderr != "" {
		t.Errorf("valid claude prompt: code=%d out=%q stderr=%q", code, stdout, stderr)
	}
	if *spawns != 2 {
		t.Errorf("spawned %d workers, want 2", *spawns)
	}
}

func TestHookPromptVariants(t *testing.T) {
	isolateHome(t)
	spawns := stubSpawn(t)
	// Not a prompt event: silent default output.
	code, stdout, stderr := run(t, stopPayload("Hi."), "hook", "prompt")
	if code != ExitOK || stdout != "{}\n" || stderr != "" {
		t.Errorf("non-prompt: code=%d out=%q stderr=%q", code, stdout, stderr)
	}
	// Broken JSON: default output with a diagnostic.
	code, stdout, stderr = run(t, "{broken", "hook", "prompt")
	if code != ExitOK || stdout != "{}\n" || stderr == "" {
		t.Errorf("broken JSON: code=%d out=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "cannot parse hook input") {
		t.Errorf("broken JSON stderr = %q", stderr)
	}
	// Unknown forced host: default output.
	code, stdout, _ = run(t, promptPayload(`"session_id":"s1"`), "hook", "prompt", "--host=bogus")
	if code != ExitOK || stdout != "{}\n" {
		t.Errorf("unknown host: code=%d out=%q", code, stdout)
	}
	// Disabled: silent default output.
	_, _, _ = run(t, "", "disable")
	code, stdout, stderr = run(t, promptPayload(`"session_id":"s1"`), "hook", "prompt")
	if code != ExitOK || stdout != "{}\n" || stderr != "" {
		t.Errorf("disabled: code=%d out=%q stderr=%q", code, stdout, stderr)
	}
	if *spawns != 0 {
		t.Errorf("variants spawned %d workers, want 0", *spawns)
	}
}

func TestHookStopQueuesSummary(t *testing.T) {
	isolateHome(t)
	spawns := stubSpawn(t)
	message := "Did the thing.\n\n<!-- turnecho-summary:v1\nFixed the bug and added a test.\n-->\n"
	code, stdout, stderr := run(t, stopPayload(message), "hook", "stop")
	if code != ExitOK || stdout != "{}\n" || stderr != "" {
		t.Fatalf("code=%d out=%q stderr=%q", code, stdout, stderr)
	}
	if *spawns != 1 {
		t.Errorf("spawned %d workers, want 1", *spawns)
	}
	job := claimOnly(t)
	if job == nil {
		t.Fatal("no job queued")
	}
	if job.Host != hosts.Codex || job.SessionID != "s1" || job.TurnID != "t1" {
		t.Errorf("unexpected job identity: %+v", job)
	}
	if job.Message != "Fixed the bug and added a test." {
		t.Errorf("unexpected job message: %q", job.Message)
	}
	if job.Voice != config.DefaultVoice || job.Speed != config.DefaultSpeed {
		t.Errorf("snapshot voice=%q speed=%v", job.Voice, job.Speed)
	}
}

func TestHookStopVariantsQueueNothing(t *testing.T) {
	isolateHome(t)
	spawns := stubSpawn(t)
	cases := map[string]string{
		"no marker":     stopPayload("Just a plain reply."),
		"broken json":   "{broken",
		"not an object": `[1,2]`,
		"active hook":   `{"hook_event_name":"Stop","session_id":"s1","turn_id":"t1","last_assistant_message":"Hi.","stop_hook_active":true}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			code, stdout, _ := run(t, payload, "hook", "stop")
			if code != ExitOK || stdout != "{}\n" {
				t.Errorf("code=%d out=%q", code, stdout)
			}
		})
	}
	// Unknown forced host with an otherwise valid payload.
	message := "Done.\n\n<!-- turnecho-summary:v1\nAll good.\n-->\n"
	code, stdout, _ := run(t, stopPayload(message), "hook", "stop", "--host=bogus")
	if code != ExitOK || stdout != "{}\n" {
		t.Errorf("unknown host: code=%d out=%q", code, stdout)
	}
	if job := claimOnly(t); job != nil {
		t.Errorf("variant queued %+v", job)
	}
	if *spawns != 0 {
		t.Errorf("variants spawned %d workers, want 0", *spawns)
	}
	_, _, stderr := run(t, "{broken", "hook", "stop")
	if !strings.Contains(stderr, "cannot parse hook input") {
		t.Errorf("broken JSON stderr = %q", stderr)
	}
}

func TestHookStopClaudeDerivesTurn(t *testing.T) {
	home := isolateHome(t)
	stubSpawn(t)
	transcript := filepath.Join(home, "transcript.jsonl")
	lines := "{\"type\":\"user\",\"message\":\"hi\"}\n{\"type\":\"assistant\",\"message\":\"one\"}\n"
	if err := os.WriteFile(transcript, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	message := "Done.\n\n<!-- turnecho-summary:v1\nAll good.\n-->\n"
	escaped, _ := json.Marshal(message)
	payload := `{"hook_event_name":"Stop","session_id":"s9","transcript_path":"` + transcript +
		`","last_assistant_message":` + string(escaped) + `}`
	code, stdout, stderr := run(t, payload, "hook", "stop")
	if code != ExitOK || stdout != "{}\n" || stderr != "" {
		t.Fatalf("code=%d out=%q stderr=%q", code, stdout, stderr)
	}
	job := claimOnly(t)
	if job == nil {
		t.Fatal("no job queued")
	}
	if job.Host != hosts.ClaudeCode || job.SessionID != "s9" {
		t.Errorf("unexpected job: %+v", job)
	}
	if !strings.HasPrefix(job.TurnID, "turn-1-") {
		t.Errorf("turn id = %q, want one assistant turn", job.TurnID)
	}
}

func TestHookStopDisabled(t *testing.T) {
	isolateHome(t)
	spawns := stubSpawn(t)
	_, _, _ = run(t, "", "disable")
	message := "Done.\n\n<!-- turnecho-summary:v1\nAll good.\n-->\n"
	code, stdout, stderr := run(t, stopPayload(message), "hook", "stop")
	if code != ExitOK || stdout != "{}\n" || stderr != "" {
		t.Errorf("code=%d out=%q stderr=%q", code, stdout, stderr)
	}
	if job := claimOnly(t); job != nil {
		t.Errorf("disabled hook queued %+v", job)
	}
	if *spawns != 0 {
		t.Errorf("disabled hook spawned %d workers", *spawns)
	}
}

func TestWorkerCommandEmptyQueue(t *testing.T) {
	isolateHome(t)
	code, stdout, stderr := run(t, "", "worker")
	if code != ExitOK || stdout != "" || stderr != "" {
		t.Errorf("code=%d out=%q stderr=%q", code, stdout, stderr)
	}
}

func TestWorkerCommandHeldLock(t *testing.T) {
	isolateHome(t)
	release, err := worker.HoldLock()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	code, stdout, stderr := run(t, "", "worker")
	if code != ExitOK || stdout != "" || stderr != "" {
		t.Errorf("code=%d out=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRootHelps(t *testing.T) {
	isolateHome(t)
	code, stdout, _ := run(t, "", "--help")
	if code != ExitOK || !strings.Contains(stdout, "turnecho") {
		t.Errorf("help: code=%d out=%q", code, stdout)
	}
	if strings.Contains(stdout, "hook") || strings.Contains(stdout, "worker") {
		t.Errorf("help leaks hidden commands: %q", stdout)
	}
	code, stdout, _ = run(t, "")
	if code != ExitOK || !strings.Contains(stdout, "turnecho") {
		t.Errorf("bare root: code=%d out=%q", code, stdout)
	}
	code, _, stderr := run(t, "", "bogus")
	if code != ExitConfig {
		t.Errorf("unknown command code=%d, want %d (stderr=%q)", code, ExitConfig, stderr)
	}
	code, _, stderr = run(t, "", "say", "--bogus")
	if code != ExitConfig {
		t.Errorf("unknown flag code=%d, want %d", code, ExitConfig)
	}
	if !strings.Contains(stderr, "unknown flag") {
		t.Errorf("unknown flag stderr = %q", stderr)
	}
	code, stdout, _ = run(t, "", "--version")
	if code != ExitOK || stdout != "turnecho version "+version.Version+"\n" {
		t.Errorf("version: code=%d out=%q", code, stdout)
	}
}

func TestSpeechCommandsRespectModelOwner(t *testing.T) {
	home := isolateHome(t)
	installFakePlayer(t)
	release, err := worker.HoldLock()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	previous := openBackend
	openBackend = func(string) (tts.Engine, error) { t.Fatal("second model opened"); return nil, nil }
	defer func() { openBackend = previous }()
	for _, args := range [][]string{{"test"}, {"doctor"}, {"say", "Hello"}, {"say", "Hello", "--output", filepath.Join(home, "out.wav")}} {
		code, _, stderr := run(t, "", args...)
		if code != ExitFailed || !strings.Contains(stderr, "already running") {
			t.Fatalf("%v: code=%d error=%s", args, code, stderr)
		}
	}
}
