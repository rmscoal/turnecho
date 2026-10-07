// Package tts synthesizes speech audio.
//
// Native speech is available in builds tagged sherpa. Unit tests use a
// silent backend without loading native libraries or model files.
package tts

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// SampleRate is the pipeline audio rate in Hz.
const SampleRate = 24000

// Backend synthesizes mono 16-bit samples at SampleRate.
type Backend interface {
	Synthesize(text, voice string, speed float64) ([]int16, error)
}

// Engine owns a loaded model. Close releases its native memory.
type Engine interface {
	Backend
	Close()
}

// SilentBackend returns one second of silence. It keeps queue ownership,
// playback, and CLI tests headless-safe.
type SilentBackend struct{}

// Synthesize implements Backend.
func (SilentBackend) Synthesize(_, _ string, _ float64) ([]int16, error) {
	return make([]int16, SampleRate), nil
}

// Close is a no-op for the test backend.
func (SilentBackend) Close() {}

// WAVWriter streams chunks without retaining the complete audio in memory.
type WAVWriter struct {
	file      *os.File
	dataBytes int
	writeErr  error
}

// CreateWAV opens a mono PCM WAV. Close finalizes its header.
func CreateWAV(path string) (*WAVWriter, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(wavHeader(0)); err != nil {
		file.Close()
		return nil, err
	}
	return &WAVWriter{file: file}, nil
}

// Write appends samples with bounded byte buffers and checks the RIFF limit.
func (w *WAVWriter) Write(samples []int16) error {
	if w.file == nil {
		return os.ErrClosed
	}
	if w.writeErr != nil {
		return w.writeErr
	}
	if uint64(len(samples))*2+uint64(w.dataBytes) > math.MaxUint32-36 {
		return fmt.Errorf("audio exceeds WAV size limit")
	}
	if err := writePCM(w.file, samples); err != nil {
		w.writeErr = err
		return err
	}
	w.dataBytes += len(samples) * 2
	return nil
}

// Close finalizes the header and releases the file, including after failure.
func (w *WAVWriter) Close() error {
	if w.file == nil {
		return nil
	}
	file := w.file
	w.file = nil
	err := w.writeErr
	if err == nil {
		if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
			err = seekErr
		} else {
			_, err = file.Write(wavHeader(w.dataBytes))
		}
	}
	return errors.Join(err, file.Close())
}

// WriteWAV stores mono 16-bit samples as a WAV file.
func WriteWAV(path string, samples []int16) error {
	writer, err := CreateWAV(path)
	if err != nil {
		return err
	}
	err = writer.Write(samples)
	return errors.Join(err, writer.Close())
}

func writePCM(writer io.Writer, samples []int16) error {
	var buffer [32768]byte
	for len(samples) > 0 {
		count := min(len(samples), len(buffer)/2)
		for i, sample := range samples[:count] {
			binary.LittleEndian.PutUint16(buffer[2*i:], uint16(sample))
		}
		written, err := writer.Write(buffer[:count*2])
		if err != nil {
			return err
		}
		if written != count*2 {
			return io.ErrShortWrite
		}
		samples = samples[count:]
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
