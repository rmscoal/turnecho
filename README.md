# TurnEcho

TurnEcho is a Codex plugin that reads a short summary of the agent's final
response out loud. It turns the end of a Codex turn into something closer to a
spoken conversation, so you can hear the result without watching the screen.

TurnEcho asks the agent to append a hidden summary marker to its final
response, then speaks only that validated summary. If the summary is missing
or invalid, or speaking fails, TurnEcho stays silent.

## How it works

1. Codex emits `UserPromptSubmit` before processing a prompt.
2. The hook asks the agent to append one short summary marker.
3. Codex emits a `Stop` hook after completing the turn.
4. The hook validates the marker and adds the summary to a SQLite queue.
5. The hook starts a detached worker and immediately returns control to Codex.
6. One global worker loads KittenTTS and speaks queued summaries in order.
7. The worker exits after 10 minutes without a new job.

The queue prevents overlapping Codex sessions from speaking at the same time.
Duplicate hook events for the same turn are ignored. If a worker exits while a
job is in progress, the next worker returns that job to the queue.

## Current status

TurnEcho is an early local plugin. The basic hook, queue, worker, and audio
playback flow is implemented. Current limits are:

- Codex is the only supported host.
- Only the validated summary is spoken. Turns without a valid summary are
  ignored without audio.
- The voice is fixed to KittenTTS `Hugo`.
- Audio uses a fixed sample rate of 24 kHz.
- Worker locking depends on `fcntl`, so Windows is not supported.
- Settings are constants in the source code. There is no user configuration
  file or UI yet.

## Requirements

- macOS or Linux
- Python 3.13 or newer
- [Codex CLI](https://developers.openai.com/codex/cli)
- [uv](https://docs.astral.sh/uv/)
- A working system audio output device
- Network access during installation so KittenTTS can obtain its model files

## Install from GitHub

Run the TurnEcho installer directly from GitHub:

```sh
uvx --from git+https://github.com/rmscoal/turnecho.git@main turnecho-install
```

This is the required installation path because audio is the main product. The
installer:

- resolves KittenTTS and `sounddevice`, loads the TTS model, and validates the
  default 24 kHz audio output before changing Codex;
- adds the `turnecho` GitHub marketplace;
- runs `codex plugin add turnecho@turnecho`;
- creates the runtime in Codex's installed plugin cache; and
- removes a newly-added plugin and marketplace if a later step fails.

If the dependencies, TTS model, or audio output cannot be prepared, the command
fails and TurnEcho is not added to Codex. A local checkout is not required. Direct
`codex plugin add` has no dependency-preflight lifecycle, so it is an internal
step of the installer rather than the recommended user command.

Both Codex hooks use only Python's standard library and the TurnEcho source.
They never wait for dependency resolution. Only the detached audio worker uses
the prepared `uv` runtime. Start a new Codex thread after installation and
review the plugin hook if Codex asks for trust.

Codex installs a cached copy of the plugin. To pick up a new GitHub commit,
run the same preflight flow in update mode:

```sh
uvx --refresh --from git+https://github.com/rmscoal/turnecho.git@main turnecho-install --update
```

The repository marketplace is defined in
`.agents/plugins/marketplace.json`. It points Codex at the root plugin in this
GitHub repository.

## Install a local checkout

For development from a clone, install the Python environment:

```sh
uv sync --no-dev
```

Then install the checkout into Codex's personal local marketplace:

```sh
uv run --no-dev python scripts/install_local_plugin.py
```

The installer first runs `uv sync --no-dev`, loads the model, validates the
default audio output, and stops if any step fails. It then creates a source link at
`~/plugins/turnecho`, creates or updates `~/.agents/plugins/marketplace.json`,
and installs through Codex by running:

```sh
codex plugin add turnecho@personal
```

It never replaces an existing real directory. Use `--dry-run` to inspect the
planned changes or `--skip-codex` to prepare the local marketplace without
installing through the Codex CLI.

For later changes to an already-installed local checkout, use the update flow:

```sh
uv run --no-dev python scripts/install_local_plugin.py --update
```

This updates the plugin version to a single `+codex.<timestamp>` cachebuster,
keeps the existing marketplace entry, and reinstalls it with
`codex plugin add turnecho@personal`. It requires the initial local marketplace
entry to exist. This is the local equivalent of the Codex plugin-creator
cachebuster flow.

This local script is only needed for development. GitHub users should use the
preflight installer above.

The plugin metadata is in `.codex-plugin/plugin.json`, and its hooks are defined
in `hooks/hooks.json`.

Plugin installation support can differ between Codex releases. If your Codex
version does not offer local plugin installation, you can test the hook from
the repository with:

```sh
printf '%s' '{
  "hook_event_name": "Stop",
  "session_id": "manual-session",
  "turn_id": "manual-turn-1",
  "last_assistant_message": "TurnEcho is ready.\n\n<!-- turnecho-summary:v1\nTurnEcho is ready and speaking is configured.\n-->",
  "stop_hook_active": false
}' | PYTHONPATH="$PWD/src" python3 -m turnecho.stop_hook
```

The command prints `{}` immediately. Audio is played by the detached worker,
so it may begin shortly after the command finishes. Use a new `turn_id` for
each manual test because TurnEcho deduplicates turns.

## Local data

TurnEcho creates these files in `~/.config/turnecho/`:

- `turnecho.db`: SQLite queue and job history, including spoken summaries
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
uv run --no-dev python -m turnecho.worker
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
uv sync --no-dev
```

Run lint and formatting checks:

```sh
make check
```

Run tests:

```sh
uv run --no-dev python -m unittest discover -s tests
```

Source code lives in `src/turnecho/`. Tests use Python's standard `unittest`
framework and mock TTS and audio output, so the normal test suite does not play
sound or load the model.

## Project structure

```text
.codex-plugin/plugin.json  Codex plugin metadata
.agents/plugins/marketplace.json  GitHub marketplace metadata
hooks/hooks.json           Codex hook registration
scripts/install_local_plugin.py  Local Codex marketplace installer
scripts/update_plugin_cachebuster.py  Local update cachebuster helper
src/turnecho/install_plugin.py  Preflighted GitHub plugin installer
src/turnecho/prompt_hook.py  Dependency-free prompt hook
src/turnecho/runtime_preflight.py  TTS model and audio output checks
src/turnecho/stop_hook.py    Stop-hook validation and job creation
src/turnecho/sqlite.py     Persistent queue and job state
src/turnecho/worker.py     Worker lock, TTS generation, and audio playback
tests/                     Hook, queue, and worker tests
```
