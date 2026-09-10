#!/usr/bin/env python3
"""Sandboxed end-to-end hook check with human-readable verdicts.

Runs the real hook entry points as subprocesses, the way Codex and Claude
Code invoke them, against a temporary HOME. Verifies stdout contracts and
queue rows for both hosts. A human reads the verdicts below.

Workers speak both summaries aloud so a human can verify by ear; set
E2E_QUIET=1 to skip playback (workers are then stopped before TTS runs).
A live host session remains the final acceptance step.
"""

from __future__ import annotations

import json
import os
import signal
import sqlite3
import subprocess
import sys
import tempfile
import time
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
SOURCE_PATH = str(REPO_ROOT / "src")
REAL_HOME = os.environ.get("HOME", "")
CLAUDE_MESSAGE = (
    "Hi from Claude Code. If you're hearing this, TurnEcho in Claude Code works."
)
CODEX_MESSAGE = "Hi from Codex. If you're hearing this, TurnEcho in Codex works."
TERMINAL_STATUSES = {"success", "failed"}
CHECKS: list[tuple[str, bool]] = []


def check(name: str, passed: bool, detail: str = "") -> None:
    """Record one human-readable verdict."""
    CHECKS.append((name, passed))
    print(
        f"[{'PASS' if passed else 'FAIL'}] {name}" + (f" — {detail}" if detail else "")
    )


def worker_pids() -> set[str]:
    try:
        completed = subprocess.run(
            ["pgrep", "-f", "turnecho.worker"],
            capture_output=True,
            text=True,
        )
    except OSError:
        return set()
    own_pid = str(os.getpid())
    return {pid for pid in completed.stdout.split() if pid.isdigit()} - {own_pid}


def stop_new_workers(before: set[str]) -> None:
    """Stop workers this script spawned, before TTS runs."""
    for pid in sorted(worker_pids() - before):
        try:
            os.kill(int(pid), signal.SIGTERM)
        except (OSError, ValueError):
            continue
    deadline = time.monotonic() + 5
    while worker_pids() - before and time.monotonic() < deadline:
        time.sleep(0.2)


def run_hook(module: str, payload: dict, home: str) -> subprocess.CompletedProcess[str]:
    environment = os.environ.copy()
    environment["HOME"] = home
    environment["PYTHONPATH"] = SOURCE_PATH
    # Reuse the user's model cache so workers speak instead of downloading.
    model_cache = Path(REAL_HOME) / ".cache" / "huggingface"
    if REAL_HOME and model_cache.is_dir():
        environment["HF_HOME"] = str(model_cache)
    return subprocess.run(
        [sys.executable, "-m", module],
        input=json.dumps(payload),
        capture_output=True,
        env=environment,
        text=True,
    )


def queue_rows(home: str) -> list[dict]:
    database = Path(home) / ".config" / "turnecho" / "turnecho.db"
    if not database.is_file():
        return []
    connection = sqlite3.connect(database)
    connection.row_factory = sqlite3.Row
    try:
        return [
            dict(row)
            for row in connection.execute(
                "SELECT host, session_id, turn_id, message, voice, speed,"
                " processing_status FROM turnecho_jobs ORDER BY rowid"
            )
        ]
    finally:
        connection.close()


def wait_for_terminal(home: str, timeout_seconds: int = 240) -> None:
    """Wait until every queued job succeeds or fails, then return."""
    deadline = time.monotonic() + timeout_seconds
    while time.monotonic() < deadline:
        rows = queue_rows(home)
        if len(rows) >= 2 and all(
            row.get("processing_status") in TERMINAL_STATUSES for row in rows
        ):
            return
        time.sleep(2)
    print("Timed out waiting for playback; reporting current states.")


def main() -> int:
    workers_before = worker_pids()
    with tempfile.TemporaryDirectory() as home:
        transcript = Path(home) / "transcript.jsonl"
        transcript.write_text(
            '{"type": "user", "message": "hi"}\n'
            '{"type": "assistant", "message": "reply"}\n',
            encoding="utf-8",
        )

        prompt_result = run_hook(
            "turnecho.prompt_hook",
            {
                "hook_event_name": "UserPromptSubmit",
                "session_id": "e2e-claude",
                "transcript_path": str(transcript),
                "prompt": "Do it.",
            },
            home,
        )
        try:
            prompt_output: object = json.loads(prompt_result.stdout)
        except json.JSONDecodeError:
            prompt_output = None
        check(
            "Claude prompt returns the summary instruction",
            isinstance(prompt_output, dict)
            and prompt_output.get("hookSpecificOutput", {}).get("hookEventName")
            == "UserPromptSubmit"
            and "turnecho-summary:v1"
            in prompt_output.get("hookSpecificOutput", {}).get("additionalContext", "")
            and prompt_result.stderr == "",
            f"stderr={prompt_result.stderr!r}",
        )

        claude_stop = run_hook(
            "turnecho.stop_hook",
            {
                "hook_event_name": "Stop",
                "session_id": "e2e-claude",
                "transcript_path": str(transcript),
                "stop_hook_active": False,
                "last_assistant_message": (
                    f"Done.\n\n<!-- turnecho-summary:v1\n{CLAUDE_MESSAGE}\n-->"
                ),
            },
            home,
        )
        check(
            "Claude stop stays silent",
            claude_stop.stdout == "{}\n" and claude_stop.stderr == "",
            f"stdout={claude_stop.stdout!r} stderr={claude_stop.stderr!r}",
        )

        codex_stop = run_hook(
            "turnecho.stop_hook",
            {
                "hook_event_name": "Stop",
                "session_id": "e2e-codex",
                "turn_id": "e2e-turn-1",
                "stop_hook_active": False,
                "last_assistant_message": (
                    f"Done.\n\n<!-- turnecho-summary:v1\n{CODEX_MESSAGE}\n-->"
                ),
            },
            home,
        )
        check(
            "Codex stop stays silent",
            codex_stop.stdout == "{}\n" and codex_stop.stderr == "",
            f"stdout={codex_stop.stdout!r} stderr={codex_stop.stderr!r}",
        )

        rows = {row["host"]: row for row in queue_rows(home)}
        claude_row = rows.get("claude_code", {})
        check(
            "Claude job queued with a synthesized turn",
            claude_row.get("session_id") == "e2e-claude"
            and str(claude_row.get("turn_id", "")).startswith("turn-1-")
            and claude_row.get("message") == CLAUDE_MESSAGE
            and claude_row.get("voice") == "Hugo"
            and claude_row.get("speed") == 1.0
            and claude_row.get("processing_status")
            in {"pending", "processing", "success"},
            f"row={claude_row!r}",
        )
        codex_row = rows.get("codex", {})
        check(
            "Codex job queued with its own turn",
            codex_row.get("session_id") == "e2e-codex"
            and codex_row.get("turn_id") == "e2e-turn-1"
            and codex_row.get("message") == CODEX_MESSAGE
            and codex_row.get("processing_status")
            in {"pending", "processing", "success"},
            f"row={codex_row!r}",
        )

        if os.environ.get("E2E_QUIET") == "1":
            stop_new_workers(workers_before)
        else:
            print("Listening check: both summaries will now play aloud.")
            wait_for_terminal(home)
            stop_new_workers(workers_before)
            spoken = {row["host"]: row for row in queue_rows(home)}
            for host, text in (
                ("claude_code", CLAUDE_MESSAGE),
                ("codex", CODEX_MESSAGE),
            ):
                status = spoken.get(host, {}).get("processing_status")
                check(
                    f"Human heard the {host} summary",
                    status == "success",
                    f"status={status} — you should have heard: {text!r}",
                )
        check(
            "No stray workers left behind",
            not (worker_pids() - workers_before),
        )

    failed = [name for name, passed in CHECKS if not passed]
    print(f"\n{len(CHECKS) - len(failed)}/{len(CHECKS)} checks passed.")
    if failed:
        print("Failed: " + ", ".join(failed))
        return 1
    print("For live acceptance, take one real turn in each host.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
