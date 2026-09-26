package hosts

// StopEventName is the Codex Stop event.
const StopEventName = "Stop"

// PromptSubmitEventName is the Codex UserPromptSubmit event.
const PromptSubmitEventName = "UserPromptSubmit"

// ParseCodexStop normalizes a Codex Stop payload, returning false when unusable.
func ParseCodexStop(payload map[string]any) (Event, bool) {
	if payload == nil {
		return Event{}, false
	}
	if payload["hook_event_name"] != StopEventName {
		return Event{}, false
	}
	sessionID, ok := nonBlankString(payload, "session_id")
	if !ok {
		return Event{}, false
	}
	turnID, ok := nonBlankString(payload, "turn_id")
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
	return Event{Host: Codex, SessionID: sessionID, TurnID: turnID, Message: message}, true
}

// IsCodexPromptSubmit reports whether the payload is a Codex UserPromptSubmit event.
func IsCodexPromptSubmit(payload map[string]any) bool {
	return payload != nil && payload["hook_event_name"] == PromptSubmitEventName
}

// RenderCodexPromptSubmit renders the Codex UserPromptSubmit envelope.
func RenderCodexPromptSubmit(instruction string) string {
	return PromptEnvelope(PromptSubmitEventName, instruction)
}
