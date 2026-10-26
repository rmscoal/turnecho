# TurnEcho v1 Behavior (Python implementation, archived)

Essence of the deleted Python implementation, written from the code before the
wipe. The full tree is preserved on the `archive/python-v1` branch. Every
section names the source file it was taken from. The Go rewrite (v2) uses this
document as its behavioral spec.

## Product

TurnEcho is a local Codex / Claude Code plugin that speaks a short summary of
the final agent response. `UserPromptSubmit` hooks inject an instruction asking
the agent to append one summary marker; `Stop` hooks validate the marker,
queue the summary in SQLite, and a single detached worker synthesizes and
plays queued summaries sequentially. (`README.md`, `src/turnecho/worker.py`)

## Hook contracts

Entry dispatch (`hooks/hooks.json`, `hooks/run_hook.sh`):

- `UserPromptSubmit` runs `run_hook.sh prompt` with a 3s timeout, `Stop` runs
  `run_hook.sh stop` with a 10s timeout. Any failure prints `{}` so the host
  call degrades to a no-op.
- `run_hook.sh` resolves the plugin root from `CLAUDE_PLUGIN_ROOT` or
  `PLUGIN_ROOT`, requires the file `$plugin_root/pyproject.toml`, requires an
  executable runtime Python, else prints `{}` and exits 0.
- Hook stdout must be exactly one JSON line. Diagnostics go to stderr, never
  to stdout. Hook processes never load TTS or wait for audio.

Host detection (`src/turnecho/hosts/__init__.py`):

- `--host=...` argv override always wins, even for unknown hosts (which then
  fail safe to empty output).
- Otherwise a payload containing `transcript_path` is Claude Code; everything
  else falls through to Codex parsing, which rejects unknown shapes safely.
- Default (fail-safe) output for both hosts is the string `{}`.

Prompt hook (`src/turnecho/prompt_hook.py`):

1. Parse stdin JSON; on any error print the error to stderr and print the
   host default output; exit 0.
2. Reject non-`UserPromptSubmit` payloads with default output.
3. Load config; on `ConfigError` print to stderr plus default output; when
   disabled, print default output silently.
4. Print the additional-context envelope
   `{"hookSpecificOutput": {"hookEventName": "UserPromptSubmit",
   "additionalContext": "<instruction>"}}` and best-effort spawn the
   background worker (to warm it before the Stop event).

Stop hook (`src/turnecho/stop_hook.py`):

1. Parse stdin JSON; on error print to stderr plus host default output.
2. Parse the host payload into a normalized event
   (`host`, `session_id`, `turn_id`, `message`); `None` means default output.
3. Load config; on `ConfigError` print to stderr plus default output; when
   disabled, print default output silently.
4. Extract the summary marker; missing or invalid means default output with
   no queue insert and no worker spawn.
5. Insert the job (snapshotting configured voice and speed); on DB error
   print to stderr plus default output.
6. Best-effort spawn the background worker; print the host default output.

Codex parsing (`src/turnecho/hosts/codex.py`):

- Stop requires a dict with `hook_event_name == "Stop"`, non-blank string
  `session_id`, non-blank string `turn_id`, non-blank string
  `last_assistant_message`, and falsy `stop_hook_active`.
- Prompt-submit requires `hook_event_name == "UserPromptSubmit"`.

Claude parsing (`src/turnecho/hosts/claude.py`):

- Same Stop requirements except there is no `turn_id` field; the turn id is
  synthesized by `derive_turn_id(transcript_path, message)`:
  `turn-{assistant_count}-{transcript_size}-{sha1_12}`, where the assistant
  count is counted over at most the last 65536 bytes of the transcript
  (`CLAUDE_TRANSCRIPT_TAIL_BYTES`), considering only lines starting with
  `{` that parse as JSON objects with `type == "assistant"`.
- Unreadable transcript falls back to `turn-{uuid8}-{sha1_12}` so the turn is
  still spoken instead of colliding.

Summary marker (`src/turnecho/summary.py`, `src/turnecho/constant.py`):

- The prompt instruction asks for exactly one trailing block:
  `\n\n<!-- turnecho-summary:v1\n<summary>\n-->\n\n`, 1 to 3 short spoken
  sentences, max 60 words, no Markdown/URLs/paths/code/IDs/lists.
- Validation: normalize CRLF, strip trailing whitespace, require the message
  to end with `\n-->`; take text after the last `<!-- turnecho-summary:v1\n`;
  reject when the inner text contains `<!--` or `-->`; collapse all
  whitespace to single spaces; reject empty or longer than 500 chars
  (`TURNECHO_SUMMARY_MAX_CHARS`).

## Queue (`src/turnecho/sqlite.py`, `src/turnecho/schema.py`, migrations)

Schema (`001_create_schema.sql`): `turnecho_jobs(id TEXT PK, host, session_id,
turn_id, message, voice, speed, processing_status, created_at, started_at,
completed_at, error_message)` with non-empty CHECKs, voice restricted to the
8 known voices, speed in [0.5, 2.0], `UNIQUE(host, session_id, turn_id)`,
plus index on `(processing_status, created_at)`.

- SQLite file `~/.config/turnecho/turnecho.db`, WAL mode, 5s busy timeout.
- Packaged migrations: files `NNN_name.sql` discovered from the bundle,
  each checksumed (SHA256); `turnecho_schema_migrations(version, name,
  checksum, applied_at)` tracks them; unknown or checksum-mismatched
  applied migrations raise `MigrationError`; all pending migrations apply in
  one `BEGIN IMMEDIATE` transaction.
- `insert_job_db`: validates voice/speed in code, inserts with
  `ON CONFLICT(host, session_id, turn_id) DO NOTHING`, returns whether a row
  was inserted (dedupe).
- `claim_next_job_from_db`: one `BEGIN IMMEDIATE` transaction, `UPDATE ...
  WHERE rowid = (SELECT rowid ... WHERE processing_status = 'pending'
  ORDER BY created_at, rowid LIMIT 1) ... RETURNING ...` (atomic claim).
- `requeue_processing_jobs_from_db`: resets all `processing` rows to
  `pending`, clearing started/completed/error columns.
- `update_job_db`: persists status, completed_at, error_message by id.
- All SQL is parameterized; no SQL is built from message input.

## Worker (`src/turnecho/worker.py`, `src/turnecho/exc.py`)

- `spawn_background_worker`: appends to `~/.config/turnecho/worker.log`
  (creating parents), spawns `[sys.executable, -m turnecho.worker]` fully
  detached (`stdin=DEVNULL`, `start_new_session`, `close_fds`).
- Lock: `~/.config/turnecho/worker.lock`, `fcntl.flock(LOCK_EX | LOCK_NB)`
  with retries for 1.0s every 0.25s; on timeout raise
  `WorkerAlreadyRunning` (only one worker owns the lock across processes).
- `process()`: take the lock, requeue abandoned `processing` jobs, exit
  quietly when no pending jobs exist (before loading TTS), else run the loop.
- Loop: claim next job; when idle longer than 600s, exit the process.
  Per job: reload config, rebuild the TTS model only when the configured
  model id changed, `model.generate(message, voice, speed)`, `play` at
  24kHz, `wait` for playback, mark success (or failed with str(error));
  always `update_job_db`.
- Preflight (`src/turnecho/runtime_preflight.py`): `check_output_settings`
  at 24kHz plus constructing the default model; failure prints to stderr
  and exits 1. Used by the installer and `doctor`.

## Config (`src/turnecho/config.py`, `src/turnecho/constant.py`)

- JSON file `~/.config/turnecho/config.json`, schema v1:
  `{schema_version: 1, enabled: bool, model: str, voice: str, speed: float}`.
- Missing file means defaults (`enabled=true`, model `mini`, voice `Hugo`,
  speed 1.0). Present files must be exact objects: unknown or missing keys,
  wrong types, unknown model/voice, non-finite or out-of-range speed
  (bool rejected), or wrong schema version all raise `ConfigError`.
- Models: `mini`/`micro`/`nano` mapped to KittenTTS HuggingFace ids.
  Voices: Bella, Jasper, Luna, Bruno, Rosie, Hugo, Kiki, Leo.
- Writes serialize on `config.lock` (`fcntl.LOCK_EX`), then write a
  same-directory temp file (mode 0600, fsync) and `os.replace` it atomically.
  `update_config` and `reset_config` read-modify-write under one lock.

## CLI (`src/turnecho/cli.py`, `skills/turnecho-config/SKILL.md`)

Entry `turnecho`, exit 0 on success, 2 on `ConfigError`, 1 on any other error:

- `config show [--json]`, `config path`, `config set model|voice|speed VALUE`
  (speed parsed as float, non-numeric rejected), `config reset
  enabled|model|voice|speed|--all` (key or `--all` required).
- `enable` / `disable` print `TurnEcho enabled.` / `TurnEcho disabled.`
- `voices [--json]`, `models [--json]` list supported values and defaults.
- `doctor [--json]` loads config, checks audio output settings, constructs
  the model, prints readiness or JSON payload.
- `test` synthesizes `TurnEcho is configured and ready.` with the configured
  voice/speed and plays it, then prints completion.
- The CLI command lives at `~/.local/bin/turnecho` as a symlink managed with
  atomic replace; the installer refuses to replace non-symlinks or links
  that do not point at a TurnEcho runtime, and supports rollback/restore and
  managed-link removal. `turnecho` warns when its directory is not on PATH.

## Installer (`src/turnecho/install_plugin.py`, `scripts/`)

GitHub installer (`turnecho-install`): `--update` or `--uninstall`
(mutually exclusive), optional `--host codex|claude` (install only, never
with `--uninstall`); default host set is every detected host CLI, else error.
Requires `uv` plus each selected host CLI before changing anything.

- Codex: `codex plugin marketplace list/add/remove --json`,
  `codex plugin list/add/remove --json`. The marketplace must point at the
  TurnEcho GitHub repo (git source, accepted URL spellings); the installed
  plugin version and git ref must equal the release being installed.
- Claude: `claude plugin marketplace list/add/update/remove --json`,
  `claude plugin list/install/update/uninstall --json --scope user`.
  Installed version must equal the release (Claude tracks the repo, not a
  pinned ref, so the error message says to retry after release publish).
- Runtime: `~/.local/share/turnecho/runtimes/<version>/` (non-editable
  `uv sync --extra audio`), guarded by a `.turnecho-runtime.json`
  `{name, version}` ownership marker; only marked directories are ever
  removed. Replacement moves the old runtime aside, builds at the final
  path, runs preflight plus `--version`, and only then commits (deletes the
  backup); any failure restores the backup. Failures roll back hosts in
  reverse order plus command link and runtime, collecting rollback errors
  into the final message.
- Uninstall: per host remove plugin then marketplace (skipping hosts whose
  CLI is absent), then remove the managed command link and all marked
  runtimes. Never touches user config or database.
- Local installer (`scripts/install_local_plugin.py`): symlinks a checkout
  into a personal marketplace, prepares `uv sync` runtime plus command link,
  runs `codex plugin add` / `claude plugin marketplace add` + `install`,
  supports `--dry-run`, `--force`, `--update` (bumps a Codex cachebuster),
  `--skip-codex/--skip-claude/--skip-dependency-sync`, with full rollback
  of link, marketplace file, manifest, command, and runtime.
- Cachebuster (`scripts/update_plugin_cachebuster.py`): rewrites
  `.codex-plugin/plugin.json` version to `<base>+codex.<UTC-timestamp>` so
  Codex picks up local updates.
- E2E (`scripts/e2e_hook_check.py`, `make e2e`): runs real hooks in a temp
  HOME for both hosts plus long-transcript repeat and unknown-host cases,
  asserts stdout contracts and 4 queued rows reaching terminal states, and
  speaks the summaries aloud unless `E2E_QUIET=1`.

## Data paths

- `~/.config/turnecho/config.json`, `turnecho.db`, `worker.lock`,
  `worker.log`, `config.lock`
- `~/.local/share/turnecho/runtimes/<version>/` (+ `.turnecho-runtime.json`)
- `~/.local/bin/turnecho` (managed symlink)
- Codex plugin cache: `$CODEX_HOME/plugins/cache/turnecho/turnecho/<version>`
  (`CODEX_HOME` default `~/.codex`)

## Constraints worth keeping in v2

- Hooks stay fast: no model load, no audio wait in the hook process.
- Hook stdout is exactly one JSON line; diagnostics go to stderr or the
  worker log; every failure mode prints the host default output.
- Queue ownership stays atomic across concurrent hooks and workers.
- SQL stays parameterized; Playback stays sequential unless deliberately
  redesigned; agent messages are never logged or transmitted.
