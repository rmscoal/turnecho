#!/usr/bin/env python3
"""Install the current TurnEcho checkout into Codex's personal marketplace."""

from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Any

from update_plugin_cachebuster import update_plugin_cachebuster

DEFAULT_MARKETPLACE_PATH = Path.home() / ".agents" / "plugins" / "marketplace.json"
DEFAULT_PLUGIN_PARENT = Path.home() / "plugins"
DEFAULT_MARKETPLACE_NAME = "personal"
DEFAULT_CATEGORY = "Productivity"


class InstallError(RuntimeError):
    """Raised when the local plugin installation cannot be completed safely."""


def load_plugin_metadata(plugin_root: Path) -> tuple[str, str]:
    """Load and validate the plugin name and marketplace category."""
    manifest_path = plugin_root / ".codex-plugin" / "plugin.json"
    if not manifest_path.is_file():
        raise InstallError(f"Plugin manifest not found: {manifest_path}")

    try:
        manifest: Any = json.loads(manifest_path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as error:
        raise InstallError(f"Invalid plugin manifest: {manifest_path}") from error

    if not isinstance(manifest, dict):
        raise InstallError(f"Plugin manifest must be a JSON object: {manifest_path}")

    plugin_name = manifest.get("name")
    if not isinstance(plugin_name, str) or not re.fullmatch(
        r"[a-z0-9]+(?:-[a-z0-9]+)*", plugin_name
    ):
        raise InstallError(
            f"Plugin manifest name must use lower-case hyphen-case: {manifest_path}"
        )

    interface = manifest.get("interface", {})
    if not isinstance(interface, dict):
        raise InstallError(
            f"Plugin manifest interface must be an object: {manifest_path}"
        )

    category = interface.get("category", DEFAULT_CATEGORY)
    if not isinstance(category, str) or not category.strip():
        category = DEFAULT_CATEGORY

    return plugin_name, category


def build_marketplace_entry(plugin_name: str, category: str) -> dict[str, Any]:
    """Build the personal-marketplace entry for a local plugin source."""
    return {
        "name": plugin_name,
        "source": {
            "source": "local",
            "path": f"./plugins/{plugin_name}",
        },
        "policy": {
            "installation": "AVAILABLE",
            "authentication": "ON_INSTALL",
        },
        "category": category,
    }


def load_or_create_marketplace(path: Path) -> tuple[dict[str, Any], str]:
    """Load a marketplace or return a new personal-marketplace structure."""
    if not path.exists():
        return (
            {
                "name": DEFAULT_MARKETPLACE_NAME,
                "interface": {"displayName": "Personal"},
                "plugins": [],
            },
            DEFAULT_MARKETPLACE_NAME,
        )

    try:
        marketplace: Any = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as error:
        raise InstallError(f"Invalid marketplace JSON: {path}") from error

    if not isinstance(marketplace, dict):
        raise InstallError(f"Marketplace must be a JSON object: {path}")

    marketplace_name = marketplace.get("name")
    if not isinstance(marketplace_name, str) or not marketplace_name.strip():
        raise InstallError(f"Marketplace has no valid name: {path}")

    plugins = marketplace.get("plugins")
    if plugins is None:
        marketplace["plugins"] = []
    elif not isinstance(plugins, list):
        raise InstallError(f"Marketplace plugins must be an array: {path}")

    return marketplace, marketplace_name


def prepare_marketplace_entry(
    marketplace: dict[str, Any],
    plugin_name: str,
    category: str,
    *,
    force: bool,
) -> bool:
    """Add or intentionally replace a marketplace entry in memory."""
    plugins = marketplace["plugins"]
    entry = build_marketplace_entry(plugin_name, category)

    for index, existing_entry in enumerate(plugins):
        if (
            not isinstance(existing_entry, dict)
            or existing_entry.get("name") != plugin_name
        ):
            continue

        if existing_entry == entry:
            return False
        if not force:
            raise InstallError(
                f"Marketplace already contains a different '{plugin_name}' entry. "
                "Use --force only when replacing it is intentional."
            )

        plugins[index] = entry
        return True

    plugins.append(entry)
    return True


def validate_plugin_link(link_path: Path, plugin_root: Path, *, force: bool) -> None:
    """Check that the destination can safely become a symlink to the source."""
    if not link_path.exists() and not link_path.is_symlink():
        return

    if link_path.is_symlink() and link_path.resolve(strict=False) == plugin_root:
        return

    if link_path.is_symlink() and force:
        return

    raise InstallError(
        f"Plugin link already exists and does not point to {plugin_root}: {link_path}. "
        "Move it manually or use --force for a conflicting symlink."
    )


def ensure_plugin_link(link_path: Path, plugin_root: Path, *, force: bool) -> bool:
    """Create the source symlink without replacing a real directory."""
    validate_plugin_link(link_path, plugin_root, force=force)

    if link_path.is_symlink() and link_path.resolve(strict=False) == plugin_root:
        return False

    if link_path.is_symlink():
        link_path.unlink()

    link_path.parent.mkdir(parents=True, exist_ok=True)
    link_path.symlink_to(plugin_root, target_is_directory=True)
    return True


def sync_plugin_dependencies(plugin_root: Path) -> None:
    """Install and validate the audio runtime before changing Codex state."""
    if shutil.which("uv") is None:
        raise InstallError(
            "The 'uv' command was not found. Install uv before installing TurnEcho."
        )

    subprocess.run(
        ["uv", "sync", "--project", str(plugin_root), "--no-dev"],
        check=True,
    )
    subprocess.run(
        [
            "uv",
            "run",
            "--project",
            str(plugin_root),
            "--no-dev",
            "python",
            "-m",
            "turnecho.runtime_preflight",
        ],
        check=True,
    )


def write_json_atomically(path: Path, payload: dict[str, Any]) -> None:
    """Write marketplace JSON without leaving a partially-written file."""
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary_path: str | None = None

    try:
        with tempfile.NamedTemporaryFile(
            "w",
            encoding="utf-8",
            dir=path.parent,
            prefix=f".{path.name}.",
            delete=False,
        ) as temporary_file:
            temporary_path = temporary_file.name
            json.dump(payload, temporary_file, indent=2)
            temporary_file.write("\n")
            temporary_file.flush()
            os.fsync(temporary_file.fileno())

        os.replace(temporary_path, path)
        temporary_path = None
    finally:
        if temporary_path is not None:
            Path(temporary_path).unlink(missing_ok=True)


def write_bytes_atomically(path: Path, payload: bytes) -> None:
    """Restore a file without exposing a partially-written marketplace."""
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary_path: str | None = None

    try:
        with tempfile.NamedTemporaryFile(
            "wb",
            dir=path.parent,
            prefix=f".{path.name}.",
            delete=False,
        ) as temporary_file:
            temporary_path = temporary_file.name
            temporary_file.write(payload)
            temporary_file.flush()
            os.fsync(temporary_file.fileno())

        os.replace(temporary_path, path)
        temporary_path = None
    finally:
        if temporary_path is not None:
            Path(temporary_path).unlink(missing_ok=True)


def restore_file(path: Path, original_content: bytes | None) -> None:
    """Restore a file captured before installation or remove a new file."""
    if original_content is None:
        path.unlink(missing_ok=True)
        return

    write_bytes_atomically(path, original_content)


def restore_plugin_link(link_path: Path, original_target: str | None) -> None:
    """Restore a symlink captured before installation or remove a new link."""
    if link_path.exists() or link_path.is_symlink():
        if not link_path.is_symlink():
            raise InstallError(
                f"Cannot roll back a non-symlink plugin path: {link_path}"
            )
        link_path.unlink()

    if original_target is not None:
        link_path.parent.mkdir(parents=True, exist_ok=True)
        link_path.symlink_to(original_target, target_is_directory=True)


def validate_codex_available() -> None:
    """Check Codex before making any local marketplace changes."""
    if shutil.which("codex") is None:
        raise InstallError(
            "The 'codex' command was not found. Re-run this command from a shell "
            "where Codex is installed, or use --skip-codex."
        )


def run_codex_install(plugin_name: str, marketplace_name: str) -> None:
    """Install the plugin from the configured Codex marketplace."""
    validate_codex_available()

    subprocess.run(
        ["codex", "plugin", "add", f"{plugin_name}@{marketplace_name}"],
        check=True,
    )


def install_plugin(
    plugin_root: Path,
    *,
    marketplace_path: Path = DEFAULT_MARKETPLACE_PATH,
    plugin_link: Path | None = None,
    force: bool = False,
    dry_run: bool = False,
    run_codex: bool = True,
    sync_dependencies: bool = True,
    update: bool = False,
) -> tuple[str, str]:
    """Install or update a checkout in the personal marketplace and Codex."""
    plugin_root = plugin_root.expanduser().resolve()
    plugin_name, category = load_plugin_metadata(plugin_root)
    manifest_path = plugin_root / ".codex-plugin" / "plugin.json"
    marketplace_path = marketplace_path.expanduser().resolve()
    plugin_link = plugin_link or DEFAULT_PLUGIN_PARENT / plugin_name
    plugin_link = plugin_link.expanduser()

    marketplace, marketplace_name = load_or_create_marketplace(marketplace_path)
    marketplace_changed = prepare_marketplace_entry(
        marketplace,
        plugin_name,
        category,
        force=force,
    )
    validate_plugin_link(plugin_link, plugin_root, force=force)

    if update and marketplace_changed:
        raise InstallError(
            "--update requires an existing marketplace entry that points to this "
            "checkout. Run the installer without --update first."
        )

    if dry_run:
        print(f"Would link {plugin_link} -> {plugin_root}")
        if marketplace_changed:
            print(f"Would update marketplace: {marketplace_path}")
        if sync_dependencies:
            print(f"Would run: uv sync --project {plugin_root} --no-dev")
            print(f"Would verify the audio runtime in: {plugin_root}")
        if update:
            print(f"Would run: update_plugin_cachebuster.py {plugin_root}")
        if run_codex:
            print(f"Would run: codex plugin add {plugin_name}@{marketplace_name}")
        return plugin_name, marketplace_name

    if run_codex:
        validate_codex_available()

    original_manifest = manifest_path.read_bytes()
    original_marketplace = (
        marketplace_path.read_bytes() if marketplace_path.is_file() else None
    )
    original_link_target = (
        os.readlink(plugin_link) if plugin_link.is_symlink() else None
    )
    link_may_change = not (
        plugin_link.is_symlink() and plugin_link.resolve(strict=False) == plugin_root
    )
    manifest_may_change = update

    try:
        if update:
            update_plugin_cachebuster(plugin_root)

        if sync_dependencies:
            sync_plugin_dependencies(plugin_root)

        ensure_plugin_link(plugin_link, plugin_root, force=force)
        if marketplace_changed:
            write_json_atomically(marketplace_path, marketplace)

        if run_codex:
            run_codex_install(plugin_name, marketplace_name)
    except Exception as error:
        rollback_errors: list[str] = []

        if link_may_change:
            try:
                restore_plugin_link(plugin_link, original_link_target)
            except Exception as rollback_error:
                rollback_errors.append(f"plugin link: {rollback_error}")

        if marketplace_changed:
            try:
                restore_file(marketplace_path, original_marketplace)
            except Exception as rollback_error:
                rollback_errors.append(f"marketplace: {rollback_error}")

        if manifest_may_change:
            try:
                write_bytes_atomically(manifest_path, original_manifest)
            except Exception as rollback_error:
                rollback_errors.append(f"manifest: {rollback_error}")

        if rollback_errors:
            raise InstallError(
                f"Installation failed: {error}. Rollback also failed: "
                + "; ".join(rollback_errors)
            ) from error
        raise

    print(f"TurnEcho source linked at {plugin_link}")
    print(f"Marketplace ready at {marketplace_path}")
    if update:
        print("Updated the local plugin cachebuster before reinstalling")
    if run_codex:
        print(f"Installed {plugin_name}@{marketplace_name} in Codex")
        print("Start a new Codex thread before testing the plugin.")
        print("If prompted, review and trust the plugin hook with /hooks.")
    else:
        print("Codex installation skipped. Run codex plugin add when ready.")

    return plugin_name, marketplace_name


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Install this TurnEcho checkout as a local Codex plugin."
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="Replace a conflicting symlink or marketplace entry; never replace a directory.",
    )
    parser.add_argument(
        "--skip-codex",
        action="store_true",
        help="Prepare the link and marketplace without running the Codex CLI.",
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Show planned changes without writing files or running Codex.",
    )
    parser.add_argument(
        "--skip-dependency-sync",
        action="store_true",
        help="Skip required dependency installation; use only for preparing metadata.",
    )
    parser.add_argument(
        "--update",
        action="store_true",
        help=(
            "Update an existing local plugin with a Codex cachebuster before reinstalling it."
        ),
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    plugin_root = Path(__file__).resolve().parent.parent

    try:
        install_plugin(
            plugin_root,
            force=args.force,
            dry_run=args.dry_run,
            run_codex=not args.skip_codex,
            sync_dependencies=not args.skip_dependency_sync,
            update=args.update,
        )
    except (InstallError, OSError, subprocess.CalledProcessError) as error:
        print(f"TurnEcho installation failed: {error}", file=sys.stderr)
        return 1

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
