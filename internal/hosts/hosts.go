// Package hosts parses per-host hook payloads into normalized events.
package hosts

import (
	"bytes"
	"encoding/json"
	"strings"
)

// Codex identifies Codex hook payloads.
const Codex = "codex"

// ClaudeCode identifies Claude Code hook payloads.
const ClaudeCode = "claude_code"

// DefaultOutput is the fail-safe empty hook output both hosts accept.
const DefaultOutput = "{}"

// Event is one normalized hook event ready for validation and queueing.
type Event struct {
	Host      string
	SessionID string
	TurnID    string
	Message   string
}

// Detect returns the hook source for a payload.
//
// An explicit override always wins, even when it names no known host (the
// caller then fails safe to empty output). Otherwise Claude Code always
// sends transcript_path while Codex sends turn_id instead, so the payload
// keys decide. Anything unrecognized falls through to Codex parsing, which
// rejects it safely.
func Detect(payload map[string]any, forcedHost string, hasForced bool) string {
	if hasForced {
		return forcedHost
	}
	if payload != nil {
		if _, ok := payload["transcript_path"]; ok {
			return ClaudeCode
		}
	}
	return Codex
}

// PromptEnvelope renders the additional-context envelope both hosts accept.
func PromptEnvelope(eventName, instruction string) string {
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
		}{HookEventName: eventName, AdditionalContext: instruction},
	})
	return strings.TrimSuffix(buffer.String(), "\n")
}

func nonBlankString(payload map[string]any, key string) (string, bool) {
	value, ok := payload[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", false
	}
	return value, true
}

// isActive reports whether stop_hook_active is set, mirroring v1 truthiness.
func isActive(payload map[string]any) bool {
	value, ok := payload["stop_hook_active"]
	if !ok || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return typed != ""
	case float64:
		return typed != 0
	case []any:
		return len(typed) != 0
	case map[string]any:
		return len(typed) != 0
	default:
		return true
	}
}
