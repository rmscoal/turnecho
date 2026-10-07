//go:build !sherpa || !cgo || (!darwin && !linux)

package tts

import (
	"strings"
	"testing"
)

func TestUnavailableRuntime(t *testing.T) {
	engine, err := Open("kokoro")
	if engine != nil || err == nil || !strings.Contains(err.Error(), "-tags sherpa") {
		t.Fatalf("engine=%v error=%v", engine, err)
	}
}
