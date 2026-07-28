package main

import (
	"os"
	"strings"

	"github.com/ebitengine/purego"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func ttsReq(text, voice, dst string) *pb.TTSRequest {
	return &pb.TTSRequest{Text: text, Voice: voice, Dst: dst}
}

var _ = Describe("moss-tts-cpp e2e", Label("e2e"), func() {
	var loaded bool

	BeforeEach(func() {
		modelPath := os.Getenv("MOSSTTS_MODEL")
		codecPath := os.Getenv("MOSSTTS_CODEC")
		tokenizerPath := os.Getenv("MOSSTTS_TOKENIZER")
		if modelPath == "" || codecPath == "" || tokenizerPath == "" {
			Skip("MOSSTTS_MODEL / MOSSTTS_CODEC / MOSSTTS_TOKENIZER not set; skipping e2e")
		}
		if !loaded {
			lib := os.Getenv("MOSSTTS_LIBRARY")
			if lib == "" {
				lib = "./libgomosstts-cpp-fallback.so"
			}
			h, err := purego.Dlopen(lib, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			Expect(err).ToNot(HaveOccurred())
			purego.RegisterLibFunc(&CppLoad, h, "mtl_load")
			purego.RegisterLibFunc(&CppTTS, h, "mtl_tts")
			purego.RegisterLibFunc(&CppPCMFree, h, "mtl_pcm_free")
			purego.RegisterLibFunc(&CppUnload, h, "mtl_unload")
			Expect(CppLoad(modelPath, codecPath, tokenizerPath)).To(Equal(0))
			loaded = true
		}
	})

	It("synthesizes a stereo WAV file via TTS", func() {
		m := &MossTtsCpp{opts: loadOptions{seed: 42}}
		dst := GinkgoT().TempDir() + "/out.wav"
		err := m.TTS(ttsReq("Hello world.", "", dst))
		Expect(err).ToNot(HaveOccurred())
		fi, err := os.Stat(dst)
		Expect(err).ToNot(HaveOccurred())
		Expect(fi.Size()).To(BeNumerically(">", int64(44)))
	})

	It("streams audio chunks via TTSStream", func() {
		m := &MossTtsCpp{opts: loadOptions{seed: 42}}
		results := make(chan []byte, 4096)
		done := make(chan error, 1)
		go func() { done <- m.TTSStream(ttsReq("Hello there, streaming test.", "", ""), results) }()

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
