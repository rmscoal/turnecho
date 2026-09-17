"""Install TurnEcho from GitHub only after its audio runtime passes preflight."""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Any, NoReturn

from .cli import (
    DEFAULT_COMMAND_PATH,
    CommandInstallError,
    CommandLinkState,
    command_directory_is_on_path,
    install_cli_command,
    remove_cli_command,
    restore_cli_command,
)
from .constant import (
    TURNECHO_CODEX_HOME_ENVIRONMENT_VARIABLE,
    TURNECHO_MARKETPLACE_MANIFEST_PATH,
    TURNECHO_MARKETPLACE_NAME,
    TURNECHO_MARKETPLACE_REF,
    TURNECHO_MARKETPLACE_SOURCE,
    TURNECHO_PLUGIN_CACHE_DIRECTORY,
    TURNECHO_PLUGIN_NAME,
    TURNECHO_PLUGIN_SELECTOR,
    TURNECHO_PLUGIN_VERSION,
    TURNECHO_RUNTIME_DIRECTORY,
    TURNECHO_RUNTIME_MARKER_FILE,
)
from .exc import InstallError
from .hosts.types import TurnEchoHostSource
from .runtime_preflight import validate_runtime_dependencies

CODEX_HOST = TurnEchoHostSource.CODEX.value
CLAUDE_HOST = TurnEchoHostSource.CLAUDE_CODE.value

# Canonical host order for installation, messaging, and rollback.
HOST_COMMANDS = {CODEX_HOST: "codex", CLAUDE_HOST: "claude"}
HOST_DISPLAY_NAMES = {CODEX_HOST: "Codex", CLAUDE_HOST: "Claude Code"}


def require_command(command_name: str) -> None:
    """Reject installation before changing a host when a command is unavailable."""
    if shutil.which(command_name) is None:
        raise InstallError(f"The '{command_name}' command was not found.")


def detect_hosts() -> list[str]:
    """Return the supported hosts whose CLIs are available, in install order."""
    return [
        host
        for host, command in HOST_COMMANDS.items()
        if shutil.which(command) is not None
    ]


def normalize_hosts(hosts: Sequence[str]) -> list[str]:
    """Validate explicit hosts and return them in canonical install order."""
    unknown = [host for host in hosts if host not in HOST_COMMANDS]
    if unknown:
        raise InstallError(f"Unsupported TurnEcho host(s): {', '.join(unknown)}.")
    return [host for host in HOST_COMMANDS if host in hosts]


@dataclass(frozen=True)
class RuntimeInstallState:
    """Runtime paths needed to commit or roll back a replacement."""

    runtime_root: Path
    backup_root: Path | None


def run_checked_command(
    command: list[str],
    *,
    environment: dict[str, str] | None = None,
    suppress_stdout: bool = False,
) -> None:
    """Run a command that does not need a parsed response."""
    subprocess.run(
        command,
        check=True,
        env=environment,
        stdout=subprocess.DEVNULL if suppress_stdout else None,
    )


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


def run_json_list_command(command: list[str]) -> list[Any]:
    """Run a command and require a JSON array response."""
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

    if not isinstance(payload, list):
        raise InstallError(f"Command did not return a JSON array: {' '.join(command)}")

    return payload


def find_marketplace(payload: dict[str, Any]) -> dict[str, Any] | None:
    """Find TurnEcho's configured marketplace entry."""
    marketplaces = payload.get("marketplaces", [])
    if not isinstance(marketplaces, list):
        raise InstallError("Codex returned an invalid marketplace list.")

    for marketplace in marketplaces:
        if (
            isinstance(marketplace, dict)
            and marketplace.get("name") == TURNECHO_MARKETPLACE_NAME
        ):
            return marketplace

    return None


def validate_marketplace_source(marketplace: dict[str, Any]) -> None:
    """Reject an existing same-name marketplace that points somewhere else."""
    source = marketplace.get("marketplaceSource")
    if not isinstance(source, dict):
        raise InstallError(
            f"Marketplace '{TURNECHO_MARKETPLACE_NAME}' exists, but its source cannot be verified."
        )

    source_type = source.get("sourceType")
    source_value = source.get("source")
    if source_type != "git" or not isinstance(source_value, str):
        raise InstallError(
            f"Marketplace '{TURNECHO_MARKETPLACE_NAME}' does not point to the TurnEcho GitHub repository."
        )

    normalized_source = source_value.removesuffix(".git").rstrip("/")
    accepted_sources = {
        TURNECHO_MARKETPLACE_SOURCE,
        f"https://github.com/{TURNECHO_MARKETPLACE_SOURCE}",
        f"git@github.com:{TURNECHO_MARKETPLACE_SOURCE}",
    }
    if normalized_source not in accepted_sources:
        raise InstallError(
            f"Marketplace '{TURNECHO_MARKETPLACE_NAME}' points to an unexpected source: "
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
            f"Marketplace '{TURNECHO_MARKETPLACE_NAME}' does not report its snapshot root."
        )

    manifest_path = (
        Path(root).expanduser().resolve() / TURNECHO_MARKETPLACE_MANIFEST_PATH
    )
    try:
        manifest: Any = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        raise InstallError(
            f"Cannot read the configured TurnEcho marketplace: {manifest_path}"
        ) from error

    if (
        not isinstance(manifest, dict)
        or manifest.get("name") != TURNECHO_MARKETPLACE_NAME
    ):
        raise InstallError("The configured TurnEcho marketplace manifest is invalid.")

    plugins = manifest.get("plugins")
    if not isinstance(plugins, list):
        raise InstallError("The configured TurnEcho marketplace has no plugin list.")

    for plugin in plugins:
        if not isinstance(plugin, dict) or plugin.get("name") != TURNECHO_PLUGIN_NAME:
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
    if version != TURNECHO_PLUGIN_VERSION or ref != TURNECHO_MARKETPLACE_REF:
        raise InstallError(
            "Codex installed an unexpected TurnEcho release: "
            f"version={version!r}, ref={ref!r}; "
            f"expected version={TURNECHO_PLUGIN_VERSION!r}, ref={TURNECHO_MARKETPLACE_REF!r}."
        )


def resolve_installed_plugin_version(plugin: dict[str, Any]) -> str:
    """Return a safe version directory name reported by Codex."""
    version = plugin.get("version")
    if not isinstance(version, str) or not version.strip():
        raise InstallError("Codex did not report TurnEcho's installed plugin version.")
    version_path = Path(version)
    if version_path.is_absolute() or version_path.name != version:
        raise InstallError(
            "Codex reported an invalid TurnEcho installed plugin version."
        )
    return version


def find_installed_plugin(payload: dict[str, Any]) -> dict[str, Any] | None:
    """Find the installed TurnEcho plugin entry."""
    installed_plugins = payload.get("installed", [])
    if not isinstance(installed_plugins, list):
        raise InstallError("Codex returned an invalid installed plugin list.")

    for plugin in installed_plugins:
        if (
            isinstance(plugin, dict)
            and plugin.get("pluginId") == TURNECHO_PLUGIN_SELECTOR
        ):
            return plugin

    return None


def resolve_installed_plugin_path(payload: dict[str, Any]) -> str:
    """Read the installed cache path returned by ``codex plugin add``."""
    if payload.get("pluginId") != TURNECHO_PLUGIN_SELECTOR:
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

            configured_codex_home = os.environ.get(
                TURNECHO_CODEX_HOME_ENVIRONMENT_VARIABLE
            )
            codex_home = (
                Path(configured_codex_home).expanduser()
                if configured_codex_home
                else Path.home() / ".codex"
            )
            plugin_root = (
                codex_home
                / TURNECHO_PLUGIN_CACHE_DIRECTORY
                / TURNECHO_MARKETPLACE_NAME
                / TURNECHO_PLUGIN_NAME
                / version
            ).resolve()

    if not (plugin_root / "pyproject.toml").is_file():
        raise InstallError(
            f"TurnEcho's installed source is missing pyproject.toml: {plugin_root}"
        )

    return plugin_root


def find_claude_marketplace(entries: list[Any]) -> dict[str, Any] | None:
    """Find TurnEcho's configured Claude marketplace entry."""
    for marketplace in entries:
        if (
            isinstance(marketplace, dict)
            and marketplace.get("name") == TURNECHO_MARKETPLACE_NAME
        ):
            return marketplace

    return None


def validate_claude_marketplace_source(marketplace: dict[str, Any]) -> None:
    """Reject an existing same-name marketplace that points somewhere else."""
    if marketplace.get("source") != "github":
        raise InstallError(
            f"Marketplace '{TURNECHO_MARKETPLACE_NAME}' does not point to the "
            "TurnEcho GitHub repository."
        )

    repo = marketplace.get("repo")
    if not isinstance(repo, str):
        raise InstallError(
            f"Marketplace '{TURNECHO_MARKETPLACE_NAME}' exists, but its source cannot be verified."
        )

    normalized_repo = repo.removesuffix(".git").rstrip("/")
    accepted_sources = {
        TURNECHO_MARKETPLACE_SOURCE,
        f"https://github.com/{TURNECHO_MARKETPLACE_SOURCE}",
        f"git@github.com:{TURNECHO_MARKETPLACE_SOURCE}",
    }
    if normalized_repo not in accepted_sources:
        raise InstallError(
            f"Marketplace '{TURNECHO_MARKETPLACE_NAME}' points to an unexpected source: "
            f"{repo}"
        )


def find_claude_plugin(entries: list[Any]) -> dict[str, Any] | None:
    """Find the installed TurnEcho Claude plugin entry."""
    for plugin in entries:
        if isinstance(plugin, dict) and plugin.get("id") == TURNECHO_PLUGIN_SELECTOR:
            return plugin

    return None


def resolve_claude_plugin_root(plugin: dict[str, Any]) -> Path:
    """Resolve and validate the installed Claude plugin path."""
    installed_path = plugin.get("installPath")
    if not isinstance(installed_path, str) or not installed_path.strip():
        raise InstallError("Claude did not report TurnEcho's installed plugin path.")

    plugin_root = Path(installed_path).expanduser().resolve()
    if not (plugin_root / "pyproject.toml").is_file():
        raise InstallError(
            f"TurnEcho's installed source is missing pyproject.toml: {plugin_root}"
        )

    return plugin_root


def resolve_claude_plugin_version(plugin: dict[str, Any]) -> str:
    """Return the installed Claude plugin version after validating it."""
    version = plugin.get("version")
    if version != TURNECHO_PLUGIN_VERSION:
        raise InstallError(
            "Claude installed an unexpected TurnEcho release: "
            f"version={version!r}; expected version={TURNECHO_PLUGIN_VERSION!r}. "
            "Claude marketplaces track the repository instead of a pinned ref, "
            "so retry once the released version is published."
        )

    return version


def resolve_runtime_base_directory() -> Path:
    """Return TurnEcho's stable, user-owned runtime directory."""
    return (Path.home() / ".local" / "share" / TURNECHO_RUNTIME_DIRECTORY).resolve()


def _runtime_marker(runtime_root: Path) -> dict[str, Any] | None:
    marker_path = runtime_root / TURNECHO_RUNTIME_MARKER_FILE
    try:
        marker: Any = json.loads(marker_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError):
        return None
    return marker if isinstance(marker, dict) else None


def is_managed_runtime_directory(runtime_root: Path) -> bool:
    """Return whether a directory has a valid TurnEcho ownership marker."""
    marker = _runtime_marker(runtime_root)
    return (
        marker is not None
        and marker.get("name") == TURNECHO_PLUGIN_NAME
        and isinstance(marker.get("version"), str)
        and bool(marker["version"])
    )


def _remove_managed_runtime(runtime_root: Path) -> None:
    if not is_managed_runtime_directory(runtime_root):
        raise InstallError(f"Refusing to remove an unmanaged runtime: {runtime_root}")
    shutil.rmtree(runtime_root)


def prepare_installed_runtime(
    plugin_root: Path,
    version: str,
    runtime_base: Path,
) -> RuntimeInstallState:
    """Build a non-editable runtime at its permanent path with rollback."""
    runtime_base = runtime_base.expanduser().resolve()
    runtime_base.mkdir(parents=True, exist_ok=True)
    runtime_root = runtime_base / version
    if runtime_root.exists() and not is_managed_runtime_directory(runtime_root):
        raise InstallError(f"Refusing to replace an unmanaged runtime: {runtime_root}")

    # Virtual-environment launchers contain absolute interpreter paths. Preserve an
    # existing runtime for rollback, then build its replacement at the final path.
    backup_root: Path | None = None
    if runtime_root.exists():
        backup_root = Path(
            tempfile.mkdtemp(prefix=f".{version}.backup-", dir=runtime_base)
        )
        backup_root.rmdir()
        os.replace(runtime_root, backup_root)

    try:
        runtime_root.mkdir()
        (runtime_root / TURNECHO_RUNTIME_MARKER_FILE).write_text(
            json.dumps({"name": TURNECHO_PLUGIN_NAME, "version": version}) + "\n",
            encoding="utf-8",
        )
        shutil.copy2(plugin_root / "pyproject.toml", runtime_root / "pyproject.toml")
        environment = os.environ.copy()
        environment.pop("VIRTUAL_ENV", None)
        environment["UV_PROJECT_ENVIRONMENT"] = str(runtime_root / ".venv")
        run_checked_command(
            [
                "uv",
                "sync",
                "--project",
                str(plugin_root),
                "--no-dev",
                "--no-editable",
            ],
            environment=environment,
        )
        runtime_python = runtime_root / ".venv" / "bin" / "python"
        runtime_command = runtime_root / ".venv" / "bin" / "turnecho"
        if not runtime_python.is_file() or not runtime_command.is_file():
            raise InstallError(
                f"TurnEcho runtime commands were not created under {runtime_root}"
            )
        run_checked_command([str(runtime_python), "-m", "turnecho.runtime_preflight"])
        run_checked_command(
            [str(runtime_command), "--version"],
            suppress_stdout=True,
        )
    except Exception:
        if runtime_root.exists():
            shutil.rmtree(runtime_root)
        if backup_root is not None and backup_root.exists():
            os.replace(backup_root, runtime_root)
        raise

    return RuntimeInstallState(runtime_root=runtime_root, backup_root=backup_root)


def commit_runtime_install(state: RuntimeInstallState) -> None:
    """Discard the previous same-version runtime after installation succeeds."""
    if state.backup_root is not None and state.backup_root.exists():
        _remove_managed_runtime(state.backup_root)


def rollback_runtime_install(state: RuntimeInstallState) -> None:
    """Remove the new runtime and restore the previous same-version runtime."""
    if state.runtime_root.exists():
        _remove_managed_runtime(state.runtime_root)
    if state.backup_root is not None and state.backup_root.exists():
        os.replace(state.backup_root, state.runtime_root)


def add_marketplace(ref: str) -> None:
    """Register the TurnEcho marketplace at an immutable release ref."""
    run_checked_command(
        [
            "codex",
            "plugin",
            "marketplace",
            "add",
            TURNECHO_MARKETPLACE_SOURCE,
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
            TURNECHO_MARKETPLACE_NAME,
            "--json",
        ]
    )


def rollback_fresh_install(*, plugin_added: bool, marketplace_added: bool) -> list[str]:
    """Remove only Codex state created by this installation attempt."""
    rollback_errors: list[str] = []

    if plugin_added:
        try:
            run_checked_command(
                ["codex", "plugin", "remove", TURNECHO_PLUGIN_SELECTOR, "--json"]
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
    command_path: Path,
    runtime_base: Path,
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
            restore_plugin_runtime(previous_ref, command_path, runtime_base)
        except Exception as error:
            rollback_errors.append(f"previous plugin runtime restoration: {error}")

    return rollback_errors


def restore_plugin_runtime(
    previous_ref: str,
    command_path: Path,
    runtime_base: Path,
) -> Path:
    """Reinstall the selected release and rebuild its runtime and CLI link."""
    install_payload = run_json_command(
        ["codex", "plugin", "add", TURNECHO_PLUGIN_SELECTOR, "--json"]
    )
    installed_path = resolve_installed_plugin_path(install_payload)
    plugin_payload = run_json_command(["codex", "plugin", "list", "--json"])
    installed_plugin = find_installed_plugin(plugin_payload)
    if installed_plugin is None:
        raise InstallError("Codex did not report the previous TurnEcho plugin.")
    restored_ref = resolve_plugin_source_ref(installed_plugin)
    if restored_ref != previous_ref:
        raise InstallError(
            "Codex restored an unexpected TurnEcho release: "
            f"ref={restored_ref!r}; expected ref={previous_ref!r}."
        )

    plugin_root = resolve_plugin_root({}, installed_path=installed_path)
    version = resolve_installed_plugin_version(installed_plugin)
    runtime_state = prepare_installed_runtime(plugin_root, version, runtime_base)
    try:
        install_cli_command(
            runtime_state.runtime_root,
            command_path,
            managed_cache_root=runtime_base,
            managed_cache_roots=(_codex_plugin_version_root(),),
        )
    except Exception:
        rollback_runtime_install(runtime_state)
        raise
    commit_runtime_install(runtime_state)
    return runtime_state.runtime_root


def rollback_plugin_without_marketplace(
    previous_ref: str,
    command_path: Path,
    runtime_base: Path,
) -> list[str]:
    """Restore a plugin whose marketplace was absent before the update."""
    rollback_errors: list[str] = []

    try:
        add_marketplace(previous_ref)
    except Exception as error:
        rollback_errors.append(f"temporary marketplace restoration: {error}")
        return rollback_errors

    try:
        restore_plugin_runtime(previous_ref, command_path, runtime_base)
    except Exception as error:
        rollback_errors.append(f"previous plugin runtime restoration: {error}")

    try:
        remove_marketplace()
    except Exception as error:
        rollback_errors.append(f"temporary marketplace removal: {error}")

    return rollback_errors


def _codex_plugin_version_root() -> Path:
    configured_codex_home = os.environ.get(TURNECHO_CODEX_HOME_ENVIRONMENT_VARIABLE)
    codex_home = (
        Path(configured_codex_home).expanduser()
        if configured_codex_home
        else Path.home() / ".codex"
    )
    return (
        codex_home
        / TURNECHO_PLUGIN_CACHE_DIRECTORY
        / TURNECHO_MARKETPLACE_NAME
        / TURNECHO_PLUGIN_NAME
    ).resolve()


def remove_managed_runtimes(runtime_base: Path) -> int:
    """Remove only marked TurnEcho runtimes from the managed runtime directory."""
    runtime_base = runtime_base.expanduser().resolve()
    if not runtime_base.is_dir():
        return 0

    removed = 0
    for child in runtime_base.iterdir():
        if child.is_dir() and is_managed_runtime_directory(child):
            _remove_managed_runtime(child)
            removed += 1
    try:
        runtime_base.rmdir()
        runtime_base.parent.rmdir()
    except OSError:
        pass
    return removed


def _uninstall_codex_host() -> None:
    """Remove the TurnEcho plugin and marketplace from Codex."""
    plugin_payload = run_json_command(["codex", "plugin", "list", "--json"])
    installed_plugin = find_installed_plugin(plugin_payload)
    marketplace_payload = run_json_command(
        ["codex", "plugin", "marketplace", "list", "--json"]
    )
    marketplace = find_marketplace(marketplace_payload)
    if marketplace is not None:
        validate_marketplace_source(marketplace)

    if installed_plugin is not None:
        run_checked_command(
            ["codex", "plugin", "remove", TURNECHO_PLUGIN_SELECTOR, "--json"]
        )
    if marketplace is not None:
        remove_marketplace()


def _uninstall_claude_host() -> None:
    """Remove the TurnEcho plugin and marketplace from Claude Code."""
    plugin_entries = run_json_list_command(["claude", "plugin", "list", "--json"])
    installed_plugin = find_claude_plugin(plugin_entries)
    marketplace_entries = run_json_list_command(
        ["claude", "plugin", "marketplace", "list", "--json"]
    )
    marketplace = find_claude_marketplace(marketplace_entries)
    if marketplace is not None:
        validate_claude_marketplace_source(marketplace)

    if installed_plugin is not None:
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
    if marketplace is not None:
        run_checked_command(
            [
                "claude",
                "plugin",
                "marketplace",
                "remove",
                TURNECHO_MARKETPLACE_NAME,
            ]
        )


def uninstall_plugin(
    *,
    command_path: Path = DEFAULT_COMMAND_PATH,
    runtime_base: Path | None = None,
    hosts: Sequence[str] = (CODEX_HOST,),
) -> tuple[bool, int]:
    """Remove host state plus only TurnEcho-owned runtime and command files.

    Hosts without an available CLI are skipped. Shared runtime and command
    cleanup always runs, even when no host is selected.
    """
    runtime_base = (
        resolve_runtime_base_directory()
        if runtime_base is None
        else runtime_base.expanduser().resolve()
    )

    for host in normalize_hosts(hosts):
        if shutil.which(HOST_COMMANDS[host]) is None:
            continue
        if host == CODEX_HOST:
            _uninstall_codex_host()
        else:
            _uninstall_claude_host()

    command_removed = remove_cli_command(
        command_path,
        managed_cache_roots=(runtime_base, _codex_plugin_version_root()),
    )
    runtime_count = remove_managed_runtimes(runtime_base)
    return command_removed, runtime_count


@dataclass
class CodexHostInstall:
    """Codex plugin state needed to build the runtime or roll back."""

    plugin_root: Path | None = None
    version: str | None = None
    plugin_added: bool = False
    marketplace_added: bool = False
    marketplace_replacement_started: bool = False
    replacement_marketplace_added: bool = False
    previous_marketplace_ref: str | None = None
    plugin_was_installed: bool = False
    plugin_install_attempted: bool = False
    plugin_without_marketplace_update: bool = False


@dataclass
class ClaudeHostInstall:
    """Claude plugin state needed to build the runtime or roll back."""

    plugin_root: Path | None = None
    version: str | None = None
    plugin_added: bool = False
    marketplace_added: bool = False


def _raise_with_rollback_errors(
    error: Exception, rollback_errors: list[str]
) -> NoReturn:
    """Re-raise an installation failure, reporting rollback failures with it."""
    if rollback_errors:
        raise InstallError(
            f"Installation failed: {error}. Rollback also failed: "
            + "; ".join(rollback_errors)
        ) from error
    raise error


def _install_codex_host(
    *,
    update: bool,
    command_path: Path,
    runtime_base: Path,
) -> CodexHostInstall:
    """Install or update the TurnEcho Codex plugin and marketplace.

    Partial progress is rolled back before any error propagates, so the
    orchestrator only ever rolls back completed host states.
    """
    state = CodexHostInstall()
    try:
        marketplace_payload = run_json_command(
            ["codex", "plugin", "marketplace", "list", "--json"]
        )
        marketplace = find_marketplace(marketplace_payload)

        plugin_payload = run_json_command(["codex", "plugin", "list", "--json"])
        installed_plugin = find_installed_plugin(plugin_payload)
        installed_path: str | None = None
        state.plugin_was_installed = installed_plugin is not None

        if marketplace is None:
            if update and installed_plugin is not None:
                state.previous_marketplace_ref = resolve_plugin_source_ref(
                    installed_plugin
                )
                if state.previous_marketplace_ref is None:
                    raise InstallError(
                        "Cannot preserve the installed TurnEcho release before update."
                    )
                state.plugin_without_marketplace_update = True
            add_marketplace(TURNECHO_MARKETPLACE_REF)
            state.marketplace_added = True
        else:
            validate_marketplace_source(marketplace)
            if update:
                state.previous_marketplace_ref = resolve_previous_marketplace_ref(
                    marketplace,
                    installed_plugin,
                )
                remove_marketplace()
                state.marketplace_replacement_started = True
                add_marketplace(TURNECHO_MARKETPLACE_REF)
                state.replacement_marketplace_added = True

        if installed_plugin is None or update:
            state.plugin_install_attempted = True
            install_payload = run_json_command(
                ["codex", "plugin", "add", TURNECHO_PLUGIN_SELECTOR, "--json"]
            )
            state.plugin_added = installed_plugin is None
            installed_path = resolve_installed_plugin_path(install_payload)
            plugin_payload = run_json_command(["codex", "plugin", "list", "--json"])
            installed_plugin = find_installed_plugin(plugin_payload)
            if installed_plugin is None:
                raise InstallError("Codex did not report TurnEcho as installed.")
            validate_installed_release(installed_plugin)

        state.plugin_root = resolve_plugin_root(
            installed_plugin,
            installed_path=installed_path,
        )
        state.version = resolve_installed_plugin_version(installed_plugin)
        return state
    except Exception as error:
        rollback_errors = _rollback_codex_host(
            state,
            command_path=command_path,
            runtime_base=runtime_base,
        )
        _raise_with_rollback_errors(error, rollback_errors)


def _rollback_codex_host(
    state: CodexHostInstall,
    *,
    command_path: Path,
    runtime_base: Path,
) -> list[str]:
    """Remove Codex state created by this run and restore replaced releases."""
    rollback_errors = rollback_fresh_install(
        plugin_added=state.plugin_added,
        marketplace_added=state.marketplace_added,
    )
    if state.marketplace_replacement_started:
        if state.previous_marketplace_ref is None:
            rollback_errors.append("previous marketplace ref was not preserved")
        else:
            rollback_errors.extend(
                rollback_marketplace_replacement(
                    previous_ref=state.previous_marketplace_ref,
                    replacement_added=state.replacement_marketplace_added,
                    restore_plugin=state.plugin_was_installed
                    and state.plugin_install_attempted,
                    command_path=command_path,
                    runtime_base=runtime_base,
                )
            )
    elif state.plugin_without_marketplace_update and state.plugin_install_attempted:
        if state.previous_marketplace_ref is None:
            rollback_errors.append("previous plugin ref was not preserved")
        else:
            rollback_errors.extend(
                rollback_plugin_without_marketplace(
                    state.previous_marketplace_ref,
                    command_path,
                    runtime_base,
                )
            )
    return rollback_errors


def _install_claude_host(*, update: bool) -> ClaudeHostInstall:
    """Install or update the TurnEcho Claude plugin and marketplace.

    Partial progress is rolled back before any error propagates, so the
    orchestrator only ever rolls back completed host states.
    """
    state = ClaudeHostInstall()
    try:
        marketplace_entries = run_json_list_command(
            ["claude", "plugin", "marketplace", "list", "--json"]
        )
        marketplace = find_claude_marketplace(marketplace_entries)
        if marketplace is None:
            run_checked_command(
                [
                    "claude",
                    "plugin",
                    "marketplace",
                    "add",
                    TURNECHO_MARKETPLACE_SOURCE,
                    "--scope",
                    "user",
                ]
            )
            state.marketplace_added = True
        else:
            validate_claude_marketplace_source(marketplace)
            if update:
                run_checked_command(
                    [
                        "claude",
                        "plugin",
                        "marketplace",
                        "update",
                        TURNECHO_MARKETPLACE_NAME,
                    ]
                )

        plugin_entries = run_json_list_command(["claude", "plugin", "list", "--json"])
        installed_plugin = find_claude_plugin(plugin_entries)
        if installed_plugin is None:
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
            state.plugin_added = True
        elif update:
            run_checked_command(
                [
                    "claude",
                    "plugin",
                    "update",
                    TURNECHO_PLUGIN_SELECTOR,
                    "--scope",
                    "user",
                ]
            )
        if installed_plugin is None or update:
            plugin_entries = run_json_list_command(
                ["claude", "plugin", "list", "--json"]
            )
            installed_plugin = find_claude_plugin(plugin_entries)
            if installed_plugin is None:
                raise InstallError("Claude did not report TurnEcho as installed.")

        state.version = resolve_claude_plugin_version(installed_plugin)
        state.plugin_root = resolve_claude_plugin_root(installed_plugin)
        return state
    except Exception as error:
        rollback_errors = _rollback_claude_host(state)
        _raise_with_rollback_errors(error, rollback_errors)


def _rollback_claude_host(state: ClaudeHostInstall) -> list[str]:
    """Remove Claude state created by this run.

    Updates need no host rollback: nothing is removed, so a failure leaves the
    previous plugin installed and working while the runtime is restored.
    """
    rollback_errors: list[str] = []
    if state.plugin_added:
        try:
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
        except Exception as error:
            rollback_errors.append(f"plugin removal: {error}")
    if state.marketplace_added:
        try:
            run_checked_command(
                [
                    "claude",
                    "plugin",
                    "marketplace",
                    "remove",
                    TURNECHO_MARKETPLACE_NAME,
                ]
            )
        except Exception as error:
            rollback_errors.append(f"marketplace removal: {error}")
    return rollback_errors


def install_plugin(
    *,
    update: bool = False,
    command_path: Path = DEFAULT_COMMAND_PATH,
    runtime_base: Path | None = None,
    hosts: Sequence[str] = (CODEX_HOST,),
) -> Path:
    """Preflight dependencies, install TurnEcho, and prepare its stable runtime.

    The default installs into Codex only. Pass explicit hosts (main() passes
    detect_hosts()) to cover Claude Code as well. Every host's installed
    version must match this release; any failure rolls every host back so no
    half-installed state remains.
    """
    host_list = normalize_hosts(hosts)
    if not host_list:
        raise InstallError(
            "Neither the 'codex' nor the 'claude' command was found. "
            "Install Codex or Claude Code, or pass --host to select one explicitly."
        )
    require_command("uv")
    for host in host_list:
        require_command(HOST_COMMANDS[host])

    # This process is launched by uvx, so imports fail before a host is changed.
    try:
        validate_runtime_dependencies()
    except Exception as error:
        raise InstallError(f"Audio runtime preflight failed: {error}") from error

    runtime_base = (
        resolve_runtime_base_directory()
        if runtime_base is None
        else runtime_base.expanduser().resolve()
    )
    codex_state: CodexHostInstall | None = None
    claude_state: ClaudeHostInstall | None = None
    command_link_state: CommandLinkState | None = None
    runtime_state: RuntimeInstallState | None = None

    try:
        for host in host_list:
            if host == CODEX_HOST:
                codex_state = _install_codex_host(
                    update=update,
                    command_path=command_path,
                    runtime_base=runtime_base,
                )
            else:
                claude_state = _install_claude_host(update=update)

        first_state = codex_state if codex_state is not None else claude_state
        if (
            first_state is None
            or first_state.plugin_root is None
            or first_state.version is None
        ):
            raise InstallError("TurnEcho host installation did not complete.")
        runtime_state = prepare_installed_runtime(
            first_state.plugin_root,
            first_state.version,
            runtime_base,
        )
        command_link_state = install_cli_command(
            runtime_state.runtime_root,
            command_path,
            managed_cache_root=runtime_base,
            managed_cache_roots=(_codex_plugin_version_root(),),
        )
    except Exception as error:
        command_rollback_error: Exception | None = None
        if command_link_state is not None:
            try:
                restore_cli_command(command_link_state)
            except Exception as rollback_error:
                command_rollback_error = rollback_error
        if runtime_state is not None:
            try:
                rollback_runtime_install(runtime_state)
            except Exception as rollback_error:
                if command_rollback_error is None:
                    command_rollback_error = rollback_error
        rollback_errors: list[str] = []
        if claude_state is not None:
            rollback_errors.extend(_rollback_claude_host(claude_state))
        if codex_state is not None:
            rollback_errors.extend(
                _rollback_codex_host(
                    codex_state,
                    command_path=command_path,
                    runtime_base=runtime_base,
                )
            )
        if command_rollback_error is not None:
            rollback_errors.insert(0, f"command restoration: {command_rollback_error}")
        _raise_with_rollback_errors(error, rollback_errors)

    if runtime_state is None:
        raise InstallError("TurnEcho runtime installation did not complete.")
    commit_runtime_install(runtime_state)
    return runtime_state.runtime_root


def parse_args() -> argparse.Namespace:
    """Parse installer options."""
    parser = argparse.ArgumentParser(
        description="Install TurnEcho with required audio dependency preflight."
    )
    action = parser.add_mutually_exclusive_group()
    action.add_argument(
        "--update",
        action="store_true",
        help="Refresh the marketplace release on each host and reinstall TurnEcho.",
    )
    action.add_argument(
        "--uninstall",
        action="store_true",
        help="Remove the GitHub plugin, marketplace, runtime, and managed command.",
    )
    parser.add_argument(
        "--host",
        choices=[CODEX_HOST, CLAUDE_HOST],
        default=None,
        help=(
            "Install into one host instead of every detected host. "
            "Cannot be combined with --uninstall."
        ),
    )
    return parser.parse_args()


def main() -> int:
    """Install TurnEcho and report a concise result for terminal users."""
    args = parse_args()
    if args.uninstall and args.host is not None:
        print(
            "TurnEcho installation failed: --host cannot be combined with --uninstall.",
            file=sys.stderr,
        )
        return 2
    hosts = [args.host] if args.host is not None else detect_hosts()
    try:
        if args.uninstall:
            command_removed, runtime_count = uninstall_plugin(hosts=hosts)
            for host in hosts:
                print(
                    f"Removed the TurnEcho plugin and marketplace from "
                    f"{HOST_DISPLAY_NAMES[host]}."
                )
            if not hosts:
                print("No supported host detected; skipped host plugin removal.")
            print(f"Removed {runtime_count} managed TurnEcho runtime(s).")
            if command_removed:
                print(f"Removed the TurnEcho command at {DEFAULT_COMMAND_PATH}")
            else:
                print(
                    f"Left the command path unchanged because it was not a "
                    f"TurnEcho-managed link: {DEFAULT_COMMAND_PATH}"
                )
            return 0
        plugin_root = install_plugin(update=args.update, hosts=hosts)
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
    for host in hosts:
        if host == CODEX_HOST:
            print("Start a new Codex thread before testing the plugin.")
            print("If prompted, review and trust the plugin hook with /hooks.")
        else:
            print("Start a new Claude Code session before testing the plugin.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
