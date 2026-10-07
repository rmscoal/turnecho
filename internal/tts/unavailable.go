//go:build !sherpa || !cgo || (!darwin && !linux)

package tts

import "fmt"

// Open fails clearly in builds without native audio support.
func Open(_ string) (Engine, error) {
	return nil, fmt.Errorf("native speech is unavailable: build with CGO_ENABLED=1 and -tags sherpa (see docs/speech-runtime.md)")
}
