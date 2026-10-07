# Native speech development

Step 6 uses Kokoro English v0.19 (FP32) through sherpa-onnx Go v1.13.8.
Speech runs locally with no Python interpreter or network access. The model
and native libraries must already be available before speech commands run.
The installer remains Step 7 work. Native archives can already be built and
verified using the packaging commands below.

## Build

Ordinary `go build ./...` and `make check` require no native speech libraries.
In this build, speech commands fail with an explicit unavailable-runtime error.
Silent audio is used only by tests, never as a production fallback.

For a speech-capable development binary on macOS or Linux:

```sh
make build-audio
```

This runs `CGO_ENABLED=1 go build -tags sherpa -o turnecho ./cmd/turnecho`.
It requires a C compiler. The pinned sherpa Go modules provide the native
libraries, and development builds link against their module-cache locations.
The development binary requires those module-cache libraries. Use the native
archive builder below for a portable bundle.
See [the packaging spike](spike-sherpa-go.md) for the verified macOS recipe.

## Model files

Use the FP32 archive from the official release:

[kokoro-en-v0_19.tar.bz2](https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-en-v0_19.tar.bz2).

Extract it into a local directory containing:

```text
kokoro-en-v0_19/
  model.onnx
  voices.bin
  tokens.txt
  espeak-ng-data/
```

By default TurnEcho looks in
`~/.local/share/turnecho/runtimes/kokoro-en-v0_19/`.
For development, point `TURNECHO_MODEL_DIR` at the extracted directory:

```sh
export TURNECHO_MODEL_DIR=/absolute/path/to/kokoro-en-v0_19
./turnecho doctor --json
./turnecho say "The database connection is ready." --output out.wav
./turnecho test
```

`doctor` loads the model, synthesizes a short phrase, and probes the player
without playing sound. `say --output` writes mono 16-bit, 24 kHz WAV audio.
`test` plays a short phrase. macOS uses `afplay`; Linux requires `aplay`
(usually provided by `alsa-utils`). Missing files and invalid runtime output
fail the command, rather than reporting success with silent audio.

## Model lifecycle and long replies

Hooks never load the model or wait for audio. A worker loads it only after
claiming a job and validating configuration. An empty queue exits immediately
without loading a model.

Manual `say`, `test`, and `doctor` commands take the same ownership lock as
the worker and report a busy error while it owns the model. Ownership is
released only after native teardown completes.

Once loaded, the worker keeps one model across chunks and queued jobs. It
re-reads the model setting before each job; a model change closes the previous model
before loading its replacement. Voice and speed remain the snapshots stored
with each queued job.

After the last job finishes, the worker waits for up to **10 minutes of
inactivity**, then closes the model and exits. New jobs restart that idle
period. Synthesis and playback time do not count as inactivity. Polling is
every 250 ms, so exit may occur up to one poll after the timeout.

Long text is split at sentence boundaries, with a fallback at word boundaries
at 300 Unicode characters. Longer unbroken tokens are split too. Each chunk is
synthesized and played in order before the next chunk begins. This starts
speech before all response audio is synthesized, but there can be synthesis gaps
between chunks. Overlapping synthesis and playback is planned for Step 8.
The worker holds only one chunk of audio at a time. `say --output` streams chunks to a temporary WAV, finalizes its header, and
atomically replaces the output only after successful synthesis. PCM writing
uses 32 KiB buffers rather than one file write per sample.

The hook still speaks validated summary markers only. Full-answer speech and
Markdown sanitization are Step 8 work. The `say` command accepts longer prose
for development and listening tests.

The CPU backend uses at most four threads. FP32 is the initial model because
the macOS spike measured it faster than INT8. Speaker IDs remain `speaker-0`
through `speaker-10`; `speaker-0` is the initial default. Choosing a preferred
voice still needs a listening comparison.

## Verification

```sh
make check
make check-audio
TURNECHO_TEST_MODEL_DIR=/absolute/path/to/kokoro-en-v0_19 \
  go test -tags sherpa ./internal/tts ./internal/worker -run Integration -v
```

Normal tests mock speech and playback, including in the native build. Only
the opt-in integration tests load a real model. They check multiple speakers,
non-silent audio, queued chunk playback, and model cleanup without playing
sound or downloading files.

## Portable native archives

```sh
make package-audio MODEL_DIR=/absolute/path/to/kokoro-en-v0_19
make test-package-audio MODEL_DIR=/absolute/path/to/kokoro-en-v0_19
```

The host-native builder supports macOS arm64/amd64 and Linux amd64. It requires
Go, a C compiler, and macOS signing tools or Linux `patchelf`. It replaces the
starter GoReleaser configuration, which could produce binaries without speech.
Archives in `dist/` contain `bin/turnecho`, `lib/`, the pinned model under
`share/turnecho/runtimes/`, plugin metadata, hooks, skills, and licenses.
`SHA256SUMS` covers bundle files; a separate checksum covers the archive.
Set `TURNECHO_VERSION` to embed a version (default `dev`).

After extracting, run `bin/turnecho` directly. Keep the directory together.
The binary finds the bundled model relative to its real executable path,
including when invoked through a symlink. `TURNECHO_MODEL_DIR` still overrides
that location. No Go, Python, module cache, or runtime download is needed.
Audio playback still requires the system's `afplay` or Linux `aplay`.
macOS uses ad hoc signatures; Developer ID signing and notarization remain
release work. Installer registration and upgrade commands are also planned.

The relocation test checks all file hashes, synthesizes a real WAV using an
isolated HOME, and verifies bundled library loading. CI performs this check on
macOS and Linux using the SHA256-pinned official model archive. Model licenses
are copied with the model, and the sherpa license accompanies the libraries.
Full third-party redistribution notices must be checked before publishing a
binary release.

## Queue recovery

The owner checks for new work after model teardown and lock release, including
work submitted during shutdown. Manual speech also restarts pending jobs after
releasing ownership. Completion-write errors stop the worker and are reported.

Before starting the first audio chunk, the worker persists a playback intent.
Recovery requeues jobs that had not reached playback. Jobs with a playback
intent become failed with an interruption reason, so previously heard audio
is not repeated. A crash between the intent and playback can therefore lose
speech. Exactly-once audio delivery cannot be guaranteed across a process crash.

## Playback control

Playback has a deadline of the WAV duration plus 30 seconds, capped at ten
minutes (five minutes if the header cannot be read). A timeout or `stop`
cancels and reaps the owned player process group. Playback is serialized by
its own lock. `stop` uses a private local Unix socket and never signals a PID
read from disk. Legacy `player.pid` files are ignored.

Local state directories are restricted to their owner (0700), including
existing installations. Queue, sidecar, lock, and log files use 0600. State
opens reject symlinks, nonregular files, and files owned by another user.

Hook input is limited to 8 MiB. Oversized input is rejected with the normal
empty JSON response and a diagnostic that contains no message text. Every
synthesis chunk is limited to 300 Unicode characters, including long tokens.
