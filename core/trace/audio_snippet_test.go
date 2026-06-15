package trace_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/trace"
)

// One second of mono 16-bit PCM at 16 kHz: 32 KiB raw. After the 44-byte
// WAV header and base64 encoding the snippet runs ~42 KiB, which is well
// over the small caps used here and matches the smallest realistic TTS
// output size.
const (
	snippetSampleRate = 16000
	snippetSeconds    = 1
)

func makePCM(seconds, sampleRate int) []byte {
	return make([]byte, seconds*sampleRate*2) // int16 mono
}

var _ = Describe("AudioSnippetFromPCM byte cap", func() {
	pcm := makePCM(snippetSeconds, snippetSampleRate)
	totalPCM := len(pcm)

	It("omits audio_wav_base64 when the encoded snippet would exceed the cap, keeping the metrics", func() {
		out := trace.AudioSnippetFromPCM(pcm, snippetSampleRate, totalPCM, 1024)

		Expect(out).ToNot(BeNil(), "metrics must still be returned even when the waveform is dropped")
		Expect(out).ToNot(HaveKey("audio_wav_base64"), "oversized base64 must be dropped so the UI does not try to render invalid audio data")
		Expect(out).To(HaveKey("audio_duration_s"))
		Expect(out).To(HaveKey("audio_sample_rate"))
		Expect(out).To(HaveKey("audio_rms_dbfs"))
	})

	It("includes audio_wav_base64 when the snippet fits under the cap", func() {
		out := trace.AudioSnippetFromPCM(pcm, snippetSampleRate, totalPCM, 1024*1024)

		Expect(out).To(HaveKey("audio_wav_base64"))
		Expect(out["audio_wav_base64"]).ToNot(BeEmpty())
	})

	It("includes audio_wav_base64 when the cap is disabled (0)", func() {
		out := trace.AudioSnippetFromPCM(pcm, snippetSampleRate, totalPCM, 0)

		Expect(out).To(HaveKey("audio_wav_base64"))
	})
})
