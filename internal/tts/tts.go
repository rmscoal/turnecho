// Package tts synthesizes speech audio.
//
// Step 5 ships only a silent stub backend so the worker, player, and CLI
// paths run end to end without a model. Step 6 replaces it with sherpa.
package tts

import (
	"encoding/binary"
	"os"
)

// SampleRate is the pipeline audio rate in Hz.
const SampleRate = 24000

// Backend synthesizes mono 16-bit samples at SampleRate.
type Backend interface {
	Synthesize(text, voice string, speed float64) ([]int16, error)
}

// SilentBackend returns one second of silence. It keeps queue ownership,
// playback, and CLI tests headless-safe.
type SilentBackend struct{}

// Synthesize implements Backend.
func (SilentBackend) Synthesize(_, _ string, _ float64) ([]int16, error) {
	return make([]int16, SampleRate), nil
}

// WriteWAV stores mono 16-bit samples as a WAV file.
func WriteWAV(path string, samples []int16) (err error) {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
	}()
	dataBytes := len(samples) * 2
	if _, err := file.Write(wavHeader(dataBytes)); err != nil {
		return err
	}
	buffer := make([]byte, 2)
	for _, sample := range samples {
		binary.LittleEndian.PutUint16(buffer, uint16(sample))
		if _, err := file.Write(buffer); err != nil {
			return err
		}
	}
	return nil
}

func wavHeader(dataBytes int) []byte {
	header := make([]byte, 44)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+dataBytes))
	copy(header[8:12], "WAVE")
	copy(header[12:16], "fmt ")
	binary.LittleEndian.PutUint32(header[16:20], 16)
	binary.LittleEndian.PutUint16(header[20:22], 1)
	binary.LittleEndian.PutUint16(header[22:24], 1)
	binary.LittleEndian.PutUint32(header[24:28], SampleRate)
	binary.LittleEndian.PutUint32(header[28:32], SampleRate*2)
	binary.LittleEndian.PutUint16(header[32:34], 2)
	binary.LittleEndian.PutUint16(header[34:36], 16)
	copy(header[36:40], "data")
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataBytes))
	return header
}
