package main

import (
	"os"
	"path/filepath"

	"github.com/go-audio/wav"
	"github.com/mudler/LocalAI/pkg/utils"
)

// decodeAudioMono16k converts an arbitrary audio file to 16 kHz mono PCM and
// returns the float32 samples together with the rate they are actually at.
//
// pkg/utils exposes the ffmpeg normalisation (AudioToWav) but no decode, so
// every Go ASR backend pairs it with go-audio itself. This mirrors
// backend/go/parakeet-cpp rather than adding a shared helper: the backends
// differ in what they need back (parakeet wants a duration, this one wants the
// sample rate to hand to the runtime), so a shared signature would be a
// lowest-common-denominator of both.
func decodeAudioMono16k(path string) ([]float32, int32, error) {
	dir, err := os.MkdirTemp("", "nemo-speech")
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	// A WAV already at 16 kHz mono 16-bit is hardlinked or copied through
	// without spawning ffmpeg, so the common case costs nothing.
	converted := filepath.Join(dir, "converted.wav")
	if err := utils.AudioToWav(path, converted); err != nil {
		return nil, 0, err
	}

	fh, err := os.Open(converted)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = fh.Close() }()

	buf, err := wav.NewDecoder(fh).FullPCMBuffer()
	if err != nil {
		return nil, 0, err
	}

	// The rate is read back from the decoded file rather than assumed to be
	// 16000. AudioToWav always lands there today, but the runtime resamples
	// anything from 8 to 96 kHz off this number, so a wrong one would not fail,
	// it would silently pitch-shift the audio and quietly degrade the transcript.
	var rate int32
	if buf.Format != nil {
		rate = int32(buf.Format.SampleRate)
	}
	return buf.AsFloat32Buffer().Data, rate, nil
}
