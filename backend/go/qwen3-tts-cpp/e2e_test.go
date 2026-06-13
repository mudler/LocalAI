package main

import (
	"math"
	"os"
	"strings"

	"github.com/ebitengine/purego"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func ttsReq(text, voice string, lang *string, dst string) *pb.TTSRequest {
	return &pb.TTSRequest{Text: text, Voice: voice, Language: lang, Dst: dst}
}

var _ = Describe("qwen3-tts-cpp e2e", Label("e2e"), func() {
	var loaded bool

	BeforeEach(func() {
		modelPath := os.Getenv("QWEN3TTS_MODEL")
		codecPath := os.Getenv("QWEN3TTS_CODEC")
		if modelPath == "" || codecPath == "" {
			Skip("QWEN3TTS_MODEL / QWEN3TTS_CODEC not set; skipping e2e")
		}
		if !loaded {
			lib := os.Getenv("QWEN3TTS_LIBRARY")
			if lib == "" {
				lib = "./libgoqwen3ttscpp-fallback.so"
			}
			h, err := purego.Dlopen(lib, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			Expect(err).ToNot(HaveOccurred())
			purego.RegisterLibFunc(&CppLoad, h, "qt3_load")
			purego.RegisterLibFunc(&CppTTS, h, "qt3_tts")
			purego.RegisterLibFunc(&CppTTSStream, h, "qt3_tts_stream")
			purego.RegisterLibFunc(&CppPCMFree, h, "qt3_pcm_free")
			purego.RegisterLibFunc(&CppUnload, h, "qt3_unload")
			Expect(CppLoad(modelPath, codecPath, 1, 0)).To(Equal(0))
			loaded = true
		}
	})

	It("synthesizes a WAV file via TTS", func() {
		b := &Qwen3TtsCpp{opts: loadOptions{seed: 42, useFA: true}}
		dst := GinkgoT().TempDir() + "/out.wav"
		lang := "english"
		err := b.TTS(ttsReq("Hello world.", "", &lang, dst))
		Expect(err).ToNot(HaveOccurred())
		fi, err := os.Stat(dst)
		Expect(err).ToNot(HaveOccurred())
		Expect(fi.Size()).To(BeNumerically(">", int64(44)))
	})

	It("streams audio chunks via TTSStream", func() {
		b := &Qwen3TtsCpp{opts: loadOptions{seed: 42, useFA: true}}
		results := make(chan []byte, 1024)
		lang := "english"
		done := make(chan error, 1)
		go func() { done <- b.TTSStream(ttsReq("Hello there, streaming test.", "", &lang, ""), results) }()

		var chunks int
		var first []byte
		for c := range results {
			if chunks == 0 {
				first = c
			}
			chunks++
		}
		Expect(<-done).ToNot(HaveOccurred())
		Expect(chunks).To(BeNumerically(">=", 2))
		Expect(string(first[0:4])).To(Equal("RIFF"))
		Expect(strings.HasPrefix(string(first[8:12]), "WAVE")).To(BeTrue())
	})

	It("clones a voice from the config audio_path reference", func() {
		// 1s of 24kHz mono audio as a clone reference; the base model carries
		// a speaker encoder, so audio_path drives x-vector voice cloning.
		ref := GinkgoT().TempDir() + "/ref.wav"
		samples := make([]float32, qwen3ttsSampleRate)
		for i := range samples {
			samples[i] = float32(0.05 * math.Sin(float64(i)*0.06))
		}
		Expect(writeWAV24k(ref, samples)).To(Succeed())

		b := &Qwen3TtsCpp{opts: loadOptions{seed: 42, useFA: true}, audioPath: ref}
		dst := GinkgoT().TempDir() + "/clone.wav"
		lang := "english"
		// Empty Voice -> the config audio_path is used as the clone reference.
		Expect(b.TTS(ttsReq("Cloned voice test.", "", &lang, dst))).To(Succeed())
		fi, err := os.Stat(dst)
		Expect(err).ToNot(HaveOccurred())
		Expect(fi.Size()).To(BeNumerically(">", int64(44)))
	})
})
