package main

import (
	"path/filepath"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs pin the voice-selection logic in resolveRequest, in particular
// the config-level audio_path (tts.audio_path -> ModelOptions.AudioPath) being
// used as the default voice-cloning reference. No model/C library is needed:
// resolveRequest only reads the reference WAV via readWAVAsFloat (pure Go).
var _ = Describe("resolveRequest voice/clone selection", func() {
	var dir, refWav string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		refWav = filepath.Join(dir, "ref.wav")
		// 0.5s of non-silent 24kHz mono audio as a clone reference.
		samples := make([]float32, qwen3ttsSampleRate/2)
		for i := range samples {
			samples[i] = 0.1
		}
		Expect(writeWAV24k(refWav, samples)).To(Succeed())
	})

	It("uses the config audio_path as the clone reference when Voice is empty", func() {
		q := &Qwen3TtsCpp{audioPath: refWav}
		_, _, speaker, _, ref, _, err := q.resolveRequest(&pb.TTSRequest{Text: "hi"})
		Expect(err).ToNot(HaveOccurred())
		Expect(speaker).To(BeEmpty())
		Expect(len(ref)).To(Equal(qwen3ttsSampleRate / 2))
	})

	It("lets a per-request audio Voice override audio_path", func() {
		other := filepath.Join(dir, "other.wav")
		Expect(writeWAV24k(other, make([]float32, 100))).To(Succeed())
		q := &Qwen3TtsCpp{audioPath: refWav}
		_, _, speaker, _, ref, _, err := q.resolveRequest(&pb.TTSRequest{Text: "hi", Voice: other})
		Expect(err).ToNot(HaveOccurred())
		Expect(speaker).To(BeEmpty())
		Expect(len(ref)).To(Equal(100))
	})

	It("does not trigger audio_path cloning for a named-speaker Voice", func() {
		q := &Qwen3TtsCpp{audioPath: refWav}
		_, _, speaker, _, ref, _, err := q.resolveRequest(&pb.TTSRequest{Text: "hi", Voice: "serena"})
		Expect(err).ToNot(HaveOccurred())
		Expect(speaker).To(Equal("serena"))
		Expect(ref).To(BeNil())
	})
})
