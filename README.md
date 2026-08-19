# TurnEcho

TurnEcho is a Codex plugin that reads the agent's final response out loud. It
turns the end of a Codex turn into something closer to a spoken conversation,
so you can hear the result without watching the screen.

TurnEcho currently speaks the full final response. Producing a shorter,
conversation-style summary is planned but is not implemented yet.

## How it works

1. Codex emits a `Stop` hook after completing a turn.
2. The hook validates the event and adds the final response to a SQLite queue.
3. The hook starts a detached worker and immediately returns control to Codex.
4. One global worker loads KittenTTS and speaks queued responses in order.
5. The worker exits after 10 minutes without a new job.

The queue prevents overlapping Codex sessions from speaking at the same time.
Duplicate hook events for the same turn are ignored. If a worker exits while a
job is in progress, the next worker returns that job to the queue.

## Current status

TurnEcho is an early local plugin. The basic hook, queue, worker, and audio
playback flow is implemented. Current limits are:

- Codex is the only supported host.
- The complete final response is spoken without summarization or text cleanup.
- The voice is fixed to KittenTTS `Hugo`.
- Audio uses a fixed sample rate of 24 kHz.
- Worker locking depends on `fcntl`, so Windows is not supported.
- Settings are constants in the source code. There is no user configuration
  file or UI yet.

## Requirements

- macOS or Linux
- Python 3.13 or newer
- [uv](https://docs.astral.sh/uv/)
- A working system audio output device
- Network access on first use so KittenTTS can obtain its model files

## Install for local development

Clone the repository, then install the Python environment:

```sh
uv sync
```

Add this repository as a local plugin in Codex. The plugin metadata is in
`.codex-plugin/plugin.json`, and its `Stop` hook is defined in
`hooks/hooks.json`.

Plugin installation support can differ between Codex releases. If your Codex
version does not offer local plugin installation, you can test the hook from
the repository with:

```sh
printf '%s' '{
  "hook_event_name": "Stop",
  "session_id": "manual-session",
  "turn_id": "manual-turn-1",
  "last_assistant_message": "TurnEcho is ready.",
  "stop_hook_active": false
}' | uv run turnecho-hook
```

The command prints `{}` immediately. Audio is played by the detached worker,
so it may begin shortly after the command finishes. Use a new `turn_id` for
each manual test because TurnEcho deduplicates turns.

## Local data

TurnEcho creates these files in `~/.config/turnecho/`:

- `turnecho.db`: SQLite queue and job history, including complete agent
  responses
- `worker.lock`: process lock used to keep one audio worker active
- `worker.log`: worker output and playback errors

Response text stays in the SQLite database after playback. Do not use TurnEcho
for sensitive responses unless storing that text locally is acceptable. Model
inference and audio playback run locally after the model files are available.

## Troubleshooting

### No audio plays

Check `~/.config/turnecho/worker.log` first. Common causes are an unavailable
audio output device, model download failure, or missing system audio support.

Run the worker in the foreground to see errors directly:

```sh
uv run python -m turnecho.worker
```

The worker only loads the TTS model when at least one pending job exists.

### A manual test does not play again

Each `(host, session_id, turn_id)` tuple is unique. Change `turn_id` before
repeating a manual hook test.

### Playback starts slowly

The first queued response may take longer because the TTS model must be
downloaded and loaded. A running worker keeps the model in memory while it
waits for more jobs.

## Development

Install dependencies:

```sh
uv sync
```

Run lint and formatting checks:

```sh
make check
```

Run tests:

```sh
uv run python -m unittest discover -s tests
```

Source code lives in `src/turnecho/`. Tests use Python's standard `unittest`
framework and mock TTS and audio output, so the normal test suite does not play
sound or load the model.

## Project structure

```text
.codex-plugin/plugin.json  Codex plugin metadata
hooks/hooks.json           Codex Stop hook registration
src/turnecho/hook.py       Hook input validation and job creation
src/turnecho/sqlite.py     Persistent queue and job state
src/turnecho/worker.py     Worker lock, TTS generation, and audio playback
tests/                     Hook, queue, and worker tests
```
