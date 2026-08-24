---
name: turnecho-config
description: Inspect and change TurnEcho configuration through its CLI. Use when the user asks to show, enable, disable, reset, or change TurnEcho settings such as model, voice, or speech speed. Do not use for normal TurnEcho summary generation, playback, or implementation work.
---

# Configure TurnEcho

TurnEcho is a local Codex plugin that speaks a short summary after an agent
turn. Use this skill only to inspect or change its user configuration. Normal
TurnEcho operation does not require this skill.

## Use the CLI

Use the installed `turnecho` command as the only configuration interface. Do
not edit TurnEcho's configuration file or SQLite database directly.

Run the operation the user requested. An inspection request does not authorize
a configuration change. When a requested change is unambiguous, run it
directly instead of adding an unnecessary read first.

Supported configuration commands are:

```sh
turnecho config show
turnecho config show --json
turnecho config path
turnecho config set model <model>
turnecho config set voice <voice>
turnecho config set speed <speed>
turnecho config reset enabled
turnecho config reset model
turnecho config reset voice
turnecho config reset speed
turnecho config reset --all
turnecho enable
turnecho disable
turnecho models
turnecho models --json
turnecho voices
turnecho voices --json
```

Use `turnecho models --json` or `turnecho voices --json` when the user asks
which values are available or when a valid value must be discovered. Let the
CLI validate names, numeric ranges, and stored configuration.

Treat the command output as the source of truth. Report the resulting state or
the practical error. If the command is unavailable or rejects the request, do
not bypass it by editing local files.

`turnecho doctor` performs runtime checks, and `turnecho test` plays audio.
They are not configuration commands. Run either only when the user explicitly
asks for that operation.
