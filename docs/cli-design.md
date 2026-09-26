# TurnEcho v2 CLI and Distribution Design

Decided companion to `.agents/plans/2026-09-18-go-rewrite.md` (phases) and
`docs/v1-behavior.md` (v1 behavioral spec). Covers the command tree, the
interactive layer, the install/upgrade channels, and the resulting codebase
structure.

## Command tree (Cobra)

```text
turnecho                        interactive home menu (TTY) or help (non-TTY)
turnecho setup                  guided wizard: voice, speed, test
turnecho config show [--json]
turnecho config path
turnecho config set [model|voice|speed] [value]   (no value: picker)
turnecho config reset [key|--all]
turnecho enable
turnecho disable
turnecho voices [--json]
turnecho models [--json]
turnecho doctor [--json]
turnecho test
turnecho say <text> [--output file.wav]
turnecho stop
turnecho install [--host codex|claude|all] [--update] [--dry-run]
turnecho upgrade [--check] [--dry-run]
turnecho uninstall
turnecho hook prompt|stop       (hidden: host entry points)
turnecho worker                 (hidden: detached worker)
```

Rules:

- Exit codes keep v1 parity: 0 success, 2 config/usage error, 1 failure.
- `--json` on `config show`, `voices`, `models`, `doctor`: this is the agent
  and script interface (see `skills/turnecho-config/SKILL.md`) and must work
  with no terminal attached.
- `hook` and `worker` are hidden from help output; hosts call them directly.
- No command ever prompts when a value was given or stdin is not a TTY.

## Interactive layer (Huh)

Stack: Cobra routes arguments; Huh renders forms and pickers. No Bubble Tea
app in v2 scope (reserved for a future live view, if ever).

- `setup` wizard groups: (1) voice pick with spoken preview per candidate,
  (2) speed pick with one sample spoken at each candidate speed, (3) full
  test phrase, then write config once at the end.
- Preview pattern is select-then-confirm in a loop: choose an option, hear
  it, keep it or pick again. This needs only stock Select plus Confirm
  fields, no custom widget behavior.
- Bare `turnecho` on a TTY shows a small menu (Setup / Voice / Model and
  speed / Test sound / Doctor / Quit); each entry dispatches to the same
  handler the matching subcommand uses. One code path, two front doors.
- Bare `turnecho` without a TTY prints help and exits 0, so scripts and CI
  never hang on input.
- Pickers require their prerequisites and say so plainly (voice picker needs
  the downloaded model; otherwise it points at `turnecho install` first).

## Installation channels

`turnecho install` is the engine behind every channel: fetch pinned sherpa
library plus Kokoro model tarballs with SHA256 verification into
`~/.local/share/turnecho/runtimes/<v>/`, run preflight (synthesize one wav,
probe the player), register marketplaces via the `codex`/`claude` CLIs, link
`~/.local/bin/turnecho`, then offer the setup wizard. `--dry-run` prints the
plan; failures roll back link, marketplace, and runtime changes (v1 rules).

Channel 1, install.sh (primary): a small hand-written shell script. Detect
darwin-arm64, darwin-amd64, or linux-amd64 (refuse others clearly), download
the matching GitHub release asset, verify it against `checksums.txt`,
extract to a temp dir, and exec `turnecho install`. The script stays dumb;
all logic lives in Go where it is tested.

Channel 2, Homebrew tap (secondary): GoReleaser publishes a formula to a
self-hosted `rmscoal/tap` on every tag. The formula installs the binary
only and its caveats tell the user to run `turnecho install` for the model
download and marketplace registration. No homebrew-core submission until
there is real traction.

## Self-upgrade

`turnecho upgrade` serves install.sh users: resolve the latest release,
compare versions, download the platform asset plus checksums, verify,
swap-replace the running binary, refresh the runtime when the release pins
new library/model versions, run preflight, and report. `--check` only
reports availability; `--dry-run` prints the plan without changing anything.

Brew guard: when the running binary lives under a Homebrew Cellar path, the
binary is brew-managed, so `upgrade` refuses and points at
`brew upgrade turnecho`. A self-updating binary must never fight its package
manager.

## Codebase structure

```text
cmd/turnecho/main.go      wire the Cobra root and hidden commands
internal/
  cli/          command tree, flags, --json printers, exit codes
  interactive/  Huh wizard, pickers, bare-root menu, TTY guard
  config/       schema, strict validation, atomic load/store
  queue/        SQLite (modernc), migrations, claim/requeue/update
  hosts/        Codex/Claude payload parsing and output envelopes
  speak/        summary extraction, sanitizer, sentence chunker
  tts/          backend interface plus sherpa implementation
  player/       afplay/aplay probe chain and sequential playback
  worker/       lock, poll loop, chunk pipeline, logging
  install/      install/upgrade/uninstall, downloads plus verify,
                marketplace calls, link management, rollback
  version/      version string and release metadata
configs/voices.yaml       Kokoro speaker id table per model release
hooks/hooks.json          exec the installed binary directly
install.sh                primary installer (downloads release, runs install)
docs/                     v1-behavior.md plus this file
```

Dependency direction: `cmd` wires `cli`; `cli` calls `interactive` for
human flows and the domain packages otherwise; `interactive` calls into
`config`, `tts`, and `player` but is never imported by `worker`, `hook`,
or `install` paths. `tts` and `player` are shared by the worker, the
wizard, `doctor`, `test`, and `say` so previews and real speech always
sound identical.
