#!/usr/bin/env python3
"""Rewrite a local plugin version to a single Codex cachebuster suffix."""

from __future__ import annotations

import argparse
import json
import re
import sys
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

CACHEBUSTER_PREFIX = "codex"


def sanitize_cachebuster(value: str) -> str:
    """Normalize a cachebuster to the format accepted by Codex versions."""
    sanitized = re.sub(r"[^a-z0-9-]+", "-", value.strip().lower())
    sanitized = re.sub(r"-{2,}", "-", sanitized).strip("-")
    if not sanitized:
        raise ValueError("Cachebuster must contain at least one letter or digit.")
    return sanitized


def default_cachebuster() -> str:
    """Return a UTC timestamp suitable for a local update cachebuster."""
    return datetime.now(timezone.utc).strftime("%Y%m%d%H%M%S")


def with_cachebuster(version: str, cachebuster: str) -> str:
    """Preserve the base version and replace any existing cachebuster."""
    version_prefix = version.split("+", 1)[0]
    return f"{version_prefix}+{CACHEBUSTER_PREFIX}.{cachebuster}"


def update_plugin_cachebuster(
    plugin_root: Path,
    *,
    cachebuster: str | None = None,
) -> tuple[str, str]:
    """Update a plugin manifest and return its old and new versions."""
    manifest_path = plugin_root / ".codex-plugin" / "plugin.json"
    if not manifest_path.is_file():
        raise FileNotFoundError(f"missing manifest: {manifest_path}")

    manifest: Any = json.loads(manifest_path.read_text(encoding="utf-8"))
    if not isinstance(manifest, dict):
        raise ValueError(f"{manifest_path} must contain a JSON object.")

    version = manifest.get("version")
    if not isinstance(version, str) or not version.strip():
        raise ValueError(f"{manifest_path} must contain a non-empty string 'version'.")

    next_cachebuster = sanitize_cachebuster(cachebuster or default_cachebuster())
    next_version = with_cachebuster(version, next_cachebuster)
    manifest["version"] = next_version
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    return version, next_version


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Rewrite a local plugin's version so it preserves everything before '+' "
            "and uses a single +codex.<cachebuster> suffix."
        )
    )
    parser.add_argument("plugin_path", help="Path to the plugin root directory")
    parser.add_argument(
        "--cachebuster",
        help="Optional cachebuster token to embed in the plugin version",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    plugin_root = Path(args.plugin_path).expanduser().resolve()
    old_version, new_version = update_plugin_cachebuster(
        plugin_root,
        cachebuster=args.cachebuster,
    )
    print(f"Updated plugin version: {old_version} -> {new_version}")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as error:  # noqa: BLE001 - CLI should surface one clear message.
        print(error, file=sys.stderr)
        raise SystemExit(1) from error
