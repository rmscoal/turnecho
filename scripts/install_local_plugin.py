#!/usr/bin/env python3
"""Install the current TurnEcho checkout into each detected host."""

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

from turnecho.cli import (
    DEFAULT_COMMAND_PATH,
    CommandInstallError,
    CommandLinkState,
    command_directory_is_on_path,
    install_cli_command,
    restore_cli_command,
)
from turnecho.constant import (
    TURNECHO_MARKETPLACE_NAME,
    TURNECHO_PLUGIN_SELECTOR,
    TURNECHO_PLUGIN_VERSION,
)
from turnecho.install_plugin import (
    CLAUDE_HOST,
    CODEX_HOST,
    RuntimeInstallState,
    commit_runtime_install,
    detect_hosts,
    find_claude_marketplace,
    find_claude_plugin,
    prepare_installed_runtime,
    resolve_runtime_base_directory,
    rollback_runtime_install,
    run_checked_command,
    run_json_list_command,
)

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


def validate_claude_available() -> None:
    """Check Claude Code before making any local marketplace changes."""
    if shutil.which("claude") is None:
        raise InstallError(
            "The 'claude' command was not found. Re-run this command from a shell "
            "where Claude Code is installed, or use --skip-claude."
        )


def run_claude_install(plugin_root: Path, *, update: bool) -> None:
    """Install the checkout as a local Claude plugin."""
    plugin_root = plugin_root.expanduser().resolve()
    validate_claude_available()

    marketplaces = run_json_list_command(
        ["claude", "plugin", "marketplace", "list", "--json"]
    )
    marketplace = find_claude_marketplace(marketplaces)
    if marketplace is None:
        run_checked_command(
            [
                "claude",
                "plugin",
                "marketplace",
                "add",
                str(plugin_root),
                "--scope",
                "user",
            ]
        )
    else:
        market_path = marketplace.get("path")
        if (
            marketplace.get("source") != "directory"
            or not isinstance(market_path, str)
            or Path(market_path).expanduser().resolve() != plugin_root
        ):
            raise InstallError(
                f"Claude marketplace '{TURNECHO_MARKETPLACE_NAME}' already exists "
                "from a different source. Remove it first to install this checkout."
            )

    plugins = run_json_list_command(["claude", "plugin", "list", "--json"])
    if find_claude_plugin(plugins) is None:
        run_checked_command(
            [
                "claude",
                "plugin",
                "install",
                TURNECHO_PLUGIN_SELECTOR,
                "--scope",
                "user",
            ]
        )
    elif update:
        # `plugin update` skips unchanged versions, so refresh explicitly.
        run_checked_command(
            [
                "claude",
                "plugin",
                "uninstall",
                TURNECHO_PLUGIN_SELECTOR,
                "--scope",
                "user",
            ]
        )
        run_checked_command(
            [
                "claude",
                "plugin",
                "install",
                TURNECHO_PLUGIN_SELECTOR,
                "--scope",
                "user",
            ]
        )


def install_plugin(
    plugin_root: Path,
    *,
    marketplace_path: Path = DEFAULT_MARKETPLACE_PATH,
    plugin_link: Path | None = None,
    force: bool = False,
    dry_run: bool = False,
    run_codex: bool = True,
    run_claude: bool = False,
    prepare_codex: bool = True,
    sync_dependencies: bool = True,
    update: bool = False,
    command_path: Path = DEFAULT_COMMAND_PATH,
    runtime_base: Path | None = None,
) -> tuple[str, str]:
    """Install or update a checkout in the personal marketplace and each host.

    The symlink, personal-marketplace entry, and Codex cachebuster serve Codex
    only. Claude installs the checkout directly as a directory marketplace, so
    a Claude-only run must not create Codex files.
    """
    plugin_root = plugin_root.expanduser().resolve()
    plugin_name, category = load_plugin_metadata(plugin_root)
    manifest_path = plugin_root / ".codex-plugin" / "plugin.json"
    marketplace_path = marketplace_path.expanduser().resolve()
    plugin_link = plugin_link or DEFAULT_PLUGIN_PARENT / plugin_name
    plugin_link = plugin_link.expanduser()
    runtime_base = (
        resolve_runtime_base_directory()
        if runtime_base is None
        else runtime_base.expanduser().resolve()
    )

    # Loading is side-effect free; the entry is only prepared when Codex files
    # are wanted, so detection (not --skip-codex) decides whether they exist.
    marketplace, marketplace_name = load_or_create_marketplace(marketplace_path)
    marketplace_changed = False
    if prepare_codex:
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
        if prepare_codex:
            print(f"Would link {plugin_link} -> {plugin_root}")
            if marketplace_changed:
                print(f"Would update marketplace: {marketplace_path}")
        if sync_dependencies:
            print(
                "Would prepare and verify the TurnEcho runtime at: "
                f"{runtime_base / TURNECHO_PLUGIN_VERSION}"
            )
            print(f"Would install the TurnEcho command at: {command_path}")
        if update and prepare_codex:
            print(f"Would run: update_plugin_cachebuster.py {plugin_root}")
        if run_codex:
            print(f"Would run: codex plugin add {plugin_name}@{marketplace_name}")
        if run_claude:
            print(
                f"Would run: claude plugin marketplace add {plugin_root} (if missing)"
            )
            print(
                f"Would run: claude plugin install {TURNECHO_PLUGIN_SELECTOR} "
                "(if missing)"
            )
            if update:
                print(
                    "Would refresh the installed Claude plugin "
                    "(uninstall and reinstall)"
                )
        return plugin_name, marketplace_name

    if run_codex:
        validate_codex_available()
    if run_claude:
        validate_claude_available()

    original_manifest = manifest_path.read_bytes()
    original_marketplace = (
        marketplace_path.read_bytes()
        if prepare_codex and marketplace_path.is_file()
        else None
    )
    original_link_target = (
        os.readlink(plugin_link) if prepare_codex and plugin_link.is_symlink() else None
    )
    link_may_change = prepare_codex and not (
        plugin_link.is_symlink() and plugin_link.resolve(strict=False) == plugin_root
    )
    manifest_may_change = update and prepare_codex
    command_link_state: CommandLinkState | None = None
    runtime_state: RuntimeInstallState | None = None

    try:
        if update and prepare_codex:
            update_plugin_cachebuster(plugin_root)

        if sync_dependencies:
            runtime_state = prepare_installed_runtime(
                plugin_root,
                TURNECHO_PLUGIN_VERSION,
                runtime_base,
            )
            command_link_state = install_cli_command(
                runtime_state.runtime_root,
                command_path,
                managed_cache_root=runtime_base,
            )

        if prepare_codex:
            ensure_plugin_link(plugin_link, plugin_root, force=force)
            if marketplace_changed:
                write_json_atomically(marketplace_path, marketplace)

        if run_codex:
            run_codex_install(plugin_name, marketplace_name)
        if run_claude:
            # Host steps run last and stay installed when a later step fails;
            # both are idempotent, so re-running completes the installation.
            run_claude_install(plugin_root, update=update)
    except Exception as error:
        rollback_errors: list[str] = []

        if command_link_state is not None:
            try:
                restore_cli_command(command_link_state)
            except Exception as rollback_error:
                rollback_errors.append(f"command link: {rollback_error}")

        if runtime_state is not None:
            try:
                rollback_runtime_install(runtime_state)
            except Exception as rollback_error:
                rollback_errors.append(f"runtime: {rollback_error}")

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

    if runtime_state is not None:
        commit_runtime_install(runtime_state)

    if prepare_codex:
        print(f"TurnEcho source linked at {plugin_link}")
        print(f"Marketplace ready at {marketplace_path}")
    if sync_dependencies:
        print(f"TurnEcho command ready at {command_path}")
        if not command_directory_is_on_path(command_path):
            print(
                f"Warning: add {command_path.parent} to PATH to run 'turnecho'.",
                file=sys.stderr,
            )
    if update and prepare_codex:
        print("Updated the local plugin cachebuster before reinstalling")
    if run_codex:
        print(f"Installed {plugin_name}@{marketplace_name} in Codex")
        print("Start a new Codex thread before testing the plugin.")
        print("If prompted, review and trust the plugin hook with /hooks.")
    elif shutil.which("codex") is None:
        print("Codex was not detected; skipped Codex installation.")
    else:
        print("Codex installation skipped. Run codex plugin add when ready.")
    if run_claude:
        print(f"Installed {TURNECHO_PLUGIN_SELECTOR} in Claude Code")
        print("Start a new Claude Code session before testing the plugin.")
    elif shutil.which("claude") is None:
        print("Claude Code was not detected; skipped Claude Code installation.")
    else:
        print("Claude Code installation skipped. Re-run without --skip-claude.")

    return plugin_name, marketplace_name


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Install this TurnEcho checkout as a local plugin."
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
        "--skip-claude",
        action="store_true",
        help="Prepare the link and marketplace without running the Claude CLI.",
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
    detected_hosts = detect_hosts()
    codex_detected = CODEX_HOST in detected_hosts
    run_codex = codex_detected and not args.skip_codex
    run_claude = CLAUDE_HOST in detected_hosts and not args.skip_claude
    if (
        not run_codex
        and not run_claude
        and not args.skip_codex
        and not args.skip_claude
        and not args.dry_run
    ):
        print(
            "TurnEcho installation failed: neither the 'codex' nor the 'claude' "
            "command was found.",
            file=sys.stderr,
        )
        return 1

    try:
        install_plugin(
            plugin_root,
            force=args.force,
            dry_run=args.dry_run,
            run_codex=run_codex,
            run_claude=run_claude,
            # --skip-codex keeps the documented prepare-files flow for a later
            # manual `codex plugin add`; only an undetected Codex skips files.
            prepare_codex=codex_detected,
            sync_dependencies=not args.skip_dependency_sync,
            update=args.update,
        )
    except (
        CommandInstallError,
        InstallError,
        OSError,
        subprocess.CalledProcessError,
    ) as error:
        print(f"TurnEcho installation failed: {error}", file=sys.stderr)
        return 1

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
