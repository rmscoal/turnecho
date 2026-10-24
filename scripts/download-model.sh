#!/usr/bin/env bash
set -euo pipefail
# Development-only Kokoro model fetcher.
# Release installs will download into the home runtime dir (Step 7 work).
# This script keeps the model inside the project scratch dir instead.
url=${MODEL_URL:-https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/kokoro-en-v0_19.tar.bz2}
dest=${MODEL_DIR:-.tmp/models/kokoro-en-v0_19}
case "$dest" in
"" | "/" | "$HOME")
  echo "Refusing to install model into unsafe directory: ${dest:-<empty>}" >&2
  exit 1
  ;;
esac
if test "${FORCE:-0}" != 1; then
  ready=1
  for name in model.onnx voices.bin tokens.txt; do test -s "$dest/$name" || ready=0; done
  test -d "$dest/espeak-ng-data" || ready=0
  if test "$ready" = 1; then
    echo "Model already present at $dest"
    case "$dest" in /*) echo "export TURNECHO_MODEL_DIR=$dest" ;; *) echo "export TURNECHO_MODEL_DIR=\$PWD/$dest" ;; esac
    exit 0
  fi
fi
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
curl -fSL "$url" -o "$work/kokoro.tar.bz2"
tar -xjf "$work/kokoro.tar.bz2" -C "$work"
# The archive wraps files in one top-level directory.
top=$(find "$work" -mindepth 1 -maxdepth 1 -type d | head -n 1)
if test -z "$top"; then
  echo "Model archive has no top-level directory: $url" >&2
  exit 1
fi
for name in model.onnx voices.bin tokens.txt; do test -s "$top/$name"; done
test -d "$top/espeak-ng-data"
test -z "$(find "$top" -type l -print)"
rm -rf "$dest"
mkdir -p "$(dirname "$dest")"
mv "$top" "$dest"
shasum -a 256 "$work/kokoro.tar.bz2" | sed 's| .*|  kokoro.tar.bz2|' > "$dest/SOURCE.sha256"
echo "Model ready at $dest"
case "$dest" in /*) echo "export TURNECHO_MODEL_DIR=$dest" ;; *) echo "export TURNECHO_MODEL_DIR=\$PWD/$dest" ;; esac
