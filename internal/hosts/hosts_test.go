package hosts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestDetect(t *testing.T) {
	claude := map[string]any{"transcript_path": "/tmp/x"}
	codex := map[string]any{"turn_id": "t1"}
	cases := []struct {
		name      string
		payload   map[string]any
		forced    string
		hasForced bool
		want      string
	}{
		{"forced wins", codex, "claude_code", true, "claude_code"},
		{"unknown forced wins", codex, "bogus", true, "bogus"},
		{"transcript means claude", claude, "", false, ClaudeCode},
		{"anything else is codex", codex, "", false, Codex},
		{"nil is codex", nil, "", false, Codex},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Detect(tc.payload, tc.forced, tc.hasForced); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseCodexStop(t *testing.T) {
	valid := map[string]any{
		"hook_event_name":        "Stop",
		"session_id":             "s1",
		"turn_id":                "t1",
		"last_assistant_message": "Hello.",
	}
	event, ok := ParseCodexStop(valid)
	if !ok {
		t.Fatal("valid payload rejected")
	}
	if event.Host != Codex || event.SessionID != "s1" || event.TurnID != "t1" || event.Message != "Hello." {
		t.Errorf("unexpected event: %+v", event)
	}

	mutations := []struct {
		name  string
		apply func(map[string]any)
	}{
		{"wrong event", func(p map[string]any) { p["hook_event_name"] = "UserPromptSubmit" }},
		{"blank session", func(p map[string]any) { p["session_id"] = "  " }},
		{"missing turn", func(p map[string]any) { delete(p, "turn_id") }},
		{"blank message", func(p map[string]any) { p["last_assistant_message"] = "" }},
		{"active hook", func(p map[string]any) { p["stop_hook_active"] = true }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{}
			for key, value := range valid {
				payload[key] = value
			}
			tc.apply(payload)
			if _, ok := ParseCodexStop(payload); ok {
				t.Error("invalid payload accepted")
			}
		})
	}
	if _, ok := ParseCodexStop(nil); ok {
		t.Error("nil payload accepted")
	}
}

func TestIsCodexPromptSubmit(t *testing.T) {
	if !IsCodexPromptSubmit(map[string]any{"hook_event_name": "UserPromptSubmit"}) {
		t.Error("prompt payload rejected")
	}
	if IsCodexPromptSubmit(map[string]any{"hook_event_name": "Stop"}) {
		t.Error("stop payload accepted")
	}
	if IsCodexPromptSubmit(nil) {
		t.Error("nil payload accepted")
	}
}

func TestIsClaudePromptSubmit(t *testing.T) {
	prompt := map[string]any{"hook_event_name": "UserPromptSubmit", "transcript_path": "/tmp/x"}
	if !IsClaudePromptSubmit(prompt) {
		t.Error("prompt payload rejected")
	}
	if IsClaudePromptSubmit(map[string]any{"hook_event_name": "Stop"}) {
		t.Error("stop payload accepted")
	}
	if IsClaudePromptSubmit(nil) {
		t.Error("nil payload accepted")
	}
}

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDeriveTurnIDStable(t *testing.T) {
	transcript := writeTranscript(t,
		`{"type":"user","message":"hi"}`,
		`{"type":"assistant","message":"one"}`,
		`not json`,
		`{"type":"assistant","message":"two"}`,
		`{"type":"tool","message":"x"}`,
	)
	first := DeriveTurnID(transcript, "Hello.")
	second := DeriveTurnID(transcript, "Hello.")
	if first != second {
		t.Fatalf("unstable turn id: %q vs %q", first, second)
	}
	info, _ := os.Stat(transcript)
	matched, _ := regexp.MatchString(`^turn-2-`+strconv.FormatInt(info.Size(), 10)+`-[0-9a-f]{12}$`, first)
	if !matched {
		t.Errorf("unexpected turn id shape: %q", first)
	}
	if DeriveTurnID(transcript, "Other.") == first {
		t.Error("distinct messages share a turn id")
	}
}

func TestDeriveTurnIDUnreadableTranscript(t *testing.T) {
	id := DeriveTurnID(filepath.Join(t.TempDir(), "missing.jsonl"), "Hello.")
	matched, _ := regexp.MatchString(`^turn-[0-9a-f]{8}-[0-9a-f]{12}$`, id)
	if !matched {
		t.Errorf("unexpected fallback turn id: %q", id)
	}
	if other := DeriveTurnID("", "Hello."); other == id {
		t.Error("fallback turn ids collide")
	}
}

func TestParseClaudeStop(t *testing.T) {
	transcript := writeTranscript(t, `{"type":"assistant","message":"one"}`)
	payload := map[string]any{
		"hook_event_name":        "Stop",
		"session_id":             "s1",
		"transcript_path":        transcript,
		"last_assistant_message": "Hello.",
	}
	event, ok := ParseClaudeStop(payload)
	if !ok {
		t.Fatal("valid payload rejected")
	}
	if event.Host != ClaudeCode || event.SessionID != "s1" || event.Message != "Hello." {
		t.Errorf("unexpected event: %+v", event)
	}
	if !strings.HasPrefix(event.TurnID, "turn-1-") {
		t.Errorf("unexpected turn id: %q", event.TurnID)
	}

	payload["stop_hook_active"] = true
	if _, ok := ParseClaudeStop(payload); ok {
		t.Error("active hook accepted")
	}
}

func TestPromptEnvelope(t *testing.T) {
	out := PromptEnvelope("Stop", "Say <hi> & \"bye\".")
	var decoded struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("envelope is not JSON: %v", err)
	}
	if decoded.HookSpecificOutput.HookEventName != "Stop" {
		t.Errorf("wrong event name: %q", decoded.HookSpecificOutput.HookEventName)
	}
	if decoded.HookSpecificOutput.AdditionalContext != "Say <hi> & \"bye\"." {
		t.Errorf("instruction mangled: %q", decoded.HookSpecificOutput.AdditionalContext)
	}
	if !strings.Contains(out, "<hi>") {
		t.Error("envelope escapes HTML; hosts expect plain text")
	}
}
