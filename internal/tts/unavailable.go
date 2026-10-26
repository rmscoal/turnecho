//go:build !sherpa || !cgo || (!darwin && !linux)

package tts

import "fmt"

// Open fails clearly in builds without native audio support.
func Open(_ string) (Engine, error) {
	return nil, unavailableError()
}

// OpenDir fails the same way: there is no native runtime to load.
func OpenDir(_ string) (Engine, error) {
	return nil, unavailableError()
}

func unavailableError() error {
	return fmt.Errorf("native speech is unavailable: " +
		"build with CGO_ENABLED=1 and -tags sherpa (see docs/speech-runtime.md)")
}
