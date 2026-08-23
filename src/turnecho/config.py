"""Load and update TurnEcho's dependency-free local configuration."""

from __future__ import annotations

import fcntl
import json
import math
import os
import tempfile
from contextlib import contextmanager
from dataclasses import asdict, dataclass, replace
from pathlib import Path
from typing import Any

from .constant import (
    TURNECHO_AVAILABLE_VOICES,
    TURNECHO_CONFIG_FILE_PATH,
    TURNECHO_DEFAULT_SPEED,
    TURNECHO_DEFAULT_VOICE,
    TURNECHO_MAX_SPEED,
    TURNECHO_MIN_SPEED,
)

CONFIG_SCHEMA_VERSION = 1
CONFIG_KEYS = {"schema_version", "enabled", "voice", "speed"}


class ConfigError(ValueError):
    """Raised when TurnEcho configuration is invalid or cannot be updated."""


@dataclass(frozen=True)
class TurnEchoConfig:
    """Validated user-controlled TurnEcho behavior."""

    schema_version: int = CONFIG_SCHEMA_VERSION
    enabled: bool = True
    voice: str = TURNECHO_DEFAULT_VOICE
    speed: float = TURNECHO_DEFAULT_SPEED


def resolve_config_path(path: Path | None = None) -> Path:
    """Return the user configuration path or a supplied test path."""
    if path is not None:
        return path.expanduser()
    return Path(TURNECHO_CONFIG_FILE_PATH).expanduser()


def validate_config(config: TurnEchoConfig) -> TurnEchoConfig:
    """Validate a typed configuration and normalize its numeric speed."""
    if config.schema_version != CONFIG_SCHEMA_VERSION:
        raise ConfigError(
            f"Unsupported configuration schema version: {config.schema_version}"
        )
    if not isinstance(config.enabled, bool):
        raise ConfigError("Configuration field 'enabled' must be a boolean.")
    if config.voice not in TURNECHO_AVAILABLE_VOICES:
        supported = ", ".join(TURNECHO_AVAILABLE_VOICES)
        raise ConfigError(
            f"Unsupported voice '{config.voice}'. Choose from: {supported}"
        )
    if isinstance(config.speed, bool) or not isinstance(config.speed, (int, float)):
        raise ConfigError("Configuration field 'speed' must be a number.")

    speed = float(config.speed)
    if (
        not math.isfinite(speed)
        or not TURNECHO_MIN_SPEED <= speed <= TURNECHO_MAX_SPEED
    ):
        raise ConfigError(
            f"Configuration field 'speed' must be between "
            f"{TURNECHO_MIN_SPEED} and {TURNECHO_MAX_SPEED}."
        )

    return replace(config, speed=speed)


def _config_from_payload(payload: Any) -> TurnEchoConfig:
    if not isinstance(payload, dict):
        raise ConfigError("TurnEcho configuration must be a JSON object.")

    unknown_keys = set(payload) - CONFIG_KEYS
    if unknown_keys:
        names = ", ".join(sorted(unknown_keys))
        raise ConfigError(f"Unknown configuration field(s): {names}")

    missing_keys = CONFIG_KEYS - set(payload)
    if missing_keys:
        names = ", ".join(sorted(missing_keys))
        raise ConfigError(f"Missing configuration field(s): {names}")

    schema_version = payload["schema_version"]
    enabled = payload["enabled"]
    voice = payload["voice"]
    speed = payload["speed"]
    if isinstance(schema_version, bool) or not isinstance(schema_version, int):
        raise ConfigError("Configuration field 'schema_version' must be an integer.")
    if not isinstance(enabled, bool):
        raise ConfigError("Configuration field 'enabled' must be a boolean.")
    if not isinstance(voice, str):
        raise ConfigError("Configuration field 'voice' must be a string.")

    return validate_config(
        TurnEchoConfig(
            schema_version=schema_version,
            enabled=enabled,
            voice=voice,
            speed=speed,
        )
    )


def load_config(path: Path | None = None) -> TurnEchoConfig:
    """Load strict configuration, using defaults only when no file exists."""
    config_path = resolve_config_path(path)
    if not config_path.exists():
        return TurnEchoConfig()

    try:
        with config_path.open(encoding="utf-8") as config_file:
            payload: Any = json.load(config_file)
    except (OSError, json.JSONDecodeError) as error:
        raise ConfigError(f"Cannot read TurnEcho configuration: {error}") from error

    return _config_from_payload(payload)


@contextmanager
def _configuration_lock(config_path: Path):
    config_path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    lock_path = config_path.with_name("config.lock")
    with lock_path.open("a+") as lock_file:
        os.chmod(lock_path, 0o600)
        fcntl.flock(lock_file.fileno(), fcntl.LOCK_EX)
        try:
            yield
        finally:
            fcntl.flock(lock_file.fileno(), fcntl.LOCK_UN)


def _write_config_unlocked(config: TurnEchoConfig, config_path: Path) -> None:
    temporary_path: Path | None = None
    try:
        with tempfile.NamedTemporaryFile(
            "w",
            encoding="utf-8",
            dir=config_path.parent,
            prefix=f".{config_path.name}.",
            delete=False,
        ) as temporary_file:
            temporary_path = Path(temporary_file.name)
            os.chmod(temporary_path, 0o600)
            json.dump(asdict(config), temporary_file, indent=2)
            temporary_file.write("\n")
            temporary_file.flush()
            os.fsync(temporary_file.fileno())

        os.replace(temporary_path, config_path)
        temporary_path = None
    finally:
        if temporary_path is not None:
            temporary_path.unlink(missing_ok=True)


def write_config(config: TurnEchoConfig, path: Path | None = None) -> None:
    """Validate and atomically write configuration under an exclusive lock."""
    config = validate_config(config)
    config_path = resolve_config_path(path)
    with _configuration_lock(config_path):
        _write_config_unlocked(config, config_path)


def update_config(
    *,
    enabled: bool | None = None,
    voice: str | None = None,
    speed: float | None = None,
    path: Path | None = None,
) -> TurnEchoConfig:
    """Update selected fields and persist the complete configuration."""
    config_path = resolve_config_path(path)
    with _configuration_lock(config_path):
        current = load_config(config_path)
        updated = replace(
            current,
            enabled=current.enabled if enabled is None else enabled,
            voice=current.voice if voice is None else voice,
            speed=current.speed if speed is None else speed,
        )
        updated = validate_config(updated)
        _write_config_unlocked(updated, config_path)
    return updated


def reset_config(key: str | None = None, path: Path | None = None) -> TurnEchoConfig:
    """Reset one setting or the complete configuration under one lock."""
    if key not in {None, "enabled", "voice", "speed"}:
        raise ConfigError(f"Unknown reset field: {key}")

    config_path = resolve_config_path(path)
    defaults = TurnEchoConfig()
    with _configuration_lock(config_path):
        if key is None:
            updated = defaults
        else:
            current = load_config(config_path)
            updated = replace(current, **{key: getattr(defaults, key)})
        _write_config_unlocked(updated, config_path)
    return updated
