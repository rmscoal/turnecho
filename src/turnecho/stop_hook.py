import json
import sys

from .config import ConfigError, load_config
from .hosts import claude, codex, default_output, detect_host, forced_host
from .hosts.types import TurnEchoHostSource
from .sqlite import insert_job_db
from .summary import extract_turnecho_summary_from_agent_message
from .worker import spawn_background_worker

__all__ = [
    "extract_turnecho_summary_from_agent_message",
    "handle_stop_hook",
    "main",
]


def handle_stop_hook(raw_input: object, host: str | None = None) -> None:
    resolved_host = host or detect_host(raw_input, forced_host(sys.argv[1:]))
    if resolved_host == TurnEchoHostSource.CLAUDE_CODE.value:
        event = claude.parse_stop_payload(raw_input)
    elif resolved_host == TurnEchoHostSource.CODEX.value:
        event = codex.parse_stop_payload(raw_input)
    else:
        event = None
    if event is None:
        return print(default_output(resolved_host))

    try:
        config = load_config()
    except ConfigError as error:
        print(error, file=sys.stderr)
        return print(default_output(resolved_host))

    if not config.enabled:
        return print(default_output(resolved_host))

    turnecho_message = extract_turnecho_summary_from_agent_message(event.message)
    if not isinstance(turnecho_message, str) or turnecho_message.strip() == "":
        # Ignore non-readable TurnEcho messages without changing the agent response.
        return print(default_output(resolved_host))

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
        return print(default_output(resolved_host))

    # Best effort spawn background worker. Initially spawned during user submit hook.
    try:
        spawn_background_worker()
    except Exception as e:
        print(e, file=sys.stderr)

    print(default_output(resolved_host))


def main() -> None:
    try:
        stdin_object: object = json.load(sys.stdin)
    except Exception as e:
        print(e, file=sys.stderr)
        return print(default_output(detect_host(None, forced_host(sys.argv[1:]))))

    return handle_stop_hook(stdin_object)


if __name__ == "__main__":
    main()
