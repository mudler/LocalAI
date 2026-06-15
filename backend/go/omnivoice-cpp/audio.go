package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"runtime"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

const omnivoiceSampleRate = 24000

// wavHeader24k returns a 44-byte WAV header for a streaming 24 kHz mono 16-bit
// PCM stream, with placeholder (0xFFFFFFFF) sizes since the total length is
// unknown up front. Emitted as the first chunk of TTSStream so the HTTP layer
// receives a self-describing WAV (the gRPC TTSStream path never sets Message,
// so the backend owns the header - see core/backend/tts.go:ModelTTSStream).
func wavHeader24k() []byte {
	var buf bytes.Buffer
	w := func(v any) { _ = binary.Write(&buf, binary.LittleEndian, v) }
	buf.WriteString("RIFF")
	w(uint32(0xFFFFFFFF))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	w(uint32(16))                      // Subchunk1Size
	w(uint16(1))                       // PCM
	w(uint16(1))                       // mono
	w(uint32(omnivoiceSampleRate))     // sample rate
	w(uint32(omnivoiceSampleRate * 2)) // byte rate = SR * blockAlign
	w(uint16(2))                       // block align (16-bit mono)
	w(uint16(16))                      // bits per sample
	buf.WriteString("data")
	w(uint32(0xFFFFFFFF))
	return buf.Bytes()
}

// floatToPCM16LE clamps each sample to [-1,1] and encodes it as little-endian
// signed 16-bit PCM.
func floatToPCM16LE(samples []float32) []byte {
	out := make([]byte, len(samples)*2)
	for i, s := range samples {
		if s > 1 {
			s = 1
		} else if s < -1 {
			s = -1
		}
		v := int16(s * 32767)
		out[i*2] = byte(v)
		out[i*2+1] = byte(v >> 8)
	}
	return out
}

// writeWAV24k writes samples as a finalized 24 kHz mono 16-bit WAV at dst.
func writeWAV24k(dst string, samples []float32) error {
	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("omnivoice: create %q: %w", dst, err)
	}
	enc := wav.NewEncoder(f, omnivoiceSampleRate, 16, 1, 1)
	ints := make([]int, len(samples))
	for i, s := range samples {
		if s > 1 {
			s = 1
		} else if s < -1 {
			s = -1
		}
		ints[i] = int(s * 32767)
	}
	b := &audio.IntBuffer{
		Format:         &audio.Format{NumChannels: 1, SampleRate: omnivoiceSampleRate},
		Data:           ints,
		SourceBitDepth: 16,
	}
	if err := enc.Write(b); err != nil {
		_ = enc.Close()
		_ = f.Close()
		return fmt.Errorf("omnivoice: encode WAV: %w", err)
	}
	if err := enc.Close(); err != nil {
		_ = f.Close()
		return fmt.Errorf("omnivoice: finalize WAV: %w", err)
	}
	return f.Close()
}

// readWAVAsFloat decodes a WAV file (any sample rate/channels) to a mono
// float32 slice in [-1,1] for use as reference audio. OmniVoice expects 24 kHz;
// callers should supply 24 kHz reference clips.
func readWAVAsFloat(path string) ([]float32, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("omnivoice: open ref %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	dec := wav.NewDecoder(f)
	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return nil, fmt.Errorf("omnivoice: decode ref %q: %w", path, err)
	}
	ch := int(buf.Format.NumChannels)
	if ch < 1 {
		ch = 1
	}
	bitDepth := int(buf.SourceBitDepth)
	if bitDepth == 0 {
		bitDepth = 16
	}
	scale := float32(int64(1) << uint(bitDepth-1))
	n := len(buf.Data) / ch
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		// Downmix to mono by averaging channels.
		var acc int
		for c := 0; c < ch; c++ {
			acc += buf.Data[i*ch+c]
		}
		out[i] = float32(acc) / float32(ch) / scale
	}
	return out, nil
}

// runtimeKeepAlive prevents the GC from reclaiming the reference-audio slice
// while its backing pointer is in use across the C call.
func runtimeKeepAlive(v any) { runtime.KeepAlive(v) }
