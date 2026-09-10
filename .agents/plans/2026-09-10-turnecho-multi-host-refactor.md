## Goal

Refactor TurnEcho so Codex-specific and Claude Code-specific code live in thin per-host adapters over one shared host-independent core, keeping high-level behavior identical on both hosts: inject the summary instruction on prompt submit, speak only a validated trailing summary marker on stop, queue through SQLite deduped by `(host, session_id, turn_id)`, and play sequentially through the single locked worker.

## Success Criteria

- `src/turnecho/hosts/` owns every host-specific shape (hook input parsing, hook output envelopes, manifest/env-var names); core modules (`config`, `sqlite`, `worker`, `schema`, summary validation) never import from `hosts/`.
- Identical user-visible behavior on both hosts: same marker contract, same fail-safe empty-JSON stdout, same voice/speed snapshotting, same deduplication semantics.
- Claude Code `Stop` produces a stable `turn_id` without a schema migration.
- `uv run --no-dev python -m unittest discover -s tests` and `make check` pass; a manual Claude Code hook firing queues a `host=claude_code` job and speaks exactly once.

## Context And Current Facts

- Current pipeline: [prompt_hook.py](/Users/rmscoal/Developer/turnecho/src/turnecho/prompt_hook.py) injects the summary instruction via `hookSpecificOutput.additionalContext`; [stop_hook.py](/Users/rmscoal/Developer/turnecho/src/turnecho/stop_hook.py) validates the trailing `<!-- turnecho-summary:v1 ... -->` marker in `last_assistant_message`, calls `insert_job_db(host="codex", ...)`, and spawns the detached worker; [worker.py](/Users/rmscoal/Developer/turnecho/src/turnecho/worker.py) holds one cross-process `fcntl` lock and plays jobs sequentially.
- The queue is already multi-host shaped: [001_create_schema.sql](/Users/rmscoal/Developer/turnecho/src/turnecho/migrations/001_create_schema.sql) has a `host` column with `UNIQUE(host, session_id, turn_id)`, and [constant.py](/Users/rmscoal/Developer/turnecho/src/turnecho/constant.py) already defines `TurnEchoHostSource` with `CODEX`, `CLAUDE_CODE`, and `OPENCODE`.
- The Codex hook contract is established by this repo (public Codex docs only describe plugin packaging, not hook payloads): stdin JSON with `hook_event_name`, `session_id`, `turn_id`, `last_assistant_message` / `prompt`, `stop_hook_active`; stdout `{}` by default, verified in [test_stop_hook.py](/Users/rmscoal/Developer/turnecho/tests/test_stop_hook.py) and [test_user_prompt_submit_hook.py](/Users/rmscoal/Developer/turnecho/tests/test_user_prompt_submit_hook.py).
- Claude Code plugin contract from its docs: manifest at `.claude-plugin/plugin.json`; plugin hooks live at `hooks/hooks.json` in the plugin root using the same format as settings hooks; command hooks get event JSON on stdin; common input fields are `session_id`, `transcript_path`, `cwd`, `permission_mode`, `hook_event_name`; `Stop` adds `stop_hook_active` and `last_assistant_message` (the transcript file may lag, so the field, not the file, carries the final message); `UserPromptSubmit` returns guidance via `hookSpecificOutput.additionalContext`; plugin scripts resolve paths through `${CLAUDE_PLUGIN_ROOT}` / the `CLAUDE_PLUGIN_ROOT` env var.
- Codex plugin contract from its docs: supported layout uses a `.codex-plugin/plugin.json` compatibility manifest with optional `skills/`, `hooks/`, `scripts/`, `assets/` directories; a portable root-`plugin.json` package is the alternative; ChatGPT and Codex share one plugin directory.
- Structural mismatch driving this plan: Claude Code `Stop` input has **no `turn_id`**, so the Claude adapter must synthesize one to preserve the existing `UNIQUE(host, session_id, turn_id)` key without a migration. Codex uses `$PLUGIN_ROOT`; Claude Code uses `$CLAUDE_PLUGIN_ROOT`, so the shared shell launcher must read both.
- Codex-only surfaces today: [.codex-plugin/plugin.json](/Users/rmscoal/Developer/turnecho/.codex-plugin/plugin.json), [hooks/hooks.json](/Users/rmscoal/Developer/turnecho/hooks/hooks.json) plus [run_hook.sh](/Users/rmscoal/Developer/turnecho/hooks/run_hook.sh) (`$PLUGIN_ROOT` only), `CODEX_*` names in `constant.py`, `host="codex"` hardcoded in `stop_hook.py`, Codex marketplace paths in [install_plugin.py](/Users/rmscoal/Developer/turnecho/src/turnecho/install_plugin.py) and [scripts/install_local_plugin.py](/Users/rmscoal/Developer/turnecho/scripts/install_local_plugin.py), and Codex-specific wording in [SKILL.md](/Users/rmscoal/Developer/turnecho/skills/turnecho-config/SKILL.md).

## Constraints And Non-goals

- Standing engineering rules still bind: hooks stay fast and dependency-free; hook stdout stays valid JSON (`{}` default); diagnostics go to stderr/worker log; queue claims stay atomic; SQL stays parameterized; TTS/audio imports stay inside the worker path; playback stays sequential; `fcntl` stays macOS/Linux-only; Python 3.13+, Ruff config in `pyproject.toml`.
- Non-goals for this refactor: worker/TTS/playback changes, queue semantic changes, a Claude Code marketplace installer (follow-up), OpenCode support (enum value already reserved), graphical configuration.

## Key Decisions

- **One plugin root, dual manifests.** Ship `.codex-plugin/plugin.json` and a new `.claude-plugin/plugin.json` in the same root with the existing shared `hooks/hooks.json`, `skills/`, and `src/`. Both hosts auto-discover the same relative paths (`hooks/hooks.json` is the documented location on both sides), so one checkout installs as either plugin. Rejected: separate per-host plugin directories, which would duplicate packaging, versioning, and the skill for no behavioral gain.
- **Payload-dispatched hook entries, explicit override.** Keep the existing `run_hook.sh prompt|stop` commands and let the entry modules detect the host from disjoint payload keys (`transcript_path` present means Claude Code, `turn_id` present means Codex, anything else fails safe to `{}`), with an explicit `--host` flag for tests and manual runs. Rejected: dual command entries inside one `hooks.json`, because both hosts would execute both commands on every event.
- **Claude Code `turn_id` is synthesized, not migrated.** `turn_id = f"turn-{assistant_count}-{sha1(last_message)[:12]}"` from a tail read of `transcript_path`, falling back to a random UUID when the transcript is missing or unreadable. This preserves the `UNIQUE(host, session_id, turn_id)` dedup key with no migration; the message hash guards against transcript lag causing two different summaries to share one turn id. Rejected: nullable `turn_id` plus message-hash dedup, which would need a migration and change queue semantics.
- **Host names move out of `constant.py`.** `constant.py` keeps only host-independent constants; `CODEX_*` moves to `hosts/codex.py`, new `CLAUDE_*` names live in `hosts/claude.py`, and `constant.py` keeps temporary re-exports so existing imports and tests keep working during the transition.
- **One shared skill file, host-neutral wording.** `skills/turnecho-config/SKILL.md` is auto-discovered by both hosts from the same path, so reword it to cover both instead of forking it.

## Recommended Approach

Target layout (new files marked with `+`, moved logic marked with `~`):

```text
src/turnecho/
  config.py            # core, unchanged (host-independent)
  sqlite.py            # core, unchanged
  worker.py            # core, unchanged
  schema.py            # core, unchanged
  constant.py          # ~ core-only constants + temporary host re-exports
  summary.py           # + moved verbatim: extract_turnecho_summary_from_agent_message
  stop_hook.py         # ~ thin Codex-compatible entry: parse stdin, dispatch by payload
  prompt_hook.py       # ~ thin Codex-compatible entry: parse stdin, dispatch by payload
  hosts/
    __init__.py
    types.py           # + HostSource (moved), TurnEchoEvent(host, session_id,
                       #   turn_id, message, stop_hook_active)
    codex.py           # + CODEX_* names, parse Codex Stop/UserPromptSubmit,
                       #   render Codex outputs
    claude.py          # + CLAUDE_* names, parse Claude payloads, transcript
                       #   turn-id synthesis, render Claude outputs
.claude-plugin/plugin.json  # + Claude manifest, version aligned with the rest
hooks/hooks.json              # ~ same events, commands tolerate both env vars
hooks/run_hook.sh             # ~ plugin_root="${CLAUDE_PLUGIN_ROOT:-${PLUGIN_ROOT:-}}"
skills/turnecho-config/SKILL.md  # ~ host-neutral wording
```

Data flow stays identical on both hosts: host adapter parses stdin into `TurnEchoEvent`, core validates config plus summary marker and inserts the job with `host` set to `codex` or `claude_code`, then the shared worker path runs unchanged.

## Work Plan

- **Phase 1: extract core, add host seam (no behavior change).** Move `extract_turnecho_summary_from_agent_message` to `summary.py`; add `hosts/types.py` (`HostSource`, `TurnEchoEvent`); move `CODEX_*` names to `hosts/codex.py` with re-exports from `constant.py`; make `stop_hook.py` / `prompt_hook.py` delegate Codex parsing to `hosts/codex.py` and core validation/insertion to shared helpers. Tests: existing `test_stop_hook.py` / `test_user_prompt_submit_hook.py` stay green untouched wherever possible; add `tests/test_summary.py` for the moved parser.
- **Phase 2: Claude Code adapter.** Implement `hosts/claude.py`: `Stop` guard (`hook_event_name`, non-empty `session_id` / `last_assistant_message`, not `stop_hook_active`), transcript tail-read turn counting plus message-hash `turn_id` with UUID fallback, `UserPromptSubmit` instruction injection through the same `hookSpecificOutput.additionalContext` envelope. Tests: `tests/test_claude_hooks.py` covering valid Stop queuing with `host="claude_code"`, missing/invalid summary, `stop_hook_active`, disabled/invalid config, unreadable-transcript fallback, and prompt-submit output shape, using `TemporaryDirectory` transcript fixtures.
- **Phase 3: dual packaging.** Add `.claude-plugin/plugin.json` (same name/version/description family, host-neutral text); extend `run_hook.sh` and `hooks/hooks.json` to the `${CLAUDE_PLUGIN_ROOT:-$PLUGIN_ROOT}` fallback; reword `SKILL.md` host-neutrally; extend `test_plugin_manifest.py` version alignment to both manifests; update hook launcher tests to cover a `CLAUDE_PLUGIN_ROOT`-only environment.
- **Phase 4: docs and release notes.** Update `README.md`, `AGENTS.md` behavior bullets (multi-host support, Claude `turn_id` synthesis rule), and `CHANGELOG.md`. No code changes in this phase.

## Validation Plan

- `uv run --no-dev python -m unittest discover -s tests` (full suite, must be green).
- `make check` (lint gate).
- Targeted: new `tests/test_claude_hooks.py` plus existing stop/prompt/manifest/launcher tests.
- Manual Claude Code check: install the checkout with `claude --plugin-dir .`, submit a prompt (expect the summary instruction in context), get a response ending in a valid marker (expect one queued `claude_code` row and one spoken summary), then repeat the turn (expect dedup, no double speech).
- Manual Codex regression: `run_hook.sh prompt|stop` with the existing fixture payloads still emit the exact current outputs.
- Highest-risk validation is the Claude Code manual check, because `turn_id` synthesis and the `transcript_path`/`prompt` payload shapes are the only contract points grounded in docs rather than in this repo's tests.

## Risks / Rollback

- Claude Code payload drift (for example a future `turn_id` or renamed field) could misroute the payload dispatcher. Mitigation: dispatcher checks `transcript_path` first, explicit `--host` override exists, unknown payloads fail safe to `{}`; monitor after each Claude Code update.
- Transcript lag or rotation could weaken turn counting. Mitigation: the message hash in the synthesized `turn_id` keeps distinct summaries distinct; worst case is a repeated identical summary deduping, which matches existing duplicate-turn behavior.
- One shared `hooks.json` means a syntax error breaks both hosts. Mitigation: launcher tests assert both commands, and both manual checks run before release.
- Rollback is per-phase: Phases 1-2 are additive behind existing entries; Phase 3 packaging changes revert by deleting `.claude-plugin/` and restoring the two launcher lines. No migration ships, so there is no database rollback.

## Open Questions

None.

## Sources

- https://code.claude.com/docs/en/plugins
- https://code.claude.com/docs/en/hooks
- https://code.claude.com/docs/en/plugins-reference
- https://learn.chatgpt.com/docs/build-plugins
- https://developers.openai.com/plugins/
- https://developers.openai.com/plugins/build/plugins.md
