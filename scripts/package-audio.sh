#!/usr/bin/env bash
set -euo pipefail
# Host-native packaging. Runtime users need neither Go nor Python.
model=${1:?Usage: package-audio.sh MODEL_DIR [OUTPUT_DIR]}
output=${2:-dist}
version=${TURNECHO_VERSION:-dev}
case "$version" in *[!a-zA-Z0-9._-]*|'') echo 'Invalid version' >&2; exit 1 ;; esac
for name in model.onnx voices.bin tokens.txt; do test -s "$model/$name"; done
test -d "$model/espeak-ng-data"
test ! -L "$model"
if test -n "$(find "$model" -type l -print)"; then echo 'Model symlinks are not allowed' >&2; exit 1; fi
os=$(go env GOOS)
arch=$(go env GOARCH)
case "$os/$arch" in
 darwin/arm64) module=macos; target=aarch64-apple-darwin; extension=dylib ;;
 darwin/amd64) module=macos; target=x86_64-apple-darwin; extension=dylib ;;
 linux/amd64) module=linux; target=x86_64-unknown-linux-gnu; extension=so ;;
 *) echo "Unsupported native release target: $os/$arch" >&2; exit 1 ;;
esac
if test "$os" = linux; then command -v patchelf >/dev/null; fi
module_dir=$(go list -m -f '{{.Dir}}' "github.com/k2-fsa/sherpa-onnx-go-$module")
mkdir -p "$output"
output=$(cd "$output" && pwd)
scratch=$(mktemp -d "$output/.bundle.XXXXXX")
trap 'rm -rf "$scratch"' EXIT
name="turnecho-$os-$arch"
bundle="$scratch/$name"
mkdir -p "$bundle/bin" "$bundle/lib" "$bundle/share/turnecho/runtimes" "$bundle/licenses"
CGO_ENABLED=1 go build -trimpath -tags sherpa \
 -ldflags "-X github.com/rmscoal/turnecho/internal/version.Version=$version" \
 -o "$bundle/bin/turnecho" ./cmd/turnecho
for lib in libsherpa-onnx-c-api libonnxruntime; do
 cp -L "$module_dir/lib/$target/$lib.$extension" "$bundle/lib/"
done
if test "$os" = darwin; then
 # Strip build-machine rpaths before adding relative bundled paths.
 while IFS= read -r rpath; do
  install_name_tool -delete_rpath "$rpath" "$bundle/bin/turnecho"
 done < <(otool -l "$bundle/bin/turnecho" | awk '/LC_RPATH/{getline;getline;print $2}')
 install_name_tool -add_rpath '@loader_path/../lib' "$bundle/bin/turnecho"
 for lib in "$bundle"/lib/*.dylib; do codesign --force --sign - "$lib"; done
 codesign --force --sign - "$bundle/bin/turnecho"
else
 patchelf --set-rpath '$ORIGIN/../lib' "$bundle/bin/turnecho"
 for lib in "$bundle"/lib/*.so; do
  patchelf --set-rpath '$ORIGIN' "$lib"
  soname=$(patchelf --print-soname "$lib")
  case "$soname" in */*) echo 'Unexpected library SONAME' >&2; exit 1 ;; esac
  if test -n "$soname" && test "$soname" != "$(basename "$lib")"; then
   ln -s "$(basename "$lib")" "$bundle/lib/$soname"
  fi
 done
fi
model_target="$bundle/share/turnecho/runtimes/kokoro-en-v0_19"
mkdir -p "$model_target"
for asset in model.onnx voices.bin tokens.txt espeak-ng-data LICENSE README.md; do
 if test -e "$model/$asset"; then cp -R "$model/$asset" "$model_target/"; fi
done
for component in .codex-plugin .claude-plugin hooks skills assets; do cp -R "$component" "$bundle/"; done
cp LICENSE "$bundle/licenses/TurnEcho-LICENSE"
cp "$module_dir/LICENSE" "$bundle/licenses/sherpa-onnx-LICENSE"
cp docs/speech-runtime.md "$bundle/README.md"
# Internal hashes cover model files as well as the executable and libraries.
(cd "$bundle" && find . -type f ! -name SHA256SUMS -exec shasum -a 256 '{}' \; > SHA256SUMS)
tar -czf "$scratch/$name.tar.gz" -C "$scratch" "$name"
mv "$scratch/$name.tar.gz" "$output/$name.tar.gz"
(cd "$output" && shasum -a 256 "$name.tar.gz" > "$name.tar.gz.sha256")
printf 'Built %s\n' "$output/$name.tar.gz"
