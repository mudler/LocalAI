package openai

import (
	"fmt"

	"github.com/mudler/LocalAI/pkg/opus"
	"github.com/mudler/LocalAI/pkg/sound"
)

const (
	opusSampleRate = 48000
	opusChannels   = 1
	// 20ms frame at 48kHz mono = 960 samples
	opusFrameSize = 960
	// Maximum Opus packet size
	opusMaxPacketSize = 4000
	// Maximum decoded frame size (120ms at 48kHz)
	opusMaxFrameSize = 5760
)

// OpusEncoder wraps libopus (via purego shim) for encoding PCM int16 LE to Opus frames.
type OpusEncoder struct {
	enc *opus.Encoder
}

func NewOpusEncoder() (*OpusEncoder, error) {
	enc, err := opus.NewEncoder(opusSampleRate, opusChannels, opus.ApplicationAudio)
	if err != nil {
		return nil, fmt.Errorf("opus encoder: %w", err)
	}
	if err := enc.SetBitrate(64000); err != nil {
		enc.Close()
		return nil, fmt.Errorf("opus set bitrate: %w", err)
	}
	if err := enc.SetComplexity(10); err != nil {
		enc.Close()
		return nil, fmt.Errorf("opus set complexity: %w", err)
	}
	return &OpusEncoder{enc: enc}, nil
}

// Encode takes PCM int16 LE bytes at the given sampleRate and returns Opus frames.
// It resamples to 48kHz if needed, then encodes in 20ms frames.
func (e *OpusEncoder) Encode(pcmInt16LE []byte, sampleRate int) ([][]byte, error) {
	samples := sound.BytesToInt16sLE(pcmInt16LE)
	if len(samples) == 0 {
		return nil, nil
	}

	if sampleRate != opusSampleRate {
		samples = sound.ResampleInt16(samples, sampleRate, opusSampleRate)
	}

	var frames [][]byte
	packet := make([]byte, opusMaxPacketSize)

	for offset := 0; offset+opusFrameSize <= len(samples); offset += opusFrameSize {
		frame := samples[offset : offset+opusFrameSize]
		n, err := e.enc.Encode(frame, opusFrameSize, packet)
		if err != nil {
			return frames, fmt.Errorf("opus encode: %w", err)
		}
		out := make([]byte, n)
		copy(out, packet[:n])
		frames = append(frames, out)
	}

	return frames, nil
}

func (e *OpusEncoder) Close() {
	e.enc.Close()
}

// OpusDecoder wraps libopus (via purego shim) for decoding Opus frames to PCM int16 LE.
type OpusDecoder struct {
	dec *opus.Decoder
}

func NewOpusDecoder() (*OpusDecoder, error) {
	dec, err := opus.NewDecoder(opusSampleRate, opusChannels)
	if err != nil {
		return nil, fmt.Errorf("opus decoder: %w", err)
	}
	return &OpusDecoder{dec: dec}, nil
}

// Decode takes a single Opus frame and returns PCM int16 LE bytes at 48kHz.
func (d *OpusDecoder) Decode(opusFrame []byte) ([]int16, error) {
	pcm := make([]int16, opusMaxFrameSize)
	n, err := d.dec.Decode(opusFrame, pcm, opusMaxFrameSize, false)
	if err != nil {
		return nil, fmt.Errorf("opus decode: %w", err)
	}
	return pcm[:n], nil
}

func (d *OpusDecoder) Close() {
	d.dec.Close()
}
