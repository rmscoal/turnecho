"""Install TurnEcho from GitHub only after its audio runtime passes preflight."""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Any

from .runtime_preflight import validate_runtime_dependencies

PLUGIN_NAME = "turnecho"
MARKETPLACE_NAME = "turnecho"
MARKETPLACE_SOURCE = "rmscoal/turnecho"
MARKETPLACE_REF = "v0.1.0"
PLUGIN_SELECTOR = f"{PLUGIN_NAME}@{MARKETPLACE_NAME}"
CODEX_HOME_ENVIRONMENT_VARIABLE = "CODEX_HOME"
PLUGIN_CACHE_DIRECTORY = Path("plugins") / "cache"


class InstallError(RuntimeError):
    """Raised when the GitHub plugin installation cannot finish safely."""


def require_command(command_name: str) -> None:
    """Reject installation before changing Codex when a command is unavailable."""
    if shutil.which(command_name) is None:
        raise InstallError(f"The '{command_name}' command was not found.")


def run_checked_command(command: list[str]) -> None:
    """Run a command that does not need a parsed response."""
    subprocess.run(command, check=True)


def run_json_command(command: list[str]) -> dict[str, Any]:
    """Run a command and require a JSON object response."""
    completed_process = subprocess.run(
        command,
        check=True,
        capture_output=True,
        text=True,
    )
    try:
        payload: Any = json.loads(completed_process.stdout)
    except json.JSONDecodeError as error:
        raise InstallError(
            f"Command did not return valid JSON: {' '.join(command)}"
        ) from error

    if not isinstance(payload, dict):
        raise InstallError(f"Command did not return a JSON object: {' '.join(command)}")

    return payload


def find_marketplace(payload: dict[str, Any]) -> dict[str, Any] | None:
    """Find TurnEcho's configured marketplace entry."""
    marketplaces = payload.get("marketplaces", [])
    if not isinstance(marketplaces, list):
        raise InstallError("Codex returned an invalid marketplace list.")

    for marketplace in marketplaces:
        if (
            isinstance(marketplace, dict)
            and marketplace.get("name") == MARKETPLACE_NAME
        ):
            return marketplace

    return None


def validate_marketplace_source(marketplace: dict[str, Any]) -> None:
    """Reject an existing same-name marketplace that points somewhere else."""
    source = marketplace.get("marketplaceSource")
    if not isinstance(source, dict):
        raise InstallError(
            f"Marketplace '{MARKETPLACE_NAME}' exists, but its source cannot be verified."
        )

    source_type = source.get("sourceType")
    source_value = source.get("source")
    if source_type != "git" or not isinstance(source_value, str):
        raise InstallError(
            f"Marketplace '{MARKETPLACE_NAME}' does not point to the TurnEcho GitHub repository."
        )

    normalized_source = source_value.removesuffix(".git").rstrip("/")
    accepted_sources = {
        MARKETPLACE_SOURCE,
        f"https://github.com/{MARKETPLACE_SOURCE}",
        f"git@github.com:{MARKETPLACE_SOURCE}",
    }
    if normalized_source not in accepted_sources:
        raise InstallError(
            f"Marketplace '{MARKETPLACE_NAME}' points to an unexpected source: "
            f"{source_value}"
        )


def find_installed_plugin(payload: dict[str, Any]) -> dict[str, Any] | None:
    """Find the installed TurnEcho plugin entry."""
    installed_plugins = payload.get("installed", [])
    if not isinstance(installed_plugins, list):
        raise InstallError("Codex returned an invalid installed plugin list.")

    for plugin in installed_plugins:
        if isinstance(plugin, dict) and plugin.get("pluginId") == PLUGIN_SELECTOR:
            return plugin

    return None


def resolve_installed_plugin_path(payload: dict[str, Any]) -> str:
    """Read the installed cache path returned by ``codex plugin add``."""
    if payload.get("pluginId") != PLUGIN_SELECTOR:
        raise InstallError("Codex did not report TurnEcho as the installed plugin.")

    installed_path = payload.get("installedPath")
    if not isinstance(installed_path, str) or not installed_path.strip():
        raise InstallError("Codex did not report TurnEcho's installed plugin path.")

    return installed_path


def resolve_plugin_root(
    plugin: dict[str, Any],
    *,
    installed_path: str | None = None,
) -> Path:
    """Resolve and validate the plugin runtime source path."""
    if installed_path is not None:
        plugin_root = Path(installed_path).expanduser().resolve()
    else:
        source = plugin.get("source")
        source_path: str | None = None
        if isinstance(source, dict) and source.get("source") in (None, "local"):
            candidate = source.get("path")
            if isinstance(candidate, str) and candidate.strip():
                source_path = candidate

        if source_path is not None:
            plugin_root = Path(source_path).expanduser().resolve()
        else:
            version = plugin.get("version")
            if not isinstance(version, str) or not version.strip():
                raise InstallError(
                    "Codex did not report TurnEcho's installed plugin version."
                )

            version_path = Path(version)
            if version_path.is_absolute() or version_path.name != version:
                raise InstallError(
                    "Codex reported an invalid TurnEcho installed plugin version."
                )

            configured_codex_home = os.environ.get(CODEX_HOME_ENVIRONMENT_VARIABLE)
            codex_home = (
                Path(configured_codex_home).expanduser()
                if configured_codex_home
                else Path.home() / ".codex"
            )
            plugin_root = (
                codex_home
                / PLUGIN_CACHE_DIRECTORY
                / MARKETPLACE_NAME
                / PLUGIN_NAME
                / version
            ).resolve()

    if not (plugin_root / "pyproject.toml").is_file():
        raise InstallError(
            f"TurnEcho's installed source is missing pyproject.toml: {plugin_root}"
        )

    return plugin_root


def sync_installed_runtime(plugin_root: Path) -> None:
    """Create the installed plugin environment and verify its runtime imports."""
    run_checked_command(["uv", "sync", "--project", str(plugin_root), "--no-dev"])
    run_checked_command(
        [
            "uv",
            "run",
            "--project",
            str(plugin_root),
            "--no-dev",
            "python",
            "-m",
            "turnecho.runtime_preflight",
        ]
    )


def rollback_fresh_install(*, plugin_added: bool, marketplace_added: bool) -> list[str]:
    """Remove only Codex state created by this installation attempt."""
    rollback_errors: list[str] = []

    if plugin_added:
        try:
            run_checked_command(
                ["codex", "plugin", "remove", PLUGIN_SELECTOR, "--json"]
            )
        except Exception as error:
            rollback_errors.append(f"plugin removal: {error}")

    if marketplace_added:
        try:
            run_checked_command(
                [
                    "codex",
                    "plugin",
                    "marketplace",
                    "remove",
                    MARKETPLACE_NAME,
                    "--json",
                ]
            )
        except Exception as error:
            rollback_errors.append(f"marketplace removal: {error}")

    return rollback_errors


def install_plugin(*, update: bool = False) -> Path:
    """Preflight dependencies, install TurnEcho, and prepare its cached runtime."""
    require_command("codex")
    require_command("uv")

    # This process is launched by uvx, so imports fail before Codex is changed.
    try:
        validate_runtime_dependencies()
    except Exception as error:
        raise InstallError(f"Audio runtime preflight failed: {error}") from error

    marketplace_payload = run_json_command(
        ["codex", "plugin", "marketplace", "list", "--json"]
    )
    marketplace = find_marketplace(marketplace_payload)
    marketplace_added = False

    plugin_payload = run_json_command(["codex", "plugin", "list", "--json"])
    installed_plugin = find_installed_plugin(plugin_payload)
    installed_path: str | None = None
    plugin_added = False

    try:
        if marketplace is None:
            run_checked_command(
                [
                    "codex",
                    "plugin",
                    "marketplace",
                    "add",
                    MARKETPLACE_SOURCE,
                    "--ref",
                    MARKETPLACE_REF,
                    "--json",
                ]
            )
            marketplace_added = True
        else:
            validate_marketplace_source(marketplace)
            if update:
                run_checked_command(
                    [
                        "codex",
                        "plugin",
                        "marketplace",
                        "upgrade",
                        MARKETPLACE_NAME,
                        "--json",
                    ]
                )

        if installed_plugin is None or update:
            install_payload = run_json_command(
                ["codex", "plugin", "add", PLUGIN_SELECTOR, "--json"]
            )
            plugin_added = installed_plugin is None
            installed_path = resolve_installed_plugin_path(install_payload)
            plugin_payload = run_json_command(["codex", "plugin", "list", "--json"])
            installed_plugin = find_installed_plugin(plugin_payload)
            if installed_plugin is None:
                raise InstallError("Codex did not report TurnEcho as installed.")

        plugin_root = resolve_plugin_root(
            installed_plugin,
            installed_path=installed_path,
        )
        sync_installed_runtime(plugin_root)
    except Exception as error:
        rollback_errors = rollback_fresh_install(
            plugin_added=plugin_added,
            marketplace_added=marketplace_added,
        )
        if rollback_errors:
            raise InstallError(
                f"Installation failed: {error}. Rollback also failed: "
                + "; ".join(rollback_errors)
            ) from error
        raise

    return plugin_root


def parse_args() -> argparse.Namespace:
    """Parse installer options."""
    parser = argparse.ArgumentParser(
        description="Install TurnEcho with required audio dependency preflight."
    )
    parser.add_argument(
        "--update",
        action="store_true",
        help="Refresh the GitHub marketplace and reinstall TurnEcho.",
    )
    return parser.parse_args()


def main() -> int:
    """Install TurnEcho and report a concise result for terminal users."""
    args = parse_args()
    try:
        plugin_root = install_plugin(update=args.update)
    except (InstallError, OSError, subprocess.CalledProcessError) as error:
        print(f"TurnEcho installation failed: {error}", file=sys.stderr)
        return 1

    print(f"Installed TurnEcho with its audio runtime at {plugin_root}")
    print("Start a new Codex thread before testing the plugin.")
    print("If prompted, review and trust the plugin hook with /hooks.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
