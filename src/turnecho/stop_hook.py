import json
import sys

from .config import ConfigError, load_config
from .hosts import codex
from .sqlite import insert_job_db
from .summary import extract_turnecho_summary_from_agent_message
from .worker import spawn_background_worker

__all__ = [
    "extract_turnecho_summary_from_agent_message",
    "handle_stop_hook",
    "main",
]


def handle_stop_hook(raw_input: object) -> None:
    event = codex.parse_stop_payload(raw_input)
    if event is None:
        return print(codex.CODEX_DEFAULT_OUTPUT_MESSAGE)

    try:
        config = load_config()
    except ConfigError as error:
        print(error, file=sys.stderr)
        return print(codex.CODEX_DEFAULT_OUTPUT_MESSAGE)

    if not config.enabled:
        return print(codex.CODEX_DEFAULT_OUTPUT_MESSAGE)

    turnecho_message = extract_turnecho_summary_from_agent_message(event.message)
    if not isinstance(turnecho_message, str) or turnecho_message.strip() == "":
        # Ignore non-readable TurnEcho messages without changing the agent response.
        return print(codex.CODEX_DEFAULT_OUTPUT_MESSAGE)

    # Save task into db
    try:
        insert_job_db(
            host=event.host,
            session_id=event.session_id,
            turn_id=event.turn_id,
            message=turnecho_message,
            voice=config.voice,
            speed=config.speed,
        )
    except Exception as e:
        print(e, file=sys.stderr)
        return print(codex.CODEX_DEFAULT_OUTPUT_MESSAGE)

    # Best effort spawn background worker. Initially spawned during user submit hook.
    try:
        spawn_background_worker()
    except Exception as e:
        print(e, file=sys.stderr)

    print(codex.CODEX_DEFAULT_OUTPUT_MESSAGE)


def main() -> None:
    try:
        stdin_object: object = json.load(sys.stdin)
    except Exception as e:
        print(e, file=sys.stderr)
        return print(codex.CODEX_DEFAULT_OUTPUT_MESSAGE)

    if (
        isinstance(stdin_object, dict)
        and stdin_object.get("hook_event_name") == codex.CODEX_HOOK_STOP_EVENT_NAME
    ):
        return handle_stop_hook(stdin_object)

    print(codex.CODEX_DEFAULT_OUTPUT_MESSAGE)


if __name__ == "__main__":
    main()
