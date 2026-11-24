package main

import (
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

var _ = Describe("OmniVoice e2e", Label("e2e"), func() {
	var loaded bool

	BeforeEach(func() {
		modelPath := os.Getenv("OMNIVOICE_MODEL")
		codecPath := os.Getenv("OMNIVOICE_CODEC")
		if modelPath == "" || codecPath == "" {
			Skip("OMNIVOICE_MODEL / OMNIVOICE_CODEC not set; skipping e2e")
		}
		if !loaded {
			lib := os.Getenv("OMNIVOICE_LIBRARY")
			if lib == "" {
				lib = "./libgomnivoicecpp-fallback.so"
			}
			h, err := purego.Dlopen(lib, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			Expect(err).ToNot(HaveOccurred())
			purego.RegisterLibFunc(&CppLoad, h, "omni_load")
			purego.RegisterLibFunc(&CppTTS, h, "omni_tts")
			purego.RegisterLibFunc(&CppTTSStream, h, "omni_tts_stream")
			purego.RegisterLibFunc(&CppPCMFree, h, "omni_pcm_free")
			purego.RegisterLibFunc(&CppUnload, h, "omni_unload")
			Expect(CppLoad(modelPath, codecPath, 0, 0)).To(Equal(0))
			loaded = true
		}
	})

	It("synthesizes a WAV file via TTS", func() {
		b := &OmnivoiceCpp{opts: loadOptions{seed: 42, denoise: true}}
		dst := GinkgoT().TempDir() + "/out.wav"
		lang := "en"
		err := b.TTS(ttsReq("Hello world.", "", &lang, dst))
		Expect(err).ToNot(HaveOccurred())
		fi, err := os.Stat(dst)
		Expect(err).ToNot(HaveOccurred())
		Expect(fi.Size()).To(BeNumerically(">", int64(44)))
	})

	It("streams audio chunks via TTSStream", func() {
		b := &OmnivoiceCpp{opts: loadOptions{seed: 42, denoise: true}}
		results := make(chan []byte, 1024)
		lang := "en"
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
})
