# Changelog

## 1.0.0 - 2026-09-18

- Initial release. TurnEcho speaks a short validated summary at the end of
  every Codex and Claude Code turn while leaving the full response on screen.
- Supports Codex and Claude Code on macOS and Linux through per-host hook
  adapters over a shared core.
- Queues validated summaries in local SQLite and plays them in order through
  one detached KittenTTS worker.
- Installs through a dependency-free `turnecho-install` plus a versioned
  audio runtime built from the release lockfile; failures roll every host
  back.
- Configures model, voice, speech speed, and enabled state through the local
  `turnecho` command.
