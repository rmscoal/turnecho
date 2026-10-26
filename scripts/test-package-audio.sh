#!/usr/bin/env bash
set -euo pipefail
# Integration check: requires a real local model, never downloads or plays audio.
model=${1:?Usage: test-package-audio.sh MODEL_DIR}
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
output=${2:-$scratch/build}
./scripts/package-audio.sh "$model" "$output"
mkdir "$scratch/moved"
tar -xzf "$output/turnecho-$(go env GOOS)-$(go env GOARCH).tar.gz" -C "$scratch/moved"
bundle=$(find "$scratch/moved" -mindepth 1 -maxdepth 1 -type d)
(cd "$bundle" && shasum -a 256 -c SHA256SUMS >/dev/null)
mkdir "$scratch/home"
env -u TURNECHO_MODEL_DIR HOME="$scratch/home" DYLD_PRINT_LIBRARIES=1 \
 "$bundle/bin/turnecho" say 'The bundled runtime works.' --output "$scratch/audio.wav" 2>"$scratch/loader.log"
test -s "$scratch/audio.wav"
test "$(printf '%s' '{"hook_event_name":"Stop","session_id":"s","turn_id":"t","last_assistant_message":"No marker."}' | HOME="$scratch/home" "$bundle/bin/turnecho" hook stop)" = '{}'

file "$scratch/audio.wav" | grep -q '24000 Hz'
if grep -q 'pkg/mod.*sherpa' "$scratch/loader.log"; then
 echo 'Loaded libraries from module cache' >&2
 exit 1
fi
case $(go env GOOS) in
 darwin) grep -q "$bundle/lib/libonnxruntime.dylib" "$scratch/loader.log" ;;
 linux) ldd "$bundle/bin/turnecho" | grep -F "$bundle/bin/../lib/libonnxruntime.so" ;;
esac
echo 'Relocated native bundle passed'
