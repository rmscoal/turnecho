"""Install TurnEcho from GitHub only after its audio runtime passes preflight."""

from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from pathlib import Path
from typing import Any

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
from .runtime_preflight import validate_runtime_dependencies


def require_command(command_name: str) -> None:
    """Reject installation before changing Codex when a command is unavailable."""
    if shutil.which(command_name) is None:
        raise InstallError(f"The '{command_name}' command was not found.")


@dataclass(frozen=True)
class RuntimeInstallState:
    """Runtime paths needed to commit or roll back an atomic replacement."""

    runtime_root: Path
    backup_root: Path | None


def run_checked_command(
    command: list[str],
    *,
    environment: dict[str, str] | None = None,
) -> None:
    """Run a command that does not need a parsed response."""
    subprocess.run(command, check=True, env=environment)


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
    """Build and atomically install a non-editable runtime outside Codex's cache."""
    runtime_base = runtime_base.expanduser().resolve()
    runtime_base.mkdir(parents=True, exist_ok=True)
    runtime_root = runtime_base / version
    if runtime_root.exists() and not is_managed_runtime_directory(runtime_root):
        raise InstallError(f"Refusing to replace an unmanaged runtime: {runtime_root}")

    staging_root = Path(
        tempfile.mkdtemp(prefix=f".{version}.install-", dir=runtime_base)
    )
    backup_root: Path | None = None
    try:
        shutil.copy2(plugin_root / "pyproject.toml", staging_root / "pyproject.toml")
        (staging_root / TURNECHO_RUNTIME_MARKER_FILE).write_text(
            json.dumps({"name": TURNECHO_PLUGIN_NAME, "version": version}) + "\n",
            encoding="utf-8",
        )
        environment = os.environ.copy()
        environment["UV_PROJECT_ENVIRONMENT"] = str(staging_root / ".venv")
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
        runtime_python = staging_root / ".venv" / "bin" / "python"
        runtime_command = staging_root / ".venv" / "bin" / "turnecho"
        if not runtime_python.is_file() or not runtime_command.is_file():
            raise InstallError(
                f"TurnEcho runtime commands were not created under {staging_root}"
            )
        run_checked_command([str(runtime_python), "-m", "turnecho.runtime_preflight"])

        if runtime_root.exists():
            backup_root = Path(
                tempfile.mkdtemp(prefix=f".{version}.backup-", dir=runtime_base)
            )
            backup_root.rmdir()
            os.replace(runtime_root, backup_root)
        os.replace(staging_root, runtime_root)
    except Exception:
        if staging_root.exists():
            shutil.rmtree(staging_root)
        if backup_root is not None and backup_root.exists():
            if runtime_root.exists():
                _remove_managed_runtime(runtime_root)
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


def uninstall_plugin(
    *,
    command_path: Path = DEFAULT_COMMAND_PATH,
    runtime_base: Path | None = None,
) -> tuple[bool, int]:
    """Remove Codex state plus only TurnEcho-owned runtime and command files."""
    require_command("codex")
    runtime_base = (
        resolve_runtime_base_directory()
        if runtime_base is None
        else runtime_base.expanduser().resolve()
    )

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

    command_removed = remove_cli_command(
        command_path,
        managed_cache_roots=(runtime_base, _codex_plugin_version_root()),
    )
    runtime_count = remove_managed_runtimes(runtime_base)
    return command_removed, runtime_count


def install_plugin(
    *,
    update: bool = False,
    command_path: Path = DEFAULT_COMMAND_PATH,
    runtime_base: Path | None = None,
) -> Path:
    """Preflight dependencies, install TurnEcho, and prepare its stable runtime."""
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
    runtime_base = (
        resolve_runtime_base_directory()
        if runtime_base is None
        else runtime_base.expanduser().resolve()
    )
    runtime_state: RuntimeInstallState | None = None

    try:
        if marketplace is None:
            if update and installed_plugin is not None:
                previous_marketplace_ref = resolve_plugin_source_ref(installed_plugin)
                if previous_marketplace_ref is None:
                    raise InstallError(
                        "Cannot preserve the installed TurnEcho release before update."
                    )
                plugin_without_marketplace_update = True
            add_marketplace(TURNECHO_MARKETPLACE_REF)
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
                add_marketplace(TURNECHO_MARKETPLACE_REF)
                replacement_marketplace_added = True

        if installed_plugin is None or update:
            plugin_install_attempted = True
            install_payload = run_json_command(
                ["codex", "plugin", "add", TURNECHO_PLUGIN_SELECTOR, "--json"]
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
        version = resolve_installed_plugin_version(installed_plugin)
        runtime_state = prepare_installed_runtime(
            plugin_root,
            version,
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
                        command_path=command_path,
                        runtime_base=runtime_base,
                    )
                )
        elif plugin_without_marketplace_update and plugin_install_attempted:
            if previous_marketplace_ref is None:
                rollback_errors.append("previous plugin ref was not preserved")
            else:
                rollback_errors.extend(
                    rollback_plugin_without_marketplace(
                        previous_marketplace_ref,
                        command_path,
                        runtime_base,
                    )
                )
        if command_rollback_error is not None:
            rollback_errors.insert(0, f"command restoration: {command_rollback_error}")
        if rollback_errors:
            raise InstallError(
                f"Installation failed: {error}. Rollback also failed: "
                + "; ".join(rollback_errors)
            ) from error
        raise

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
        help="Replace the GitHub marketplace ref and reinstall TurnEcho.",
    )
    action.add_argument(
        "--uninstall",
        action="store_true",
        help="Remove the GitHub plugin, marketplace, runtime, and managed command.",
    )
    return parser.parse_args()


def main() -> int:
    """Install TurnEcho and report a concise result for terminal users."""
    args = parse_args()
    try:
        if args.uninstall:
            command_removed, runtime_count = uninstall_plugin()
            print("Removed the TurnEcho GitHub plugin and marketplace.")
            print(f"Removed {runtime_count} managed TurnEcho runtime(s).")
            if command_removed:
                print(f"Removed the TurnEcho command at {DEFAULT_COMMAND_PATH}")
            else:
                print(
                    f"Left the command path unchanged because it was not a "
                    f"TurnEcho-managed link: {DEFAULT_COMMAND_PATH}"
                )
            return 0
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
