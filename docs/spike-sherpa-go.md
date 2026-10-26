# Spike Report: sherpa-onnx Go API + Kokoro (Step 4)

Date: 2026-09-19. Machine: macOS arm64. Pinned: sherpa-onnx v1.13.8,
`sherpa-onnx-go` v1.13.8, models `kokoro-en-v0_19` (fp32) and
`kokoro-int8-en-v0_19` (int8). Scratch lives in /tmp/spike (not committed);
wavs there can be ear-checked with afplay.

## Verdict: native Go package (primary). Gate PASSED.

Build works with plain `go build` (CGO on, ~5s first build, 2.9MB binary).
Runtime shipping is proven: delete the build-machine rpath, add
`@loader_path/../lib`, ad-hoc re-sign, and the binary loads its bundled
dylibs with no module cache involvement (confirmed via DYLD_PRINT_LIBRARIES).

## Measurements (threads=4 unless noted)

```text
model  cold-load   short 2.3s audio      long 47s audio       peak RSS
fp32   0.61s       1.04s wall (RTF 0.46) 16.5s wall (RTF 0.35) 803MB
int8   0.68s       2.73s wall (RTF 1.18) 44.3s wall (RTF 0.94) 642MB
int8t1 -           3.55s wall (RTF 1.54) 58.8s wall (RTF 1.25) 646MB
fp32t1 -           3.31s wall (RTF 1.45) 54.7s wall (RTF 1.16) 803MB
CLI    ~5.3s wall for the short sentence (RTF 1.44 + process/model load)
```

- fp32 is ~2.7x faster than int8 on this CPU: ship fp32. int8 saves size
  but costs both speed and quality here.
- 4 threads vs 1 is ~3x faster: the worker must set NumThreads (use
  min(CPU, 4)? tune later; 4 measured well).
- Output is 24kHz 16-bit mono (384kbps), matching the v1 pipeline.
- sids 0 and 1 both synthesize; multi-voice confirmed.

## Sizes

```text
download: fp32 model 304MB (int8 98MB) + engine lib 8MB (shared-lib tarball)
installed: fp32 model dir 361MB (int8 152MB) + 2 dylibs 33MB + binary ~3MB
model dir contents: model.onnx (fp32) or model.int8.onnx, voices.bin 5.7MB,
  tokens.txt, espeak-ng-data 18MB, LICENSE (Apache 2.0), README
```

## Shipping recipe (proven on a copy in /tmp/spike/ship)

```sh
cp turnecho ship/bin/
cp <modcache>/sherpa-onnx-go-macos@<v>/lib/aarch64-apple-darwin/*.dylib ship/lib/
install_name_tool -delete_rpath <modcache-lib-path> -add_rpath @loader_path/../lib ship/bin/turnecho
codesign -f -s - ship/bin/turnecho
```

Only 2 dylibs are needed (`libsherpa-onnx-c-api`, `libonnxruntime`); the cxx
dylib is not linked. Linux equivalent (chrpath/RPATH `$ORIGIN`) still to
prove in CI. Windows is out of scope.

## CLI fallback, quantified

`sherpa-onnx-offline-tts` speaks the short sentence in 5.3s wall vs 1.04s
native (fp32/4 threads), and reloads the 346MB model per process. Viable as
an emergency fallback, 5x slower per chunk. Not recommended.

## Open items (not gate-blocking)

- Ear-check fp32 vs int8 wavs in /tmp/spike/prog before locking the default
  (recommendation: fp32).
- espeak-ng-data ships with no separate license file (weights LICENSE is
  Apache 2.0); confirm redistribution terms before release.
- Linux rpath proof and Gatekeeper behavior for downloaded dylibs still to
  verify (Step 7 install work / CI).
- Thread count policy (fixed 4 vs NumCPU-capped) to tune in Step 6.
