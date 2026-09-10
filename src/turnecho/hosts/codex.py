"""Codex hook payload parsing and output rendering.

This module owns every Codex-specific wire shape: event names, stdin payload
fields, and stdout envelopes. Core queueing never sees these details; it only
receives the normalized TurnEchoEvent returned here.
"""

from __future__ import annotations

import json

from .types import TurnEchoEvent, TurnEchoHostSource

CODEX_HOOK_STOP_EVENT_NAME = "Stop"
CODEX_HOOK_USER_PROMPT_SUBMIT_NAME = "UserPromptSubmit"
CODEX_DEFAULT_OUTPUT_MESSAGE = "{}"


def parse_stop_payload(raw_input: object) -> TurnEchoEvent | None:
    """Normalize a Codex Stop payload, returning None when it is unusable."""
    if not isinstance(raw_input, dict):
        return None
    if raw_input.get("hook_event_name") != CODEX_HOOK_STOP_EVENT_NAME:
        return None
    session_id = raw_input.get("session_id")
    if not isinstance(session_id, str) or session_id.strip() == "":
        return None
    turn_id = raw_input.get("turn_id")
    if not isinstance(turn_id, str) or turn_id.strip() == "":
        return None
    last_assistant_message = raw_input.get("last_assistant_message")
    if (
        not isinstance(last_assistant_message, str)
        or last_assistant_message.strip() == ""
    ):
        return None
    if raw_input.get("stop_hook_active", False):
        return None
    return TurnEchoEvent(
        host=TurnEchoHostSource.CODEX.value,
        session_id=session_id,
        turn_id=turn_id,
        message=last_assistant_message,
    )


def is_user_prompt_submit_payload(raw_input: object) -> bool:
    """Return whether the payload is a Codex UserPromptSubmit event."""
    return (
        isinstance(raw_input, dict)
        and raw_input.get("hook_event_name") == CODEX_HOOK_USER_PROMPT_SUBMIT_NAME
    )


def render_prompt_submit_output(instruction: str) -> str:
    """Render the Codex UserPromptSubmit additional-context envelope."""
    return json.dumps(
        {
            "hookSpecificOutput": {
                "hookEventName": CODEX_HOOK_USER_PROMPT_SUBMIT_NAME,
                "additionalContext": instruction,
            }
        }
    )
