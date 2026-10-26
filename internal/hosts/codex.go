package hosts

// StopEventName is the Codex Stop event.
const StopEventName = "Stop"

// PromptSubmitEventName is the Codex UserPromptSubmit event.
const PromptSubmitEventName = "UserPromptSubmit"

// ParseCodexStop normalizes a Codex Stop payload, returning false when unusable.
func ParseCodexStop(payload map[string]any) (Event, bool) {
	fields, ok := parseStopFields(payload, StopEventName)
	if !ok {
		return Event{}, false
	}
	turnID, ok := nonBlankString(payload, "turn_id")
	if !ok {
		return Event{}, false
	}
	return Event{Host: Codex, SessionID: fields.sessionID, TurnID: turnID, Message: fields.message}, true
}

// IsCodexPromptSubmit reports whether the payload is a Codex UserPromptSubmit event.
func IsCodexPromptSubmit(payload map[string]any) bool {
	return payload != nil && payload["hook_event_name"] == PromptSubmitEventName
}

// RenderCodexPromptSubmit renders the Codex UserPromptSubmit envelope.
func RenderCodexPromptSubmit(instruction string) string {
	return PromptEnvelope(PromptSubmitEventName, instruction)
}
