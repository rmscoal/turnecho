package hosts

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// ClaudeStopEventName is the Claude Code Stop event.
const ClaudeStopEventName = "Stop"

// ClaudePromptSubmitEventName is the Claude Code UserPromptSubmit event.
const ClaudePromptSubmitEventName = "UserPromptSubmit"

// TranscriptTailBytes bounds the transcript read so Stop handling stays fast.
const TranscriptTailBytes = 65536

// transcriptStats counts assistant turns in the tail window and reports the
// total file size, opening the transcript once so Stop handling stays fast.
func transcriptStats(transcriptPath string) (count int, size int64, ok bool) {
	if strings.TrimSpace(transcriptPath) == "" {
		return 0, 0, false
	}
	handle, err := os.Open(expandUser(transcriptPath))
	if err != nil {
		return 0, 0, false
	}
	defer handle.Close()

	info, err := handle.Stat()
	if err != nil {
		return 0, 0, false
	}
	offset := max(info.Size()-TranscriptTailBytes, 0)
	tail := make([]byte, info.Size()-offset)
	if _, err := handle.ReadAt(tail, offset); err != nil {
		return 0, 0, false
	}

	for line := range strings.Lines(string(tail)) {
		if !strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "{") {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry["type"] == "assistant" {
			count++
		}
	}
	return count, info.Size(), true
}

func expandUser(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}

func randomSuffix() string {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(suffix[:])
}

// DeriveTurnID builds a stable turn id without a Codex-style turn field.
//
// The assistant-turn count keeps turns ordered, the transcript size keeps
// repeated messages distinct once the tail window starts sliding, and the
// message hash keeps distinct summaries distinct when the transcript lags
// behind the Stop event. An unreadable transcript falls back to a unique
// id so the turn is still spoken instead of colliding with another turn.
func DeriveTurnID(transcriptPath, message string) string {
	hash := sha1.Sum([]byte(message))
	digest := hex.EncodeToString(hash[:])[:12]
	count, size, ok := transcriptStats(transcriptPath)
	if !ok {
		return "turn-" + randomSuffix() + "-" + digest
	}
	return "turn-" + strconv.Itoa(count) + "-" + strconv.FormatInt(size, 10) + "-" + digest
}

// ParseClaudeStop normalizes a Claude Code Stop payload, returning false when unusable.
func ParseClaudeStop(payload map[string]any) (Event, bool) {
	fields, ok := parseStopFields(payload, ClaudeStopEventName)
	if !ok {
		return Event{}, false
	}
	transcriptPath, _ := payload["transcript_path"].(string)
	return Event{
		Host:      ClaudeCode,
		SessionID: fields.sessionID,
		TurnID:    DeriveTurnID(transcriptPath, fields.message),
		Message:   fields.message,
	}, true
}

// IsClaudePromptSubmit reports whether the payload is a Claude UserPromptSubmit event.
func IsClaudePromptSubmit(payload map[string]any) bool {
	return payload != nil && payload["hook_event_name"] == ClaudePromptSubmitEventName
}

// RenderClaudePromptSubmit renders the Claude Code UserPromptSubmit envelope.
func RenderClaudePromptSubmit(instruction string) string {
	return PromptEnvelope(ClaudePromptSubmitEventName, instruction)
}
