"""Claude Code hook payload parsing and output rendering.

This module owns every Claude Code-specific wire shape: event names, stdin
payload fields, transcript-based turn identity, and stdout envelopes. Core
queueing never sees these details; it only receives the normalized
TurnEchoEvent returned here.
"""

import hashlib
import json
from pathlib import Path
from uuid import uuid4

from .types import (
    TurnEchoEvent,
    TurnEchoHostSource,
    prompt_submit_envelope,
)

CLAUDE_HOOK_STOP_EVENT_NAME = "Stop"
CLAUDE_HOOK_USER_PROMPT_SUBMIT_NAME = "UserPromptSubmit"
CLAUDE_DEFAULT_OUTPUT_MESSAGE = "{}"

# Bound the transcript tail read so Stop handling stays fast on long sessions.
CLAUDE_TRANSCRIPT_TAIL_BYTES = 65536


def _count_assistant_turns(transcript_path: object) -> int | None:
    """Count assistant entries in the transcript tail, or None when unreadable."""
    if not isinstance(transcript_path, str) or not transcript_path.strip():
        return None
    try:
        with Path(transcript_path).expanduser().open("rb") as handle:
            handle.seek(0, 2)
            handle.seek(max(0, handle.tell() - CLAUDE_TRANSCRIPT_TAIL_BYTES))
            tail = handle.read().decode("utf-8", errors="replace")
    except OSError:
        return None

    count = 0
    for line in tail.splitlines():
        if not line.lstrip().startswith("{"):
            continue
        try:
            entry = json.loads(line)
        except ValueError:
            continue
        if isinstance(entry, dict) and entry.get("type") == "assistant":
            count += 1
    return count


def derive_turn_id(transcript_path: object, message: str) -> str:
    """Build a stable turn id without a Codex-style turn field.

    The assistant-turn count keeps turns ordered while the message hash keeps
    distinct summaries distinct when the transcript lags behind the Stop
    event. An unreadable transcript falls back to a unique id so the turn is
    still spoken instead of colliding with another turn.
    """
    digest = hashlib.sha1(message.encode("utf-8")).hexdigest()[:12]
    count = _count_assistant_turns(transcript_path)
    if count is None:
        return f"turn-{uuid4().hex[:8]}-{digest}"
    return f"turn-{count}-{digest}"


def parse_stop_payload(raw_input: object) -> TurnEchoEvent | None:
    """Normalize a Claude Code Stop payload, returning None when unusable."""
    if not isinstance(raw_input, dict):
        return None
    if raw_input.get("hook_event_name") != CLAUDE_HOOK_STOP_EVENT_NAME:
        return None
    session_id = raw_input.get("session_id")
    if not isinstance(session_id, str) or session_id.strip() == "":
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
        host=TurnEchoHostSource.CLAUDE_CODE.value,
        session_id=session_id,
        turn_id=derive_turn_id(
            raw_input.get("transcript_path"), last_assistant_message
        ),
        message=last_assistant_message,
    )


def is_user_prompt_submit_payload(raw_input: object) -> bool:
    """Return whether the payload is a Claude Code UserPromptSubmit event."""
    return (
        isinstance(raw_input, dict)
        and raw_input.get("hook_event_name") == CLAUDE_HOOK_USER_PROMPT_SUBMIT_NAME
    )


def render_prompt_submit_output(instruction: str) -> str:
    """Render the Claude Code UserPromptSubmit additional-context envelope."""
    return prompt_submit_envelope(CLAUDE_HOOK_USER_PROMPT_SUBMIT_NAME, instruction)
