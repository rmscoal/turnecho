import json
import sys

from .config import ConfigError, load_config
from .constant import (
    CODEX_DEFAULT_OUTPUT_MESSAGE,
    CODEX_HOOK_STOP_EVENT_NAME,
    TURNECHO_SUMMARY_CLOSE_MARKER,
    TURNECHO_SUMMARY_MAX_CHARS,
    TURNECHO_SUMMARY_OPEN_MARKER,
)
from .sqlite import insert_job_db
from .worker import spawn_background_worker


def extract_turnecho_summary_from_agent_message(agent_message: str) -> str | None:
    normalized_message = agent_message.replace("\r\n", "\n").rstrip()
    if not normalized_message.endswith(TURNECHO_SUMMARY_CLOSE_MARKER):
        return None

    marker_start = normalized_message.rfind(TURNECHO_SUMMARY_OPEN_MARKER)
    if marker_start == -1:
        return None

    summary_start = marker_start + len(TURNECHO_SUMMARY_OPEN_MARKER)
    summary_end = len(normalized_message) - len(TURNECHO_SUMMARY_CLOSE_MARKER)
    raw_summary = normalized_message[summary_start:summary_end]

    if "<!--" in raw_summary or "-->" in raw_summary:
        return None

    summary = " ".join(raw_summary.split())
    if not summary:
        return None

    if len(summary) > TURNECHO_SUMMARY_MAX_CHARS:
        return None

    return summary


def handle_stop_hook(raw_input: object) -> None:
    if not isinstance(raw_input, dict):
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)

    # Validation
    if raw_input.get("hook_event_name") != CODEX_HOOK_STOP_EVENT_NAME:
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)
    session_id = raw_input.get("session_id")
    if not isinstance(session_id, str) or session_id.strip() == "":
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)
    turn_id = raw_input.get("turn_id")
    if not isinstance(turn_id, str) or turn_id.strip() == "":
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)
    last_assistant_message = raw_input.get("last_assistant_message")
    if (
        not isinstance(last_assistant_message, str)
        or last_assistant_message.strip() == ""
    ):
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)
    if raw_input.get("stop_hook_active", False):
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)

    try:
        config = load_config()
    except ConfigError as error:
        print(error, file=sys.stderr)
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)

    if not config.enabled:
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)

    turnecho_message = extract_turnecho_summary_from_agent_message(
        last_assistant_message
    )
    if not isinstance(turnecho_message, str) or turnecho_message.strip() == "":
        # Ignore non-readable TurnEcho messages without changing the agent response.
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)

    # Save task into db
    try:
        insert_job_db(
            host="codex",
            session_id=session_id,
            turn_id=turn_id,
            message=turnecho_message,
            voice=config.voice,
            speed=config.speed,
        )
    except Exception as e:
        print(e, file=sys.stderr)
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)

    # Best effort spawn background worker. Initially spawned during user submit hook.
    try:
        spawn_background_worker()
    except Exception as e:
        print(e, file=sys.stderr)

    print(CODEX_DEFAULT_OUTPUT_MESSAGE)


def main() -> None:
    try:
        stdin_object: object = json.load(sys.stdin)
    except Exception as e:
        print(e, file=sys.stderr)
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)

    if (
        isinstance(stdin_object, dict)
        and stdin_object.get("hook_event_name") == CODEX_HOOK_STOP_EVENT_NAME
    ):
        return handle_stop_hook(stdin_object)

    print(CODEX_DEFAULT_OUTPUT_MESSAGE)


if __name__ == "__main__":
    main()
