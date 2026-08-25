# Changelog

## 0.2.3 - 2026-08-25

- Build managed virtual environments at their permanent versioned path so
  generated command launchers never retain a deleted installation path.
- Execute the final managed `turnecho` command before reporting installation
  success, and restore the previous same-version runtime if validation fails.
- Use the same permanent runtime for GitHub and local-checkout installations.
  Hook launchers now execute that runtime's Python directly without invoking
  `uv` on every Codex hook.

## 0.2.2 - 2026-08-25

- Store versioned, non-editable Python runtimes outside the Codex-managed
  plugin cache so rematerializing a plugin does not delete its environment.
- Run hooks through a guarded launcher that points `uv` at the stable runtime,
  preventing fallback to the current workspace when a plugin root is missing.
- Return valid empty JSON when the bound plugin source or runtime is missing.
- Prepare same-version repairs atomically and restore the previous runtime and
  managed command when an update fails.
- Add an official GitHub uninstall path that removes marked runtimes and the
  managed command without changing unrelated files.
- Document new-task update boundaries, repair behavior, stable runtime storage,
  troubleshooting, and external runtime cleanup.

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
