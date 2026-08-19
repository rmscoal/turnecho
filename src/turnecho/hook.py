import json
import subprocess
import sys
from pathlib import Path

from .constant import (
    CODEX_DEFAULT_OUTPUT_MESSAGE,
    CODEX_HOOK_STOP_EVENT_NAME,
    TURNECHO_WORKER_LOG_FILE_PATH_MACOS_LINUX,
)
from .schema import CodexHookStopInputMessage
from .sqlite import insert_job_db


def spawn_background_worker():
    log_path = Path(TURNECHO_WORKER_LOG_FILE_PATH_MACOS_LINUX).expanduser()
    log_path.parent.mkdir(parents=True, exist_ok=True)

    with log_path.open("ab") as log_file:
        subprocess.Popen(
            [sys.executable, "-m", "turnecho.worker"],
            stdin=subprocess.DEVNULL,
            stdout=log_file,
            stderr=subprocess.STDOUT,
            start_new_session=True,
            close_fds=True,
        )


def main():
    input: CodexHookStopInputMessage

    # Input
    try:
        stdin_object: object = json.load(sys.stdin)
        input = CodexHookStopInputMessage.model_validate(stdin_object)
    except Exception as e:  # pyright: ignore
        print(e, file=sys.stderr)
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)

    # Validation
    if input.hook_event_name != CODEX_HOOK_STOP_EVENT_NAME:
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)
    if not isinstance(input.session_id, str) or input.session_id.strip() == "":
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)
    if not isinstance(input.turn_id, str) or input.turn_id.strip() == "":
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)
    if (
        not isinstance(input.last_assistant_message, str)
        or input.last_assistant_message.strip() == ""
    ):
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)
    if input.stop_hook_active:
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)

    # Save task into db
    try:
        insert_job_db(
            host="codex",
            session_id=input.session_id,
            turn_id=input.turn_id,
            message=input.last_assistant_message,
        )
    except Exception as e:
        print(e, file=sys.stderr)
        return print(CODEX_DEFAULT_OUTPUT_MESSAGE)

    try:
        spawn_background_worker()
    except Exception as e:
        print(e, file=sys.stderr)

    print(CODEX_DEFAULT_OUTPUT_MESSAGE)


if __name__ == "__main__":
    main()
