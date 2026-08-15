package main

import (
	"errors"
	"math"
	"os"
	"path/filepath"

	"github.com/go-audio/audio"
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

	// #nosec G304 -- converted is filepath.Join of a directory this function just
	// created with os.MkdirTemp and a constant basename. The request-controlled
	// path is the INPUT to AudioToWav and never reaches this open.
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
	rate, err := sampleRateOf(buf)
	if err != nil {
		return nil, 0, err
	}
	return buf.AsFloat32Buffer().Data, rate, nil
}

// sampleRateOf reads the decoded rate back off the buffer.
//
// It is an error rather than a zero fallback because 0 is not "unknown" to this
// runtime: nemo_speech_asr_recognize_f32 and nemo_speech_asr_stream_push_f32
// both read a 0 rate as "these samples are already at the model rate" and skip
// resampling (include/nemo_speech/asr.h). Handing 0 over for a header the
// decoder could not read would not fail, it would silently pitch-shift the
// audio and quietly degrade the transcript, which is the same failure the
// caller comment warns about for a wrong rate.
//
// The upper bound is what makes the narrowing to int32 safe rather than merely
// unlikely. go-audio reads the WAV header's sample rate as an unsigned 32-bit
// field into an int, so on a 64-bit build a header claiming more than 2^31-1
// survives the "> 0" test and then narrows to a NEGATIVE rate, which the runtime
// would take as a resampling ratio. Nothing this backend decodes can reach that
// today (AudioToWav either passes through a WAV it has confirmed is exactly
// 16 kHz or runs ffmpeg with -ar 16000), but that is a property of a helper in
// another package, and this function exists precisely because the rate is read
// back rather than assumed.
func sampleRateOf(buf *audio.IntBuffer) (int32, error) {
	if buf.Format == nil || buf.Format.SampleRate <= 0 || buf.Format.SampleRate > math.MaxInt32 {
		return 0, errors.New("nemo-speech-cpp: decoded audio has no usable sample rate")
	}
	return int32(buf.Format.SampleRate), nil
}
