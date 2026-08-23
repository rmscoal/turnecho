<div align="center">
  <table>
    <tr>
      <td align="center" bgcolor="#202124">
        <img src="assets/turnecho-logo.png" alt="TurnEcho logo" width="420">
      </td>
    </tr>
  </table>
</div>

<h1 align="center">TurnEcho</h1>

<p align="center">
  <strong>Hear the result of every Codex turn.</strong><br>
  A local Codex plugin that speaks a short, useful summary while keeping the
  full response on screen.
</p>

<p align="center">
  <a href="https://github.com/rmscoal/turnecho/blob/v0.1.0/LICENSE">MIT</a>
  · Python 3.13+
  · macOS and Linux
  · Codex plugin
</p>

TurnEcho adds a lightweight audio layer to Codex. It asks the agent for one
short spoken summary, validates that summary at the end of the turn, and
plays it locally in the background. You can keep coding, reviewing, or step
away from the screen while still hearing what changed.

The original Codex response is never replaced or read in full. TurnEcho only
speaks a valid summary marker appended to `last_assistant_message`.

## Why TurnEcho

Long agent turns contain useful detail, but not every moment needs another
screen-sized response. TurnEcho separates the detailed written answer from a
small spoken signal:

- **Stay in the flow.** Hear the outcome, blocker, or next action without
  opening the transcript immediately.
- **Keep the full answer.** Codex still returns its normal response. TurnEcho
  does not rewrite, truncate, or inject audio into the agent response.
- **Use a local audio path.** Summary validation, queueing, text-to-speech,
  and playback happen on the local machine after installation.
- **Handle concurrent work safely.** Multiple Codex sessions share one SQLite
  queue and one process lock, so summaries play sequentially instead of
  talking over each other.

## Features

- **Summary-only speech:** speaks 1 to 3 short conversational sentences,
  limited to the validated TurnEcho summary.
- **Fast hooks:** both Codex hooks use the Python standard library and return
  immediately. TTS model loading and playback stay out of the hook process.
- **Persistent queue:** SQLite stores pending, processing, successful, and
  failed jobs so audio work can continue after the hook exits.
- **Atomic claims:** database transactions ensure that only one worker owns a
  queued job at a time.
- **Cross-session serialization:** an `fcntl` lock allows one worker to own
  audio playback across Codex sessions.
- **Duplicate protection:** repeated hook events for the same
  `(host, session_id, turn_id)` are ignored.
- **Crash recovery:** abandoned `processing` jobs return to `pending` when a
  new worker takes the lock.
- **Lazy model loading:** KittenTTS loads only when pending work exists.
- **Quiet failure behavior:** invalid summaries, hook errors, and queue errors
  preserve Codex's response. Worker failures are recorded in the worker log
  and database without producing audio.
- **Deterministic configuration:** a local CLI changes voice, speech speed, and
  enabled state without routing through an LLM or MCP server.

## Requirements

- macOS or Linux
- Python 3.13 or newer
- [Codex CLI](https://developers.openai.com/codex/cli)
- [uv](https://docs.astral.sh/uv/), available on `PATH` during installation
  and whenever Codex runs the hooks
- a working system audio output device
- network access during installation so KittenTTS can obtain its model files

TurnEcho uses `uv` to select the Python 3.13+ runtime and to run the hooks
without dependency synchronization. The worker lock uses `fcntl`, so Windows
is not supported.

## Installation

Choose one installation method:

- For normal use, install the released plugin from GitHub. A local checkout is
  not required.
- For development, install from a local clone so code changes can be tested.

Do not use both methods for the same installation.

### Install from GitHub

Run the TurnEcho installer directly from GitHub:

```sh
uvx --from git+https://github.com/rmscoal/turnecho.git@v0.1.0 turnecho-install
```

This is the recommended installation path because audio dependencies are part
of the product. The installer:

- resolves KittenTTS and `sounddevice`;
- loads the TTS model and validates the default 24 kHz audio output before
  changing Codex;
- adds the `turnecho` GitHub marketplace;
- runs `codex plugin add turnecho@turnecho`;
- creates the runtime in Codex's installed plugin cache;
- installs the `turnecho` command into `~/.local/bin`; and
- removes a newly added plugin and marketplace if a later step fails.

If dependencies, model files, or the audio output cannot be prepared, the
command fails and TurnEcho is not added to Codex. A local checkout is not
required. Direct `codex plugin add` has no dependency-preflight lifecycle, so
it is an internal installer step rather than the recommended user command.

Both hooks use only Python's standard library and the TurnEcho source. They
run through `uv` with `--no-sync`, so they use the project's compatible Python
runtime without resolving or downloading dependencies during a turn. The
detached audio worker uses the same prepared `uv` runtime. Start a new Codex
thread after installation and review the plugin hook if Codex asks for trust.

The installer prepares both the plugin and CLI in one operation. If it reports
that `~/.local/bin` is not on `PATH`, add that directory to your shell's `PATH`
before running `turnecho`.

The repository marketplace is defined in
`.agents/plugins/marketplace.json`. It points Codex at the root plugin in this
GitHub repository and pins this release to `v0.1.0`. Future releases will
replace the tag in the installation command and marketplace entry.

### Install a local checkout

For development from a clone, run this command from the repository root:

```sh
uv run --no-dev python scripts/install_local_plugin.py
```

The installer first runs `uv sync --no-dev`, loads the model, validates the
default audio output, and stops if any step fails. It then creates a source
link at `~/plugins/turnecho`, creates or updates
`~/.agents/plugins/marketplace.json`, and installs through Codex by running:

```sh
codex plugin add turnecho@personal
```

It also installs `~/.local/bin/turnecho` as a managed link to the prepared
checkout environment. Updates switch this link only after runtime validation
succeeds.

It never replaces an existing real directory. Use `--dry-run` to inspect the
planned changes or `--skip-codex` to prepare the local marketplace without
installing through the Codex CLI.

This local script is only needed for development. GitHub users should use the
preflight installer above.

The plugin metadata is in `.codex-plugin/plugin.json`, and its hooks are
defined in `hooks/hooks.json`.

## Manage the installation

### Update a GitHub installation

Codex installs a cached copy of the plugin. To refresh the installed
`v0.1.0` release, run the preflight installer in update mode:

```sh
uvx --refresh --from git+https://github.com/rmscoal/turnecho.git@v0.1.0 turnecho-install --update
```

### Update a local checkout

After changing an installed local checkout, run:

```sh
uv run --no-dev python scripts/install_local_plugin.py --update
```

This updates the plugin version to a single `+codex.<timestamp>` cachebuster,
keeps the existing marketplace entry, and reinstalls it with
`codex plugin add turnecho@personal`. The initial local marketplace entry must
already exist.

### Uninstall

Remove a GitHub installation with:

```sh
codex plugin remove turnecho@turnecho
```

To also remove the configured TurnEcho marketplace, run:

```sh
codex plugin marketplace remove turnecho
```

Remove a local-checkout installation with:

```sh
codex plugin remove turnecho@personal
```

Both installation methods create a managed command link. Remove it after
uninstalling the plugin:

```sh
rm ~/.local/bin/turnecho
```

Only run this command when that path is still the TurnEcho-managed symlink.

Removing the plugin does not delete TurnEcho's runtime data. The configuration,
queue, worker log, and stored summaries remain under `~/.config/turnecho/`. To
remove this data too, first back up anything you need, then run:

```sh
rm -rf ~/.config/turnecho
```

This permanently removes the local queue, logs, and stored summaries.

## How TurnEcho works

### Summary marker

The visible response remains the normal answer for the user. The model also
appends a hidden HTML comment containing a compact spoken summary:

```text
<!-- turnecho-summary:v1
Implemented the queue and worker flow. Tests pass, and the next step is to review the install path.
-->
```

The `UserPromptSubmit` hook supplies the summary instruction as
`additionalContext`. When Codex emits `Stop`, TurnEcho reads only the final
marker from `last_assistant_message`. The marker must be at the end of the
message, use the expected format, and contain no nested comment syntax. The
summary is normalized and capped before it enters the queue.

If the marker is missing or invalid, TurnEcho does nothing and the normal
Codex response is still returned unchanged.

### Runtime flow

```mermaid
flowchart LR
    U[User prompt] --> C[Codex]
    C -->|UserPromptSubmit| P[Prompt hook\nstdlib only]
    P -->|additionalContext\nsummary instruction| C
    C -->|visible response\nhidden summary marker| S[Stop hook\nvalidate and enqueue]
    S -->|INSERT if new turn| Q[(SQLite queue)]
    S -->|detached start| W[One worker process]
    W -->|fcntl lock| L[Global worker lock]
    W -->|atomic claim| Q
    W --> T[KittenTTS\nlocal model]
    T --> A[System audio output]
```

The hook path is intentionally small:

1. `UserPromptSubmit` adds the summary instruction without resolving project
   dependencies or loading the TTS model.
2. Codex completes the turn and returns its normal response, including the
   hidden marker when the instruction was followed.
3. `Stop` validates the marker, inserts one deduplicated job into SQLite,
   snapshots the configured voice and speed, and starts the worker in a
   detached process.
4. The worker acquires the cross-process lock, recovers abandoned jobs, claims
   pending work atomically, generates speech, and plays audio sequentially.
5. The worker exits after 10 minutes without new work. The model stays loaded
   while the worker is active.

## Configuration

TurnEcho configuration is managed directly from a terminal. These commands do
not invoke an LLM and do not require MCP:

```sh
turnecho config show
turnecho config set voice Luna
turnecho config set speed 1.1
turnecho disable
turnecho enable
turnecho voices
turnecho doctor
turnecho test
```

Use `turnecho config show --json`, `turnecho voices --json`, or
`turnecho doctor --json` for machine-readable output. `turnecho test` loads the
TTS model and speaks a fixed local test phrase. Other configuration commands do
not load the model.

The configuration file is `~/.config/turnecho/config.json`:

```json
{
  "schema_version": 1,
  "enabled": true,
  "voice": "Hugo",
  "speed": 1.0
}
```

Supported voices are Bella, Jasper, Luna, Bruno, Rosie, Hugo, Kiki, and Leo.
Speed must be between `0.5` and `2.0`. Missing configuration uses the defaults.
An invalid existing configuration fails silently: hooks return valid empty JSON,
report the problem to stderr, and do not request or queue a summary.

Configuration writes are locked and atomically replaced. Disabling TurnEcho
prevents new summary instructions and queue entries. Jobs queued before it was
disabled are still processed using the voice and speed captured with each job.

The SQLite migration engine is defined in `src/turnecho/sqlite.py` and loads
numbered DDL files from `src/turnecho/migrations/`. Applied versions and
checksums are recorded in the database. Migrations and their ledger updates run
in one immediate transaction. Each process performs this initialization once
for each database path, so normal queue polling does not repeatedly take the
migration write lock.

## Current status and boundaries

TurnEcho is an early local plugin. The hook, queue, worker, and audio playback
flow are implemented, with these current boundaries:

- Codex is the only supported host input.
- Only a valid summary marker at the end of `last_assistant_message` is
  spoken.
- Turns without a valid summary are ignored without audio.
- The default voice is KittenTTS `Hugo` and can be changed through the CLI.
- Speech speed can be configured from `0.5` to `2.0`.
- Audio uses a fixed sample rate of 24 kHz.
- There is no graphical configuration UI.
- Worker locking depends on `fcntl`, so Windows is not supported.

## Local data and privacy

TurnEcho creates these files in `~/.config/turnecho/`:

- `config.json`: validated voice, speed, and enabled settings
- `config.lock`: serializes concurrent configuration updates
- `turnecho.db`: SQLite queue and job history, including stored summaries
- `worker.lock`: process lock used to keep one audio worker active
- `worker.log`: worker output and playback errors

Summary text stays in the SQLite database after playback. Do not use TurnEcho
for sensitive responses unless storing that text locally is acceptable. Model
inference and audio playback run locally after the model files are available.
Network access is needed during installation for dependencies and model files.

## Troubleshooting

### No audio plays

Check `~/.config/turnecho/worker.log` first. Common causes are an unavailable
audio output device, model download failure, or missing system audio support.

Check configuration and the local runtime:

```sh
turnecho config show
turnecho doctor
turnecho test
```

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

### Test the hook manually

If your Codex version does not offer local plugin installation, you can test
the hook from the repository with:

```sh
printf '%s' '{
  "hook_event_name": "Stop",
  "session_id": "manual-session",
  "turn_id": "manual-turn-1",
  "last_assistant_message": "TurnEcho is ready.\n\n<!-- turnecho-summary:v1\nTurnEcho is ready and speaking is configured.\n-->\n",
  "stop_hook_active": false
}' | PLUGIN_ROOT="$PWD" PYTHONPATH="$PWD/src" uv run --project "$PWD" --no-dev --no-sync python -m turnecho.stop_hook
```

The command prints `{}` immediately. Audio is played by the detached worker,
so it may begin shortly after the command finishes. Use a new `turn_id` for
each manual test because TurnEcho deduplicates turns.

### Run the checks

Run lint and formatting checks:

```sh
make check
```

Run tests:

```sh
uv run --no-dev python -m unittest discover -s tests
```

Source code lives in `src/turnecho/`. Tests use Python's standard `unittest`
framework and mock TTS and audio output, so the normal test suite does not
play sound or load the model.

## License

TurnEcho is released under the [MIT License](LICENSE).
