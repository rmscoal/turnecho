# Native speech development

Step 6 uses Kokoro English v0.19 (FP32) through sherpa-onnx Go v1.13.8.
Speech runs locally with no Python interpreter or network access. The model
and native libraries must already be available before speech commands run.
The installer and distributable runtime packaging are Step 7 work.

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
The resulting binary is not yet portable to a machine without those libraries.
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

Once loaded, the worker keeps one model across chunks and queued jobs. It
re-reads the model setting before each job; a model change loads a replacement
and closes the previous model. Voice and speed remain the snapshots stored
with each queued job.

After the last job finishes, the worker waits for up to **10 minutes of
inactivity**, then closes the model and exits. New jobs restart that idle
period. Synthesis and playback time do not count as inactivity. Polling is
every 250 ms, so exit may occur up to one poll after the timeout.

Long text is split at sentence boundaries, with a fallback at word boundaries
around 300 characters. A single longer word is kept intact. Each chunk is
synthesized and played in order before the next chunk begins. This starts
speech before all response audio is synthesized, but there can be synthesis gaps
between chunks. Overlapping synthesis and playback is planned for Step 8.
The worker holds only one chunk of audio at a time. `say --output` assembles
all chunks into one WAV file in memory.

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

If native loading fails, check that `model.onnx` is the FP32 English v0.19
model, all companion files were extracted, and the module-cache native
libraries still exist. Release binaries will need bundled library paths;
the development module-cache path is not a distribution strategy.
