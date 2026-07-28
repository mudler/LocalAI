package main

import (
	"path/filepath"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// These specs pin the voice-selection logic in resolveRequest, in particular
// the config-level audio_path (tts.audio_path -> ModelOptions.AudioPath) being
// used as the default voice-cloning reference. MOSS-TTS-Local takes the
// reference as a WAV path (the engine decodes it), so resolveRequest is pure
// path logic and needs no model / C library.
var _ = Describe("resolveRequest voice/clone selection", func() {
	var dir, refWav string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		refWav = filepath.Join(dir, "ref.wav")
	})

	It("uses the config audio_path as the clone reference when Voice is empty", func() {
		m := &MossTtsCpp{audioPath: refWav}
		ref, seed := m.resolveRequest(&pb.TTSRequest{Text: "hi"})
		Expect(ref).To(Equal(refWav))
		Expect(seed).To(Equal(0)) // zero-value loadOptions seed
	})

	It("lets a per-request audio Voice override audio_path", func() {
		other := filepath.Join(dir, "other.wav")
		m := &MossTtsCpp{audioPath: refWav}
		ref, _ := m.resolveRequest(&pb.TTSRequest{Text: "hi", Voice: other})
		Expect(ref).To(Equal(other))
	})

	It("does not clone for a bare-token Voice, falling back to audio_path", func() {
		m := &MossTtsCpp{audioPath: refWav}
		ref, _ := m.resolveRequest(&pb.TTSRequest{Text: "hi", Voice: "serena"})
		Expect(ref).To(Equal(refWav))
	})

	It("reads a per-request seed override from params", func() {
		m := &MossTtsCpp{opts: loadOptions{seed: -1}}
		_, seed := m.resolveRequest(&pb.TTSRequest{Text: "hi", Params: map[string]string{"seed": "123"}})
		Expect(seed).To(Equal(123))
	})
})
