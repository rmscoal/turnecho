"""Manual audio diagnostic for TurnEcho's real KittenTTS output.

This file intentionally does not start with ``test_`` so the normal
automated suite (``python -m unittest discover -s tests``) never imports
or runs it. It always loads the real configured KittenTTS model; nothing
here is mocked.

Usage::

    python tests/manual_audio_check.py        # measurements only
    python tests/manual_audio_check.py --play # measurements plus playback
    python tests/manual_audio_check.py --single-stream --idle-wait 300
    # measurements plus one worker-like play() after 5 min of silence

Both modes print one valid JSON document to stdout. Playback progress
and KittenTTS generation messages go to stderr. No files are created.
"""

from __future__ import annotations

import argparse
import contextlib
import json
import math
import sys
import time
from typing import Any

import numpy as np

from turnecho.config import load_config
from turnecho.constant import TURNECHO_AUDIO_SAMPLE_RATE, TURNECHO_MODEL_IDS

# Fixed sentences so measurements can be compared between versions.
DIAGNOSTIC_SENTENCES = (
    "TurnEcho finished checking the project.",
    "The worker processed the queued message and completed audio playback.",
    "Three changes were verified, and no further action is required.",
)

# Mean absolute value above this suggests a DC offset worth flagging.
MEAN_ABSOLUTE_WARNING = 0.005
# Absolute sample at or above this is close enough to full scale to flag.
NEAR_CLIPPING_LEVEL = 0.99
# Peak absolute amplitude above this is structurally unsafe (|x| > 1.0).
PEAK_ERROR_LEVEL = 1.0
# First and last samples used for the start/end edge energy checks.
EDGE_WINDOW_MS = 20
# Edge RMS above this suggests an abrupt tape-style start or end.
EDGE_RMS_WARNING = 0.01
# Share of low-frequency energy above this suggests DC drift or rumble.
LOW_FREQUENCY_RATIO_WARNING = 0.10
# Cutoff for the diagnostic first-order high-pass measurement below.
LOW_FREQUENCY_CUTOFF_HZ = 20.0
# Durations outside this range indicate a broken generation.
MIN_REASONABLE_DURATION_SECONDS = 0.1
MAX_REASONABLE_DURATION_SECONDS = 30.0
# Positive/negative peak ratio below this counts as strongly imbalanced.
PEAK_IMBALANCE_RATIO_WARNING = 0.5
# Peaks below this are treated as near-silence, not imbalance.
PEAK_IMBALANCE_MIN_PEAK = 0.1
# Pause between sentences so boundaries are easy to hear.
INTER_SENTENCE_PAUSE_SECONDS = 0.5
# Decimals kept in the JSON report. Validation uses original precision.
DISPLAY_PRECISION = 6


def calculate_rms(waveform: np.ndarray) -> float:
    """Return the root-mean-square of a waveform, or 0.0 when empty."""
    flat = np.asarray(waveform, dtype=np.float64).ravel()
    if flat.size == 0:
        return 0.0
    return float(np.sqrt(np.mean(np.square(flat))))


def calculate_low_frequency_ratio(waveform: np.ndarray, sample_rate: int) -> float:
    """Estimate the share of very-low-frequency energy in a waveform.

    Applies a temporary first-order 20 Hz high-pass calculation to a copy,
    subtracts it from the original, and reports ``rms(removed) /
    rms(original)``. This is a diagnostic heuristic only: it does not prove
    whether audio sounds good or bad.
    """
    original = np.asarray(waveform, dtype=np.float64).ravel()
    if original.size == 0:
        return 0.0
    original_rms = calculate_rms(original)
    if original_rms <= 0.0 or not math.isfinite(original_rms):
        return 0.0

    # One-pole high-pass: y[i] = alpha * (y[i-1] + x[i] - x[i-1]).
    cutoff = LOW_FREQUENCY_CUTOFF_HZ
    time_constant = 1.0 / (2.0 * math.pi * cutoff)
    step = 1.0 / float(sample_rate)
    alpha = time_constant / (time_constant + step)

    high_passed = np.empty_like(original)
    previous = 0.0
    previous_input = original[0]
    for index, sample in enumerate(original):
        previous = alpha * (previous + sample - previous_input)
        previous_input = sample
        high_passed[index] = previous

    removed_rms = calculate_rms(original - high_passed)
    return float(removed_rms / original_rms)


def _display(value: float) -> float:
    """Round a measurement for the JSON report."""
    return round(float(value), DISPLAY_PRECISION)


def analyze_waveform(text: str, waveform: np.ndarray) -> dict[str, Any]:
    """Measure one generated waveform and classify it as ok/warning/error."""
    audio = np.asarray(waveform)
    sample_rate = TURNECHO_AUDIO_SAMPLE_RATE
    findings: list[str] = []
    status = "ok"

    def flag(level: str, finding: str) -> None:
        nonlocal status
        findings.append(finding)
        if level == "error":
            status = "error"
        elif status != "error":
            status = "warning"

    shape = [int(dim) for dim in audio.shape]
    dtype_name = str(audio.dtype)
    sample_count = int(audio.size)
    duration_seconds = sample_count / float(sample_rate)

    # Structural checks run before any float conversion so odd inputs
    # (empty, multidimensional, non-numeric) cannot raise below.
    if sample_count == 0:
        flag("error", "The waveform is empty.")
    if audio.ndim != 1:
        flag(
            "error",
            f"The waveform has {audio.ndim} dimensions; expected 1.",
        )
    numeric = audio.dtype.kind in ("f", "i", "u")

    values: np.ndarray | None = None
    if numeric and sample_count > 0:
        try:
            values = np.asarray(audio, dtype=np.float64).ravel()
        except (TypeError, ValueError):
            values = None
    if not numeric or (values is None and sample_count > 0):
        flag("error", "The waveform data is non-numeric.")

    if values is not None:
        minimum = float(np.min(values))
        maximum = float(np.max(values))
        peak = max(abs(minimum), abs(maximum))
        rms = calculate_rms(values)
        mean = float(np.mean(values))
        first_sample = float(values[0])
        last_sample = float(values[-1])
        if audio.dtype.kind == "f":
            non_finite = int(np.count_nonzero(~np.isfinite(values)))
        else:
            non_finite = 0
        clipped = int(np.count_nonzero(np.abs(values) > PEAK_ERROR_LEVEL))
        low_frequency_ratio = calculate_low_frequency_ratio(values, sample_rate)
        edge_samples = max(1, int(sample_rate * EDGE_WINDOW_MS / 1000))
        leading_rms = calculate_rms(values[:edge_samples])
        trailing_rms = calculate_rms(values[-edge_samples:])
        near_clipped = int(np.count_nonzero(np.abs(values) >= NEAR_CLIPPING_LEVEL))
    else:
        minimum = maximum = peak = rms = mean = 0.0
        first_sample = last_sample = 0.0
        non_finite = clipped = near_clipped = 0
        low_frequency_ratio = leading_rms = trailing_rms = 0.0

    # Errors: structurally invalid or unsafe waveforms.
    if values is not None:
        if non_finite > 0:
            flag(
                "error",
                f"The waveform contains {non_finite} non-finite sample(s).",
            )
        if peak > PEAK_ERROR_LEVEL:
            flag(
                "error",
                "The peak amplitude exceeds 1.0 and is unsafe for playback.",
            )
        if (
            audio.ndim == 1
            and sample_count > 0
            and not (
                MIN_REASONABLE_DURATION_SECONDS
                <= duration_seconds
                <= MAX_REASONABLE_DURATION_SECONDS
            )
        ):
            flag(
                "error",
                f"The duration {duration_seconds:.3f}s is outside the "
                f"reasonable range "
                f"({MIN_REASONABLE_DURATION_SECONDS}s-"
                f"{MAX_REASONABLE_DURATION_SECONDS}s).",
            )

        # Warnings: suspicious but playable properties.
        if abs(mean) > MEAN_ABSOLUTE_WARNING:
            flag(
                "warning",
                "The absolute mean exceeds the configured warning threshold.",
            )
        if low_frequency_ratio > LOW_FREQUENCY_RATIO_WARNING:
            flag(
                "warning",
                "The low-frequency RMS ratio exceeds the configured warning threshold.",
            )
        if leading_rms > EDGE_RMS_WARNING:
            flag(
                "warning",
                "The leading-edge RMS exceeds the configured warning threshold.",
            )
        if trailing_rms > EDGE_RMS_WARNING:
            flag(
                "warning",
                "The trailing-edge RMS exceeds the configured warning threshold.",
            )
        if near_clipped > 0:
            flag(
                "warning",
                f"{near_clipped} sample(s) are at or near clipping.",
            )
        if peak >= PEAK_IMBALANCE_MIN_PEAK:
            positive_peak = maximum if maximum > 0 else 0.0
            negative_peak = -minimum if minimum < 0 else 0.0
            if positive_peak == 0.0 or negative_peak == 0.0:
                flag(
                    "warning",
                    "The waveform peaks are one-sided.",
                )
            else:
                balance = min(positive_peak, negative_peak) / max(
                    positive_peak, negative_peak
                )
                if balance < PEAK_IMBALANCE_RATIO_WARNING:
                    flag(
                        "warning",
                        "The positive and negative peaks are strongly imbalanced.",
                    )

    return {
        "text": text,
        "sample_count": sample_count,
        "duration_seconds": _display(duration_seconds),
        "shape": shape,
        "dtype": dtype_name,
        "minimum": _display(minimum),
        "maximum": _display(maximum),
        "peak": _display(peak),
        "rms": _display(rms),
        "mean": _display(mean),
        "first_sample": _display(first_sample),
        "last_sample": _display(last_sample),
        "leading_rms": _display(leading_rms),
        "trailing_rms": _display(trailing_rms),
        "non_finite_samples": non_finite,
        "clipped_samples": clipped,
        "low_frequency_rms_ratio": _display(low_frequency_ratio),
        "status": status,
        "findings": findings,
    }


def generate_waveforms(model: Any, voice: str, speed: float) -> list[np.ndarray]:
    """Generate the fixed diagnostic waveforms with the real model."""
    waveforms: list[np.ndarray] = []
    for sentence in DIAGNOSTIC_SENTENCES:
        # Keep stdout valid JSON: KittenTTS progress goes to stderr.
        with contextlib.redirect_stdout(sys.stderr):
            audio = model.generate(sentence, voice=voice, speed=speed)
        waveforms.append(np.asarray(audio))
    return waveforms


def play_waveforms(waveforms: list[np.ndarray]) -> None:
    """Play unmodified waveforms sequentially via the worker's audio path."""
    import sounddevice

    total = len(waveforms)
    for index, audio in enumerate(waveforms):
        print(f"Playing sentence {index + 1} of {total}...", file=sys.stderr)
        sounddevice.play(audio, samplerate=TURNECHO_AUDIO_SAMPLE_RATE)
        sounddevice.wait()
        if index < total - 1:
            time.sleep(INTER_SENTENCE_PAUSE_SECONDS)


def play_single_stream(waveforms: list[np.ndarray]) -> None:
    """Play all waveforms in one stream, exactly like one worker job.

    Mirrors worker run_in_loop: a single generate result goes through one
    play() call followed by one wait(), with no gaps and no fades. The
    plain concatenation also reproduces the hard splice KittenTTS itself
    uses between text chunks.
    """
    import sounddevice

    print("Playing all sentences in one stream...", file=sys.stderr)
    combined = np.concatenate(waveforms, axis=-1)
    sounddevice.play(combined, samplerate=TURNECHO_AUDIO_SAMPLE_RATE)
    sounddevice.wait()


def main(argv: list[str] | None = None) -> int:
    """Run the diagnostic and return the process exit status."""
    parser = argparse.ArgumentParser(
        description="Measure real KittenTTS waveforms and optionally play them."
    )
    parser.add_argument(
        "--play",
        action="store_true",
        help="also play the waveforms through sounddevice",
    )
    parser.add_argument(
        "--single-stream",
        action="store_true",
        help="play all waveforms in one play() call like a single worker job",
    )
    parser.add_argument(
        "--idle-wait",
        type=float,
        default=0.0,
        metavar="SECONDS",
        help="silent wait before playback so the audio device idles "
        "first (try 300 to mimic a worker speaking after quiet)",
    )
    args = parser.parse_args(argv)
    if args.idle_wait < 0:
        parser.error("--idle-wait must not be negative.")

    config = load_config()
    model_id = TURNECHO_MODEL_IDS[config.model]

    try:
        # Imported here so importing this module never loads audio deps.
        from kittentts import KittenTTS

        with contextlib.redirect_stdout(sys.stderr):
            model = KittenTTS(model_id)
        waveforms = generate_waveforms(model, config.voice, config.speed)
    except Exception as error:  # noqa: BLE001 - report, then exit nonzero
        print(f"Failed to generate diagnostic audio: {error}", file=sys.stderr)
        print(json.dumps({"status": "error", "error": str(error)}, indent=2))
        return 1

    sentences = [
        analyze_waveform(text, audio)
        for text, audio in zip(DIAGNOSTIC_SENTENCES, waveforms)
    ]
    statuses = {sentence["status"] for sentence in sentences}
    if "error" in statuses:
        overall_status = "error"
    elif "warning" in statuses:
        overall_status = "warning"
    else:
        overall_status = "ok"

    report = {
        "model": config.model,
        "model_id": model_id,
        "voice": config.voice,
        "speed": config.speed,
        "sample_rate": TURNECHO_AUDIO_SAMPLE_RATE,
        "playback_enabled": bool(args.play or args.single_stream),
        "overall_status": overall_status,
        "sentences": sentences,
    }
    # Print before playback so stdout stays valid JSON even if audio fails.
    print(json.dumps(report, indent=2))
    sys.stdout.flush()

    if args.play or args.single_stream:
        if args.idle_wait > 0:
            print(
                f"Waiting {args.idle_wait:g}s in silence before playback...",
                file=sys.stderr,
            )
            time.sleep(args.idle_wait)
        try:
            if args.single_stream:
                play_single_stream(waveforms)
            else:
                play_waveforms(waveforms)
        except Exception as error:  # noqa: BLE001 - report, then exit nonzero
            print(f"Playback failed: {error}", file=sys.stderr)
            return 1

    return 1 if overall_status == "error" else 0


if __name__ == "__main__":
    raise SystemExit(main())
