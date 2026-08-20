"""Validate TurnEcho's TTS model and default audio output."""

from __future__ import annotations

import importlib
import sys

from .constant import TURNECHO_AUDIO_SAMPLE_RATE, TURNECHO_MODEL_NAME


def validate_runtime_dependencies() -> None:
    """Load the TTS model and validate the default audio output settings."""
    kittentts = importlib.import_module("kittentts")
    sounddevice = importlib.import_module("sounddevice")

    sounddevice.check_output_settings(samplerate=TURNECHO_AUDIO_SAMPLE_RATE)
    kittentts.KittenTTS(TURNECHO_MODEL_NAME)


def main() -> int:
    """Return a failing status when the audio runtime cannot be prepared."""
    try:
        validate_runtime_dependencies()
    except Exception as error:
        print(f"TurnEcho runtime preflight failed: {error}", file=sys.stderr)
        return 1

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
