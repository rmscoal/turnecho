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

from .cli import (
    DEFAULT_COMMAND_PATH,
    CommandInstallError,
    CommandLinkState,
    command_directory_is_on_path,
    install_cli_command,
    restore_cli_command,
)
from .runtime_preflight import validate_runtime_dependencies

PLUGIN_NAME = "turnecho"
MARKETPLACE_NAME = "turnecho"
MARKETPLACE_SOURCE = "rmscoal/turnecho"
PLUGIN_VERSION = "0.2.0"
MARKETPLACE_REF = f"v{PLUGIN_VERSION}"
PLUGIN_SELECTOR = f"{PLUGIN_NAME}@{MARKETPLACE_NAME}"
CODEX_HOME_ENVIRONMENT_VARIABLE = "CODEX_HOME"
PLUGIN_CACHE_DIRECTORY = Path("plugins") / "cache"
MARKETPLACE_MANIFEST_PATH = Path(".agents") / "plugins" / "marketplace.json"


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


def resolve_plugin_source_ref(plugin: dict[str, Any]) -> str | None:
    """Return the immutable Git ref reported for an installed plugin."""
    source = plugin.get("source")
    if not isinstance(source, dict) or source.get("source") != "git":
        return None

    ref = source.get("ref")
    if not isinstance(ref, str) or not ref.strip():
        return None

    return ref


def resolve_marketplace_plugin_ref(marketplace: dict[str, Any]) -> str:
    """Read the TurnEcho plugin ref from a configured marketplace snapshot."""
    root = marketplace.get("root")
    if not isinstance(root, str) or not root.strip():
        raise InstallError(
            f"Marketplace '{MARKETPLACE_NAME}' does not report its snapshot root."
        )

    manifest_path = Path(root).expanduser().resolve() / MARKETPLACE_MANIFEST_PATH
    try:
        manifest: Any = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise InstallError(
            f"Cannot read the configured TurnEcho marketplace: {manifest_path}"
        ) from error

    if not isinstance(manifest, dict) or manifest.get("name") != MARKETPLACE_NAME:
        raise InstallError("The configured TurnEcho marketplace manifest is invalid.")

    plugins = manifest.get("plugins")
    if not isinstance(plugins, list):
        raise InstallError("The configured TurnEcho marketplace has no plugin list.")

    for plugin in plugins:
        if not isinstance(plugin, dict) or plugin.get("name") != PLUGIN_NAME:
            continue

        source = plugin.get("source")
        if not isinstance(source, dict):
            break

        ref = source.get("ref")
        if isinstance(ref, str) and ref.strip():
            return ref
        break

    raise InstallError("The configured TurnEcho marketplace has no release ref.")


def resolve_previous_marketplace_ref(
    marketplace: dict[str, Any],
    installed_plugin: dict[str, Any] | None,
) -> str:
    """Resolve the ref needed to restore a replaced marketplace."""
    if installed_plugin is not None:
        installed_ref = resolve_plugin_source_ref(installed_plugin)
        if installed_ref is not None:
            return installed_ref

    return resolve_marketplace_plugin_ref(marketplace)


def validate_installed_release(plugin: dict[str, Any]) -> None:
    """Require Codex to report the release requested by this installer."""
    version = plugin.get("version")
    ref = resolve_plugin_source_ref(plugin)
    if version != PLUGIN_VERSION or ref != MARKETPLACE_REF:
        raise InstallError(
            "Codex installed an unexpected TurnEcho release: "
            f"version={version!r}, ref={ref!r}; "
            f"expected version={PLUGIN_VERSION!r}, ref={MARKETPLACE_REF!r}."
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


def add_marketplace(ref: str) -> None:
    """Register the TurnEcho marketplace at an immutable release ref."""
    run_checked_command(
        [
            "codex",
            "plugin",
            "marketplace",
            "add",
            MARKETPLACE_SOURCE,
            "--ref",
            ref,
            "--json",
        ]
    )


def remove_marketplace() -> None:
    """Remove the configured TurnEcho marketplace registration."""
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
            remove_marketplace()
        except Exception as error:
            rollback_errors.append(f"marketplace removal: {error}")

    return rollback_errors


def rollback_marketplace_replacement(
    *,
    previous_ref: str,
    replacement_added: bool,
    restore_plugin: bool,
) -> list[str]:
    """Restore marketplace and plugin state after a failed update."""
    rollback_errors: list[str] = []

    if replacement_added:
        try:
            remove_marketplace()
        except Exception as error:
            rollback_errors.append(f"replacement marketplace removal: {error}")

    try:
        add_marketplace(previous_ref)
    except Exception as error:
        rollback_errors.append(f"previous marketplace restoration: {error}")
        return rollback_errors

    if restore_plugin:
        try:
            run_checked_command(["codex", "plugin", "add", PLUGIN_SELECTOR, "--json"])
        except Exception as error:
            rollback_errors.append(f"previous plugin restoration: {error}")

    return rollback_errors


def rollback_plugin_without_marketplace(previous_ref: str) -> list[str]:
    """Restore a plugin whose marketplace was absent before the update."""
    rollback_errors: list[str] = []

    try:
        add_marketplace(previous_ref)
    except Exception as error:
        rollback_errors.append(f"temporary marketplace restoration: {error}")
        return rollback_errors

    try:
        run_checked_command(["codex", "plugin", "add", PLUGIN_SELECTOR, "--json"])
    except Exception as error:
        rollback_errors.append(f"previous plugin restoration: {error}")

    try:
        remove_marketplace()
    except Exception as error:
        rollback_errors.append(f"temporary marketplace removal: {error}")

    return rollback_errors


def install_plugin(
    *,
    update: bool = False,
    command_path: Path = DEFAULT_COMMAND_PATH,
) -> Path:
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
    plugin_install_attempted = False
    command_link_state: CommandLinkState | None = None
    previous_marketplace_ref: str | None = None
    marketplace_replacement_started = False
    replacement_marketplace_added = False
    plugin_was_installed = installed_plugin is not None
    plugin_without_marketplace_update = False

    try:
        if marketplace is None:
            if update and installed_plugin is not None:
                previous_marketplace_ref = resolve_plugin_source_ref(installed_plugin)
                if previous_marketplace_ref is None:
                    raise InstallError(
                        "Cannot preserve the installed TurnEcho release before update."
                    )
                plugin_without_marketplace_update = True
            add_marketplace(MARKETPLACE_REF)
            marketplace_added = True
        else:
            validate_marketplace_source(marketplace)
            if update:
                previous_marketplace_ref = resolve_previous_marketplace_ref(
                    marketplace,
                    installed_plugin,
                )
                remove_marketplace()
                marketplace_replacement_started = True
                add_marketplace(MARKETPLACE_REF)
                replacement_marketplace_added = True

        if installed_plugin is None or update:
            plugin_install_attempted = True
            install_payload = run_json_command(
                ["codex", "plugin", "add", PLUGIN_SELECTOR, "--json"]
            )
            plugin_added = installed_plugin is None
            installed_path = resolve_installed_plugin_path(install_payload)
            plugin_payload = run_json_command(["codex", "plugin", "list", "--json"])
            installed_plugin = find_installed_plugin(plugin_payload)
            if installed_plugin is None:
                raise InstallError("Codex did not report TurnEcho as installed.")
            validate_installed_release(installed_plugin)

        plugin_root = resolve_plugin_root(
            installed_plugin,
            installed_path=installed_path,
        )
        sync_installed_runtime(plugin_root)
        command_link_state = install_cli_command(plugin_root, command_path)
    except Exception as error:
        command_rollback_error: Exception | None = None
        if command_link_state is not None:
            try:
                restore_cli_command(command_link_state)
            except Exception as rollback_error:
                command_rollback_error = rollback_error
        rollback_errors = rollback_fresh_install(
            plugin_added=plugin_added,
            marketplace_added=marketplace_added,
        )
        if marketplace_replacement_started:
            if previous_marketplace_ref is None:
                rollback_errors.append("previous marketplace ref was not preserved")
            else:
                rollback_errors.extend(
                    rollback_marketplace_replacement(
                        previous_ref=previous_marketplace_ref,
                        replacement_added=replacement_marketplace_added,
                        restore_plugin=plugin_was_installed
                        and plugin_install_attempted,
                    )
                )
        elif plugin_without_marketplace_update and plugin_install_attempted:
            if previous_marketplace_ref is None:
                rollback_errors.append("previous plugin ref was not preserved")
            else:
                rollback_errors.extend(
                    rollback_plugin_without_marketplace(previous_marketplace_ref)
                )
        if command_rollback_error is not None:
            rollback_errors.insert(0, f"command restoration: {command_rollback_error}")
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
        help="Replace the GitHub marketplace ref and reinstall TurnEcho.",
    )
    return parser.parse_args()


def main() -> int:
    """Install TurnEcho and report a concise result for terminal users."""
    args = parse_args()
    try:
        plugin_root = install_plugin(update=args.update)
    except (
        CommandInstallError,
        InstallError,
        OSError,
        subprocess.CalledProcessError,
    ) as error:
        print(f"TurnEcho installation failed: {error}", file=sys.stderr)
        return 1

    print(f"Installed TurnEcho with its audio runtime at {plugin_root}")
    print(f"Installed the TurnEcho command at {DEFAULT_COMMAND_PATH}")
    if not command_directory_is_on_path():
        print(
            f"Warning: add {DEFAULT_COMMAND_PATH.parent} to PATH to run 'turnecho'.",
            file=sys.stderr,
        )
    print("Start a new Codex thread before testing the plugin.")
    print("If prompted, review and trust the plugin hook with /hooks.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
