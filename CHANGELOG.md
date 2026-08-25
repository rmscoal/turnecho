# Changelog

## 0.2.1 - 2026-08-25

- Rebuild and verify the previous cached runtime when an update rollback
  restores an earlier plugin release.
- Restore the managed `turnecho` command as part of update rollback.
- Repair dangling managed command links left by removed Codex cache versions.
- Clarify the supported install, update, repair, and uninstall lifecycle.
- Keep GitHub installer release metadata in shared constants.

## 0.2.0 - 2026-08-24

- Added the dependency-free `turnecho` CLI for inspecting and changing local
  settings.
- Added configurable KittenTTS models, with `mini` as the default and `micro`
  and `nano` as alternatives.
- Added model-aware runtime checks, audio tests, and worker model reloading.
- Added the `turnecho-config` skill for guided CLI configuration through Codex.
- Kept configuration schema version 1 because the model setting was added
  before this release.
