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

func countAssistantTurns(transcriptPath string) (int, bool) {
	tail, ok := readTranscriptTail(transcriptPath)
	if !ok {
		return 0, false
	}
	count := 0
	for line := range strings.Lines(tail) {
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
	return count, true
}

func expandUser(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + path[1:]
		}
	}
	return path
}

func readTranscriptTail(transcriptPath string) (string, bool) {
	if strings.TrimSpace(transcriptPath) == "" {
		return "", false
	}
	handle, err := os.Open(expandUser(transcriptPath))
	if err != nil {
		return "", false
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil {
		return "", false
	}
	offset := max(info.Size()-TranscriptTailBytes, 0)
	tail := make([]byte, info.Size()-offset)
	if _, err := handle.ReadAt(tail, offset); err != nil {
		return "", false
	}
	return string(tail), true
}

func transcriptSize(transcriptPath string) (int64, bool) {
	if strings.TrimSpace(transcriptPath) == "" {
		return 0, false
	}
	info, err := os.Stat(expandUser(transcriptPath))
	if err != nil {
		return 0, false
	}
	return info.Size(), true
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
	count, countOK := countAssistantTurns(transcriptPath)
	size, sizeOK := transcriptSize(transcriptPath)
	if !countOK || !sizeOK {
		return "turn-" + randomSuffix() + "-" + digest
	}
	return "turn-" + strconv.Itoa(count) + "-" + strconv.FormatInt(size, 10) + "-" + digest
}

// ParseClaudeStop normalizes a Claude Code Stop payload, returning false when unusable.
func ParseClaudeStop(payload map[string]any) (Event, bool) {
	if payload == nil {
		return Event{}, false
	}
	if payload["hook_event_name"] != ClaudeStopEventName {
		return Event{}, false
	}
	sessionID, ok := nonBlankString(payload, "session_id")
	if !ok {
		return Event{}, false
	}
	message, ok := nonBlankString(payload, "last_assistant_message")
	if !ok {
		return Event{}, false
	}
	if isActive(payload) {
		return Event{}, false
	}
	transcriptPath, _ := payload["transcript_path"].(string)
	return Event{
		Host:      ClaudeCode,
		SessionID: sessionID,
		TurnID:    DeriveTurnID(transcriptPath, message),
		Message:   message,
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
