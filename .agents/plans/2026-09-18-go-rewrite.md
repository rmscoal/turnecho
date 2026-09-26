## Goal

Delete the Python implementation and rewrite TurnEcho from a clean tree as a single-binary Go CLI that speaks agent answers for a peer-programming style coding buddy, with Kokoro as the local text-to-speech backend, zero Python runtime, and a rich configuration experience. There are no existing users to protect, so nothing is migrated or kept backward compatible.

## Success Criteria

- The repository contains zero Python: no `.py` files, no `pyproject.toml`, no `uv.lock`, no `.venv`, and the old behavior is preserved only in a git tag plus one essence document.
- One `turnecho` binary per supported platform installs the plugin, configures speech, runs hooks, and synthesizes audio with no Python interpreter or virtualenv on the machine.
- Hooks work on both hosts (Codex, Claude Code) with the stdin JSON in, stdout JSON out contract and fast-fail empty-object behavior.
- Speech covers short and long agent answers: short replies speak promptly, long replies speak chunk by chunk without long silence before the first sound.
- The CLI covers the full surface: show/set/reset config, enable/disable, list voices and models, check runtime health, speak a test phrase, install/uninstall.
- CI builds, tests, and smoke-tests every shipped platform binary.

## Context And Current Facts

Current Python implementation, verified in the repo this run (to be archived, then deleted):

- `hooks/hooks.json` runs `sh .../run_hook.sh prompt|stop` with 3s/10s timeouts and falls back to printing `{}`.
- `hooks/run_hook.sh` resolves the plugin root, requires a versioned venv Python, and prints `{}` when the runtime is missing.
- `src/turnecho/stop_hook.py`: stdin JSON, host detection, config load, summary-marker extraction, job insert, best-effort worker spawn, default host output.
- `src/turnecho/worker.py`: one global worker behind an `fcntl` lock file, abandoned-job requeue, poll interval 0.25s, idle exit after 600s, generate then play then wait per job.
- `src/turnecho/sqlite.py`: WAL mode, packaged checksumed SQL migrations, dedupe key `(host, session_id, turn_id)`, atomic claim via `BEGIN IMMEDIATE` plus `UPDATE ... RETURNING`.
- `src/turnecho/config.py` plus `src/turnecho/cli.py`: strict schema v1 JSON config (`enabled`, `model`, `voice`, `speed`), and commands `config show|path|set|reset`, `enable`, `disable`, `voices`, `models`, `doctor`, `test`.
- `src/turnecho/constant.py`: config, database, lock, and log paths, 24kHz audio, speed range 0.5 to 2.0.
- `src/turnecho/install_plugin.py`: marketplace add/remove/rollback, versioned runtime prepare/commit/rollback, per-host uninstall.
- Plugin scaffolding to keep: `.codex-plugin/plugin.json`, `.claude-plugin/marketplace.json`, `.claude-plugin/plugin.json`, `skills/`, `assets/` (referenced by the manifest), `.agents/plugins/marketplace.json`.
- Local environment verified this run: Go 1.26.2 installed, `/usr/bin/afplay` present (v2.0), `syscall.Flock` exists, and plain `go build` with `GOOS`/`GOARCH` cross-compiles a static Linux binary and a macOS arm64 binary.

External facts verified by inspecting primary sources this run (see Sources):

- `go build` compiles code into an executable; cross-compilation to Linux and macOS from one machine was verified locally.
- sherpa-onnx publishes prebuilt native binaries and documents a Go API with a Kokoro model config (`OfflineTtsKokoroModelConfig`: model, voices, tokens, data dir) and generation config (speaker id, speed). Its native usage references a shared-library directory through `LD_LIBRARY_PATH` / `DYLD_LIBRARY_PATH`.
- Kokoro weights are Apache licensed; the documented English model is 24kHz with 11 sid-addressed speakers, and model tarballs are released under sherpa-onnx `tts-models`.
- Qwen3-TTS is Apache-2.0 but its official README documents a Python path (`pip install`, torch, flash-attention, GPU memory notes) with 0.6B/1.7B models; no binary distribution was identified in the inspected content.
- `modernc.org/sqlite` is a CGo-free port of SQLite usable through Go's standard `database/sql` interface.
- Cobra is a library for building modern CLI applications; Bubble Tea is a TUI framework following the Elm architecture.
- Bun can compile TypeScript into a single-file executable with cross-compile targets, so a TypeScript binary is possible in principle.
- GoReleaser automates release engineering for tagged releases.
- `golang.org/x/sys/unix` provides `Flock` for the worker lock port.
- Ubuntu documents `aplay` as the ALSA command-line player (provided by `alsa-utils`) that detects format from the file header.

## Constraints And Non-goals

- Single user (the author). No upgrade path, no backward compatibility, no config or database migration from v1. Fresh schema and fresh config are fine.
- Fully local speech, no API keys, no network calls at speak time. Network is allowed only at install time (binary, library, and model downloads) with pinned versions and SHA256 verification.
- macOS (arm64 primary) and Linux x64 are the supported platforms. Windows is a non-goal for this rewrite.
- No MCP server in this rewrite. The hook plus CLI surface is the product; an MCP surface can be proposed separately later.
- The rewrite keeps the host hook protocols (Codex and Claude payload shapes) and the SQLite queue semantics (dedupe key, atomic claim, requeue), and may extend the spoken-content rule (buddy mode) behind config.
- Python removal is total: no interpreter, no venv, no lockfile, no `.py` files anywhere in the tree.

## Key Decisions

0. **Wipe, do not migrate.** With no users, the Python tree is archived (git tag `v1-python-final` plus an `archive/python-v1` branch) and its core behaviors are captured in one essence document (`docs/v1-behavior.md`), then every Python file is deleted. The tag is the complete backup; the essence doc is the behavioral spec the Go rewrite builds from. Nothing from v1 is imported, wrapped, or kept running.
1. **Language: Go, not TypeScript.** Go matches the user's stated preference, `go build` produces one executable per platform (verified locally, including cross-compilation), the worker lock maps to `Flock`, SQLite maps to a CGo-free driver, and sherpa-onnx documents a first-class Go API for Kokoro. TypeScript via Bun single-file executables is viable in principle (inspected), but it loses on fit: the inspected sherpa Go API has no inspected TypeScript counterpart, Bun bundles a runtime while the verified Go output is a small static binary, and Go already covers the systems needs (Flock, CGo-free SQLite). Rejected.
2. **TTS backend: Kokoro via sherpa-onnx.** Kokoro weights are Apache licensed, the documented English model matches the 24kHz pipeline, and the Go API exposes exactly the knobs TurnEcho needs (voice by speaker id, speed). Qwen3-TTS was evaluated at the user's request and rejected for now: its official path is Python plus GPU-oriented dependencies with much larger models, which contradicts the binary goal. The decision can be revisited if listening tests demand higher quality.
3. **sherpa integration: native Go package first, CLI-subprocess fallback.** The primary path calls the sherpa-onnx Go package in-process so the worker keeps one model loaded across jobs and chunks, which is what long-form buddy speech needs. If the Step 4 spike shows the native link/loader setup is too painful to ship reliably, the fallback is shelling out to a sherpa CLI binary per chunk. The spike gate below makes this call with measurements, not opinions.
4. **Speech pipeline: sentence chunks, sequential playback.** Long answers are split into sentence-ish chunks (bounded length), each chunk is synthesized, and chunks play back to back through an OS-native player. This bounds time-to-first-audio and keeps memory flat regardless of answer length. The queue keeps one job per turn; chunking happens inside the worker, not in the database schema.
5. **Playback: OS-native player subprocess, no audio library.** macOS uses `afplay` (verified present); Linux probes for a player starting with `aplay` and reports a clear `doctor` error when none is found. No CGo audio dependency, no bundled player binary in v2.
6. **CLI: Cobra command tree plus a Huh interactive layer.** Scripting keeps plain subcommands with `--json` output; interactive use gets a `setup` wizard plus value-less `config set` pickers, all with spoken previews, and a bare-`turnecho` menu on TTYs. Bubble Tea stays reserved for a future live view. Full design in `docs/cli-design.md`.
7. **Releases and distribution: GoReleaser on tags, install.sh primary, Homebrew tap secondary, self-upgrade.** Each tag produces per-platform archives with checksums. A hand-written `install.sh` downloads the matching asset, verifies checksums, and execs `turnecho install`, which downloads the pinned sherpa library and Kokoro model tarballs with SHA256 verification into a versioned runtime directory. A self-hosted tap installs the binary only and points at `turnecho install`. `turnecho upgrade` self-updates install.sh users and refuses on brew-managed binaries.
8. **Fresh state, no v1 loader.** Config and database schemas start fresh for v2 with no code that reads v1 files. Same conventional paths may be reused (`~/.config/turnecho/`, `~/.local/share/turnecho/`), and any stale v1 files there are simply ignored or overwritten with a notice.

## Recommended Approach

Archive, document the essence, wipe the tree to scaffolding only, then build the Go product in one static-ish binary per platform. The only sidecar downloads are the pinned sherpa native library and the Kokoro model tarball, both fetched once by `turnecho install`. Hooks depend on no interpreter: `hooks.json` executes the installed binary directly (keeping the `|| printf '{}\n'` guard), and `run_hook.sh` is deleted.

Architecture:

```text
Codex / Claude Code hooks (JSON over stdio)
        |  prompt            |  stop
        v                    v
+--------------------------------------------------+
| turnecho (one Go binary)                         |
|                                                  |
|  hook prompt ----> instruction output             |
|  hook stop ------> validate --> queue job ------+|
|                                                  |
|  worker (single, flock)                         ||
|    claim job --> chunk text --> [sherpa Kokoro] ||
|                                        |        ||
|                                     wav chunks   ||
|                                        v        ||
|                              OS player (afplay/  ||
|                              aplay probe)        ||
|                                                  |
|  config / voices / models / doctor / test / say  |
|  install / upgrade / uninstall / setup wizard    |
+--------------------------------------------------+
        |                    |
  ~/.config/turnecho/   ~/.local/share/turnecho/
  config.json (v2)      runtimes/<v>/  (sherpa lib
  turnecho.db (WAL)      + Kokoro model, pinned)
  worker.lock, .log     ~/.local/bin/turnecho
```

Runtime flow (Stop hook to speech):

```text
agent answer + summary marker
        |
        v
turnecho hook stop (stdin JSON)
  parse host payload, load config, extract speakable text
        |
        v
SQLite: INSERT ... ON CONFLICT DO NOTHING   (dedupe by host/session/turn)
        |
        v
spawn worker (detached, best effort) ---> print host output, exit fast
        |
        v
worker: flock, requeue abandoned, claim oldest pending
        |
        v
split text into sentence chunks
        |
        v
for each chunk: sherpa synthesize (model stays loaded) --> play wav
        |
        v
mark job success/failed, next job or idle-exit after timeout
```

Install flow:

```text
user runs: turnecho install [--host codex|claude|all]
        |
        v
resolve platform (darwin-arm64, darwin-amd64, linux-amd64)
        |
        v
download pinned sherpa native lib + Kokoro model tarballs
verify SHA256 of each, extract into runtimes/<v>/
        |
        v
preflight: load model, synthesize one phrase to wav, probe player
        |
        v
register marketplace + plugin per host,
link ~/.local/bin/turnecho, print next steps
(on any failure: rollback link/marketplace changes, keep old runtime)
```

Buddy-mode long-form pipeline:

```text
full agent answer (Markdown, code, lists)
        |
        v
sanitizer: strip code blocks/URLs/paths, flatten lists,
           expand simple structure into spoken sentences
        |
        v
sentence chunker (bounded chars per chunk, never mid-word)
        |
        v
synthesize chunk N+1 while chunk N plays (overlap when possible,
single voice stream, gapless order guaranteed)
        |
        v
`turnecho stop` kills current playback + drops remaining chunks
```

Repository layout for the Go tree:

```text
cmd/turnecho/        main, Cobra root (config, voices, models, enable,
                     disable, doctor, test, say, stop, install,
                     upgrade, uninstall, setup, hook, worker)
internal/
  cli/               command tree, flags, --json printers, exit codes
  interactive/       Huh wizard, pickers, bare-root menu, TTY guard
  config/            schema load/validate/write (atomic)
  queue/             modernc sqlite, migrations, claim/requeue/update
  hosts/             codex + claude payload parsing, output envelopes
  speak/             summary extraction, sanitizer, sentence chunker
  tts/               sherpa backend interface + native impl (+ CLI fallback)
  player/            afplay/aplay probe chain, sequential playback
  worker/            lock, poll loop, chunk pipeline, logging
  install/           install/upgrade/uninstall, downloads+verify,
                     marketplace calls, link management, rollback
  version/           version string and release metadata
hooks/hooks.json     exec installed binary directly
configs/voices.yaml  Kokoro speaker id table (pinned per model release)
.goreleaser.yaml     per-platform archives + checksums
install.sh           primary installer (downloads release, runs install)
docs/v1-behavior.md  archived essence of the deleted Python implementation
docs/cli-design.md   CLI and distribution design (this decision set)
```

## Work Plan

Step 0, archive the Python tree. Commit any pending work, then tag the tip as `v1-python-final` and push the tag, and create the `archive/python-v1` branch from the same tip. From this point on, the full Python implementation is recoverable without keeping a single Python file in the working tree.

Step 1, write the essence document. Capture the core v1 behaviors in `docs/v1-behavior.md`, verified against the code before anything is deleted: hook stdin/stdout contracts and timeouts per host, the summary-marker format and validation rules, queue semantics (dedupe key, claim, requeue, statuses), worker semantics (lock, poll, idle exit, failure recording), config keys and validation ranges, the CLI command surface with exit codes, and installer behaviors (runtime layout, link management, rollback rules, uninstall scope). Review it once against the sources, then treat it as the behavioral spec for the rewrite.

Step 2, wipe the tree. Delete `src/`, `tests/`, `scripts/*.py`, `pyproject.toml`, `uv.lock`, `.python-version`, `.venv/`, `dist/`, `.ruff_cache/`, all `__pycache__/`, and `hooks/run_hook.sh`. Keep plugin scaffolding (`.codex-plugin/`, `.claude-plugin/`, `skills/`, `assets/`, `.agents/plugins/marketplace.json`), `hooks/hooks.json` (content rewritten later), docs (`README.md`, `CHANGELOG.md`, `LICENSE`, `AGENTS.md`, `.agents/plans/`), and `.github/workflows/ci.yml` (content rewritten later). Rewrite `.gitignore` for Go and drop the Python entries. Verify: no `.py` file remains, no `pyproject.toml`/`uv.lock`/`.venv`, and `git status` shows only the intended deletions and the new essence doc.

Step 3, scaffold the Go module. Create `go.mod`, the `cmd/turnecho` plus `internal/` layout above, a `Makefile` with Go targets (`build`, `test`, `vet`, `fmt`), a starter `.goreleaser.yaml`, and a rewritten CI workflow that runs `go build`, `go vet`, and `go test` on macOS and Linux. Land a `main` that prints the version and exits, proving the toolchain end to end. Depends on: Step 2.

Step 4, spike the sherpa Go integration (time-boxed, 1 to 2 days). In a scratch directory (not the product tree), load the pinned Kokoro model through the sherpa-onnx Go package on macOS arm64, synthesize one short and one long paragraph, and report: exact link/loader setup, cold-load seconds, per-chunk synth seconds, peak memory, and the smallest shippable file set. Decision gate: native Go package (primary) or CLI-subprocess fallback. No product code lands in this step. Depends on: Step 3.

Step 5, Go core (no TTS yet). Implement config (fresh v2 schema, strict validation, atomic writes), queue (modernc, migrations, same atomic claim and dedupe key), hosts (Codex/Claude parsing and envelopes), hooks (`hook prompt`, `hook stop` with the v1 stdout/stderr contracts from the essence doc), and the worker skeleton (flock, requeue, poll, idle exit) with a fake TTS backend that writes silent wavs. Implement the CLI surface: `config show|path|set|reset`, `enable`, `disable`, `voices`, `models`, `doctor`, `test`, plus new `say` (synthesize to file or speaker) and `stop` (silence current speech). Depends on: Step 4 decision. Produces: `go build ./...`, `go test ./...` green.

Step 6, speech integration. Implement the chosen sherpa backend (native package or CLI fallback), wire voice-by-speaker-id and speed from config, implement the sentence chunker and sequential player chain (`afplay` on macOS, probed `aplay` first on Linux), and make `doctor` plus `test` exercise the real path. Depends on: Step 5. Produces: end-to-end spoken summaries on macOS.

Step 7, installer and rich CLI. Implement `turnecho install/uninstall` (pinned downloads with SHA256, preflight, marketplace registration, atomic link management, rollback) and `turnecho upgrade` (release check, verified swap-replace, runtime refresh, brew-managed refusal), add the Huh setup wizard, value-less `config set` pickers with spoken previews, and the bare-root TTY menu per `docs/cli-design.md`, add shell completion, rewrite `hooks/hooks.json` to call the binary directly, and update the config skill for the new CLI. Depends on: Step 6. Produces: install from a release archive with no Python on the machine.

Step 8, buddy mode. Add full-answer speech behind config (`speak: summary|answer`), the Markdown-to-speech sanitizer, chunk-overlap playback, barge-in via `turnecho stop`, and a prompt instruction that asks for speakable structure. Keep summary-marker mode as the default until listening tests pass. Depends on: Step 7. Produces: the peer-programming voice experience.

Step 9, release. Finish `.goreleaser.yaml` (archives, checksums, tap formula), write `install.sh`, extend CI with per-platform install smoke (assert real wav bytes from `say --output`, never speakers), rewrite `README.md`, `AGENTS.md`, and `CHANGELOG.md` for v2, and cut the v2.0.0 tag with release notes. Depends on: Step 8. Produces: shippable v2.

## Validation Plan

- Step 0: `git tag` lists `v1-python-final` and the archive branch exists at the same tip; a fresh clone plus checkout of the tag restores the Python tree.
- Step 1: every behavior in `docs/v1-behavior.md` names the source file it was taken from; a second read-through confirms no section is written from memory.
- Step 2: `find . -name '*.py'` is empty (outside `.git`), `pyproject.toml`, `uv.lock`, `.venv`, and `dist` are gone, and the keep list above is intact.
- Step 3: `go build ./...`, `go vet ./...`, and `gofmt -l .` (empty) pass on macOS and Linux CI; the binary prints its version.
- Step 4: spike report with measured cold-load, per-chunk synth, and memory numbers on macOS arm64, plus the exact loader setup or a documented fallback decision. Manual listening check of one long paragraph.
- Step 5: `go test ./...` green; hook contract tests feeding recorded Codex/Claude payloads asserting exact stdout (`{}` or the required envelope) and empty stderr; worker tests for lock contention, atomic claim, and requeue; CLI golden tests for `--json` outputs.
- Step 6: `turnecho say "phrase" --output out.wav` produces a valid 24kHz wav (byte-asserted, headless-safe); `turnecho test` speaks aloud on a dev machine (manual); `turnecho doctor` fails clearly with the runtime missing and passes with it present.
- Step 7: from a clean `HOME`, `turnecho install` then `turnecho doctor` passes with no Python involved (assert by hiding `python3` from `PATH`); uninstall removes link and marketplace entries and leaves user data; setup wizard and pickers walked manually once per platform; `upgrade` refuses on brew-managed paths (unit-tested) and `--check`/`--dry-run` change nothing.
- Step 8: sanitizer unit tests over Markdown fixtures (code, tables, paths, URLs); long-answer fixture speaks start to finish in order (manual); `turnecho stop` silences within one chunk (manual).
- Step 9: CI matrix (macOS arm64, Linux x64, plus macOS amd64 if kept) runs unit tests, builds release binaries, and runs the install smoke with wav assertions.
- Highest-risk validation: the Step 4 spike. If native linking cannot be packaged reliably, the fallback keeps the plan alive but changes latency characteristics, so the gate decision must be explicit and dated.

## Risks / Rollback

- Lost v1 knowledge during the wipe (risk: a behavior is forgotten once the code is gone). Mitigation: tag plus archive branch keep every line recoverable, and the essence doc is written and reviewed before any deletion.
- Native link/loader packaging (risk: sherpa native usage expects a shared-library directory on the loader path; the exact Go-package mechanics were not fully inspectable and must be proven in the spike). Mitigation: time-boxed spike with a CLI-subprocess fallback already designed.
- Per-chunk latency on long answers (risk: buddy mode feels sluggish if cold load or per-chunk synth is slow). Mitigation: model stays loaded in the worker, chunk overlap in Step 8, and a smaller model variant if the pinned release offers one.
- Downloaded-binary trust on macOS (risk: Gatekeeper friction for the sherpa library and model downloads; exact behavior unconfirmed). Mitigation: verify in the spike, prefer documented install paths, keep checksum verification regardless.
- Linux player availability (risk: `aplay` presence varies by install; only its documented behavior was inspected, not default installation). Mitigation: runtime probe chain with a clear `doctor` message, CI smoke on stock Ubuntu.
- Rollback: before Step 2 completes, recovery is the tag plus archive branch. After the wipe, there is no v1 runtime to fall back to, which is accepted: there are no users, and the tag restores v1 on demand.

## Open Questions

1. Kokoro voice lineup: which speaker ids ship as the named voice list, and what is the default? (Default if unanswered: the English sid table from the pinned model release, default to be picked by listening in Step 6.)
2. Should macOS Intel (darwin-amd64) be a supported release target from day one? (Default if unanswered: yes, it costs one more GoReleaser target, dropped only if the spike shows sherpa gaps there.)
3. Buddy-mode default: summary-only until listening tests pass, or full-answer from the first v2 release? (Default if unanswered: summary-only default with `speak: answer` opt-in.)

## Sources

- https://go.dev/doc/tutorial/compile-install
- https://github.com/k2-fsa/sherpa-onnx
- https://k2-fsa.github.io/sherpa/onnx/tts/all/English/kokoro-en-v0_19.html
- https://github.com/k2-fsa/sherpa-onnx-go
- https://github.com/hexgrad/kokoro
- https://github.com/QwenLM/Qwen3-TTS
- https://pkg.go.dev/modernc.org/sqlite
- https://github.com/spf13/cobra
- https://github.com/charmbracelet/bubbletea
- https://bun.sh/docs/bundler/executables
- https://raw.githubusercontent.com/goreleaser/goreleaser/main/README.md
- https://pkg.go.dev/golang.org/x/sys/unix
- https://manpages.ubuntu.com/manpages/noble/en/man1/aplay.1.html
