# Project Agent Instructions

These instructions apply to all work in this repository.

## Project purpose

TurnEcho is a local Codex plugin that speaks the final agent response. The
current implementation registers a Codex `Stop` hook, stores messages in a
SQLite queue, and processes them sequentially with one detached KittenTTS
worker.

Current behavior matters when changing code or documentation:

- Only Codex hook input is supported.
- The full `last_assistant_message` is spoken. There is no summary step yet.
- Jobs are deduplicated by `(host, session_id, turn_id)`.
- Only one worker may own the cross-process `fcntl` lock.
- Abandoned `processing` jobs are requeued when a new worker takes the lock.
- The worker loads the TTS model only when pending work exists.
- The worker processes audio sequentially and exits after an idle timeout.

Do not describe planned summary, configuration, voice selection, or additional
host support as implemented behavior.

## Repository layout

- `.codex-plugin/plugin.json`: Codex plugin metadata
- `hooks/hooks.json`: plugin hook registration and command
- `src/turnecho/hook.py`: hook parsing, validation, queue insertion, and worker
  startup
- `src/turnecho/sqlite.py`: SQLite schema and queue operations
- `src/turnecho/worker.py`: process locking, recovery, TTS, and audio playback
- `src/turnecho/schema.py`: Pydantic input and job models
- `src/turnecho/constant.py`: shared paths, timing, status, and host constants
- `tests/`: standard-library `unittest` tests

## Engineering rules

- Keep the hook fast. Do not load TTS, wait for audio, or perform other slow
  work in the hook process.
- Preserve the hook's stdout contract. It must print valid JSON (`{}`) so it
  does not modify the agent response.
- Send diagnostics to stderr or the worker log, never to hook stdout.
- Keep database claims atomic. Changes to queue ownership must remain safe when
  several hooks or worker processes start at nearly the same time.
- Use parameterized SQL. Never construct SQL from hook or message input.
- Preserve job deduplication unless a requirement explicitly changes its key or
  semantics.
- Keep model and audio imports inside the worker path. Tests and empty-queue
  workers must not load these expensive dependencies.
- Keep playback sequential unless queue ownership and overlapping audio are
  deliberately redesigned and tested.
- Treat agent messages and worker logs as potentially sensitive local data. Do
  not add message logging or external transmission without an explicit
  requirement and clear documentation.
- Prefer small, direct functions and existing dependencies. Avoid adding an
  abstraction for a single implementation.
- Keep platform assumptions explicit. `fcntl` currently limits worker locking
  to macOS and Linux.

## Code style

- Support Python 3.13 or newer.
- Follow Ruff configuration in `pyproject.toml`.
- Use clear type hints for new or changed public functions.
- Use `pathlib.Path` for filesystem paths.
- Use context managers or explicit cleanup for database connections, locks,
  files, and subprocess resources.
- Keep constants in `constant.py` when they are shared across modules.
- Keep comments focused on reasons, constraints, and non-obvious behavior.

## Tests and verification

Add or update focused tests for behavior changes. Mock TTS and audio output in
normal automated tests so tests do not download models or play sound.

Run targeted tests while developing, then run:

```sh
uv run python -m unittest discover -s tests
make check
```

For queue changes, cover deduplication, atomic claims, status transitions, and
recovery where relevant. For hook changes, verify stdout, stderr, validation,
and detached subprocess behavior. For worker changes, verify lock cleanup and
both success and failure job states.

Before finishing, review the complete diff and confirm documentation still
matches actual behavior.
