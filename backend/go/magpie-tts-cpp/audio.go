package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

// magpieSampleRate is the fixed Magpie TTS Multilingual (NanoCodec) output
// rate: 22.05 kHz.
const magpieSampleRate = 22050

// magpieChannels is the fixed output layout: mono.
const magpieChannels = 1

// wavHeaderMono returns a 44-byte WAV header for a streaming 16-bit mono PCM
// stream at 22050 Hz, with placeholder (0xFFFFFFFF) sizes since the total
// length is unknown up front. Emitted as the first chunk of TTSStream so the
// HTTP layer receives a self-describing WAV.
func wavHeaderMono() []byte {
	const blockAlign = magpieChannels * 2 // 16-bit mono
	var buf bytes.Buffer
	w := func(v any) { _ = binary.Write(&buf, binary.LittleEndian, v) }
	buf.WriteString("RIFF")
	w(uint32(0xFFFFFFFF))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	w(uint32(16))                            // Subchunk1Size
	w(uint16(1))                             // PCM
	w(uint16(magpieChannels))                // mono
	w(uint32(magpieSampleRate))              // sample rate
	w(uint32(magpieSampleRate * blockAlign)) // byte rate = SR * blockAlign
	w(uint16(blockAlign))                    // block align
	w(uint16(16))                            // bits per sample
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
		out[i*2] = byte(v)        // #nosec G115 -- intentional little-endian split of a clamped int16
		out[i*2+1] = byte(v >> 8) // #nosec G115 -- high byte of the same clamped int16
	}
	return out
}

// writeWAVMono writes float samples as a finalized 16-bit mono WAV at
// 22050 Hz.
func writeWAVMono(dst string, samples []float32) error {
	f, err := os.Create(dst) // #nosec G304 -- dst is the server-chosen output path from the TTS request, not user-traversable
	if err != nil {
		return fmt.Errorf("magpie-tts: create %q: %w", dst, err)
	}
	enc := wav.NewEncoder(f, magpieSampleRate, 16, magpieChannels, 1)
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
		Format:         &audio.Format{NumChannels: magpieChannels, SampleRate: magpieSampleRate},
		Data:           ints,
		SourceBitDepth: 16,
	}
	if err := enc.Write(b); err != nil {
		_ = enc.Close()
		_ = f.Close()
		return fmt.Errorf("magpie-tts: encode WAV: %w", err)
	}
	if err := enc.Close(); err != nil {
		_ = f.Close()
		return fmt.Errorf("magpie-tts: finalize WAV: %w", err)
	}
	return f.Close()
}
