"""Deterministic command-line configuration for TurnEcho."""

from __future__ import annotations

import argparse
import importlib
import json
import os
import sys
import tempfile
import tomllib
from dataclasses import asdict, dataclass
from importlib.metadata import PackageNotFoundError, version
from pathlib import Path
from typing import Sequence

from .config import (
    ConfigError,
    TurnEchoConfig,
    load_config,
    reset_config,
    resolve_config_path,
    update_config,
)
from .constant import (
    TURNECHO_AUDIO_SAMPLE_RATE,
    TURNECHO_AVAILABLE_VOICES,
    TURNECHO_DEFAULT_MODEL,
    TURNECHO_DEFAULT_VOICE,
    TURNECHO_MODEL_IDS,
)

TEST_PHRASE = "TurnEcho is configured and ready."
DEFAULT_COMMAND_PATH = Path.home() / ".local" / "bin" / "turnecho"


class CommandInstallError(RuntimeError):
    """Raised when the installer cannot safely manage the CLI command."""


@dataclass(frozen=True)
class CommandLinkState:
    """Previous command state used for installation rollback."""

    path: Path
    original_target: str | None
    installed_target: Path
    changed: bool


def _command_source(plugin_root: Path) -> Path:
    return plugin_root / ".venv" / "bin" / "turnecho"


def _is_turnecho_environment_command(target: Path) -> bool:
    if target.name != "turnecho" or target.parent.name != "bin":
        return False
    if target.parent.parent.name != ".venv":
        return False

    pyproject_path = target.parent.parent.parent / "pyproject.toml"
    if not pyproject_path.is_file():
        return False
    try:
        with pyproject_path.open("rb") as pyproject_file:
            pyproject = tomllib.load(pyproject_file)
    except (OSError, tomllib.TOMLDecodeError):
        return False
    project = pyproject.get("project")
    return isinstance(project, dict) and project.get("name") == "turnecho"


def _is_missing_managed_cache_command(target: Path, cache_root: Path) -> bool:
    """Recognize a missing versioned command under TurnEcho's Codex cache."""
    if target.exists():
        return False

    try:
        relative_target = target.relative_to(cache_root.expanduser().resolve())
    except ValueError:
        return False

    return (
        len(relative_target.parts) == 4
        and relative_target.parts[0] not in {"", ".", ".."}
        and relative_target.parts[1:] == (".venv", "bin", "turnecho")
    )


def install_cli_command(
    plugin_root: Path,
    command_path: Path = DEFAULT_COMMAND_PATH,
    *,
    managed_cache_root: Path | None = None,
) -> CommandLinkState:
    """Atomically point the user command at the installed plugin runtime."""
    plugin_root = plugin_root.expanduser().resolve()
    source = _command_source(plugin_root)
    if not source.is_file():
        raise CommandInstallError(f"TurnEcho command was not created at {source}")

    command_path = command_path.expanduser()
    original_target: str | None = None
    if command_path.exists() or command_path.is_symlink():
        if not command_path.is_symlink():
            raise CommandInstallError(
                f"Refusing to replace an unrelated command: {command_path}"
            )
        original_target = os.readlink(command_path)
        current_target = (command_path.parent / original_target).resolve()
        if current_target == source.resolve():
            return CommandLinkState(command_path, original_target, source, False)
        is_missing_managed_command = (
            managed_cache_root is not None
            and _is_missing_managed_cache_command(
                current_target,
                managed_cache_root,
            )
        )
        if (
            not _is_turnecho_environment_command(current_target)
            and not is_missing_managed_command
        ):
            raise CommandInstallError(
                f"Refusing to replace an unmanaged command link: {command_path}"
            )

    command_path.parent.mkdir(parents=True, exist_ok=True)
    temporary_path: Path | None = None
    try:
        file_descriptor, temporary_name = tempfile.mkstemp(
            dir=command_path.parent,
            prefix=f".{command_path.name}.",
        )
        os.close(file_descriptor)
        temporary_path = Path(temporary_name)
        temporary_path.unlink()
        temporary_path.symlink_to(source)
        os.replace(temporary_path, command_path)
        temporary_path = None
    finally:
        if temporary_path is not None:
            temporary_path.unlink(missing_ok=True)

    return CommandLinkState(command_path, original_target, source, True)


def restore_cli_command(state: CommandLinkState) -> None:
    """Restore the command link captured before an installation attempt."""
    if not state.changed:
        return
    if not state.path.is_symlink() or state.path.resolve() != state.installed_target:
        raise CommandInstallError(
            f"Refusing to replace a command changed during installation: {state.path}"
        )
    state.path.unlink(missing_ok=True)
    if state.original_target is not None:
        state.path.parent.mkdir(parents=True, exist_ok=True)
        state.path.symlink_to(state.original_target)


def command_directory_is_on_path(command_path: Path = DEFAULT_COMMAND_PATH) -> bool:
    """Return whether the command directory appears in the current PATH."""
    path_entries = os.environ.get("PATH", "").split(os.pathsep)
    command_directory = command_path.expanduser().parent.resolve()
    return any(
        Path(entry).expanduser().resolve() == command_directory
        for entry in path_entries
        if entry
    )


def _package_version() -> str:
    try:
        return version("turnecho")
    except PackageNotFoundError:
        return "development"


def _print_config(config: TurnEchoConfig, *, as_json: bool) -> None:
    payload = asdict(config)
    if as_json:
        print(json.dumps(payload, sort_keys=True))
        return

    print(f"enabled: {str(config.enabled).lower()}")
    print(f"model: {config.model}")
    print(f"voice: {config.voice}")
    print(f"speed: {config.speed:g}")


def _config_show(args: argparse.Namespace) -> None:
    _print_config(load_config(), as_json=args.json)


def _config_path(_: argparse.Namespace) -> None:
    print(resolve_config_path())


def _config_set(args: argparse.Namespace) -> None:
    if args.key == "model":
        config = update_config(model=args.value)
    elif args.key == "voice":
        config = update_config(voice=args.value)
    else:
        try:
            speed = float(args.value)
        except ValueError as error:
            raise ConfigError("Speed must be a number.") from error
        config = update_config(speed=speed)
    _print_config(config, as_json=False)


def _config_reset(args: argparse.Namespace) -> None:
    config = reset_config(None if args.all else args.key)
    _print_config(config, as_json=False)


def _set_enabled(enabled: bool) -> None:
    config = update_config(enabled=enabled)
    print("TurnEcho enabled." if config.enabled else "TurnEcho disabled.")


def _voices(args: argparse.Namespace) -> None:
    if args.json:
        print(json.dumps({"voices": list(TURNECHO_AVAILABLE_VOICES)}))
        return
    for voice in TURNECHO_AVAILABLE_VOICES:
        suffix = " (default)" if voice == TURNECHO_DEFAULT_VOICE else ""
        print(f"{voice}{suffix}")


def _models(args: argparse.Namespace) -> None:
    if args.json:
        payload = {
            "default": TURNECHO_DEFAULT_MODEL,
            "models": dict(TURNECHO_MODEL_IDS),
        }
        print(json.dumps(payload, sort_keys=True))
        return
    for model, model_id in TURNECHO_MODEL_IDS.items():
        suffix = " (default)" if model == TURNECHO_DEFAULT_MODEL else ""
        print(f"{model}{suffix}: {model_id}")


def _doctor(args: argparse.Namespace) -> None:
    config = load_config()
    kittentts = importlib.import_module("kittentts")
    sounddevice = importlib.import_module("sounddevice")
    model_id = TURNECHO_MODEL_IDS[config.model]
    sounddevice.check_output_settings(samplerate=TURNECHO_AUDIO_SAMPLE_RATE)
    kittentts.KittenTTS(model_id)
    payload = {
        "status": "ok",
        "config_path": str(resolve_config_path()),
        "model": config.model,
        "model_id": model_id,
        "voice": config.voice,
        "speed": config.speed,
        "audio_sample_rate": TURNECHO_AUDIO_SAMPLE_RATE,
    }
    if args.json:
        print(json.dumps(payload, sort_keys=True))
    else:
        print("TurnEcho configuration and audio runtime are ready.")


def _test_audio(_: argparse.Namespace) -> None:
    config = load_config()
    kittentts = importlib.import_module("kittentts")
    sounddevice = importlib.import_module("sounddevice")
    model = kittentts.KittenTTS(TURNECHO_MODEL_IDS[config.model])
    audio = model.generate(TEST_PHRASE, voice=config.voice, speed=config.speed)
    sounddevice.play(audio, samplerate=TURNECHO_AUDIO_SAMPLE_RATE)
    sounddevice.wait()
    print("TurnEcho audio test completed.")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="turnecho",
        description="Configure TurnEcho plugin.",
    )
    parser.add_argument("--version", action="version", version=_package_version())
    subparsers = parser.add_subparsers(dest="command", required=True)

    config_parser = subparsers.add_parser("config", help="Manage configuration.")
    config_subparsers = config_parser.add_subparsers(
        dest="config_command", required=True
    )

    show_parser = config_subparsers.add_parser("show", help="Show configuration.")
    show_parser.add_argument("--json", action="store_true")
    show_parser.set_defaults(handler=_config_show)

    path_parser = config_subparsers.add_parser("path", help="Show config path.")
    path_parser.set_defaults(handler=_config_path)

    set_parser = config_subparsers.add_parser("set", help="Set one value.")
    set_parser.add_argument("key", choices=("model", "voice", "speed"))
    set_parser.add_argument("value")
    set_parser.set_defaults(handler=_config_set)

    reset_parser = config_subparsers.add_parser("reset", help="Reset values.")
    reset_target = reset_parser.add_mutually_exclusive_group(required=True)
    reset_target.add_argument(
        "key", nargs="?", choices=("enabled", "model", "voice", "speed")
    )
    reset_target.add_argument("--all", action="store_true")
    reset_parser.set_defaults(handler=_config_reset)

    enable_parser = subparsers.add_parser("enable", help="Enable future summaries.")
    enable_parser.set_defaults(handler=lambda _: _set_enabled(True))
    disable_parser = subparsers.add_parser("disable", help="Disable future summaries.")
    disable_parser.set_defaults(handler=lambda _: _set_enabled(False))

    voices_parser = subparsers.add_parser("voices", help="List supported voices.")
    voices_parser.add_argument("--json", action="store_true")
    voices_parser.set_defaults(handler=_voices)

    models_parser = subparsers.add_parser("models", help="List supported models.")
    models_parser.add_argument("--json", action="store_true")
    models_parser.set_defaults(handler=_models)

    doctor_parser = subparsers.add_parser("doctor", help="Check local runtime.")
    doctor_parser.add_argument("--json", action="store_true")
    doctor_parser.set_defaults(handler=_doctor)

    test_parser = subparsers.add_parser("test", help="Speak a test phrase.")
    test_parser.set_defaults(handler=_test_audio)
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        args.handler(args)
    except ConfigError as error:
        print(f"TurnEcho configuration error: {error}", file=sys.stderr)
        return 2
    except Exception as error:
        print(f"TurnEcho command failed: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
