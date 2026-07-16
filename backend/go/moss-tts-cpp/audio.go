package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
)

// mossttsSampleRate is the MOSS-TTS-Local v1.5 output rate; the engine also
// reports it per-synthesis via out_sr, which the writers honour.
const mossttsSampleRate = 48000

// mossttsChannels is the MOSS-TTS-Local output layout: 48 kHz stereo, samples
// interleaved [L,R,L,R,...].
const mossttsChannels = 2

// wavHeaderStereo returns a 44-byte WAV header for a streaming 16-bit stereo PCM
// stream at sampleRate, with placeholder (0xFFFFFFFF) sizes since the total
// length is unknown up front. Emitted as the first chunk of TTSStream so the
// HTTP layer receives a self-describing WAV.
func wavHeaderStereo(sampleRate int) []byte {
	if sampleRate <= 0 {
		sampleRate = mossttsSampleRate
	}
	const blockAlign = mossttsChannels * 2 // 16-bit stereo
	var buf bytes.Buffer
	w := func(v any) { _ = binary.Write(&buf, binary.LittleEndian, v) }
	buf.WriteString("RIFF")
	w(uint32(0xFFFFFFFF))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	w(uint32(16))                      // Subchunk1Size
	w(uint16(1))                       // PCM
	w(uint16(mossttsChannels))         // stereo
	w(uint32(sampleRate))              // sample rate
	w(uint32(sampleRate * blockAlign)) // byte rate = SR * blockAlign
	w(uint16(blockAlign))              // block align
	w(uint16(16))                      // bits per sample
	buf.WriteString("data")
	w(uint32(0xFFFFFFFF))
	return buf.Bytes()
}

// floatToPCM16LE clamps each sample to [-1,1] and encodes it as little-endian
// signed 16-bit PCM. Input may be interleaved stereo; the layout is preserved.
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

// writeWAVStereo writes interleaved [L,R,...] float samples as a finalized
// 16-bit stereo WAV at sampleRate (falling back to the v1.5 default of 48 kHz).
func writeWAVStereo(dst string, samples []float32, sampleRate int) error {
	if sampleRate <= 0 {
		sampleRate = mossttsSampleRate
	}
	f, err := os.Create(dst) // #nosec G304 -- dst is the server-chosen output path from the TTS request, not user-traversable
	if err != nil {
		return fmt.Errorf("moss-tts: create %q: %w", dst, err)
	}
	enc := wav.NewEncoder(f, sampleRate, 16, mossttsChannels, 1)
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
		Format:         &audio.Format{NumChannels: mossttsChannels, SampleRate: sampleRate},
		Data:           ints,
		SourceBitDepth: 16,
	}
	if err := enc.Write(b); err != nil {
		_ = enc.Close()
		_ = f.Close()
		return fmt.Errorf("moss-tts: encode WAV: %w", err)
	}
	if err := enc.Close(); err != nil {
		_ = f.Close()
		return fmt.Errorf("moss-tts: finalize WAV: %w", err)
	}
	return f.Close()
}
