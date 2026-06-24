package main

import (
	"encoding/binary"
	"math"
	"os"

	"github.com/ebitengine/purego"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func ttsReq(text, voice, lang, dst string) *pb.TTSRequest {
	r := &pb.TTSRequest{Text: text, Voice: voice, Dst: dst}
	if lang != "" {
		r.Language = &lang
	}
	return r
}

// wavRMS parses a 16-bit PCM WAV file and returns (sampleRate, channels, RMS
// of the normalized samples).
func wavRMS(path string) (int, int, float64) {
	data, err := os.ReadFile(path) // #nosec G304 -- test-owned temp file
	Expect(err).ToNot(HaveOccurred())
	Expect(len(data)).To(BeNumerically(">", 44))
	Expect(string(data[0:4])).To(Equal("RIFF"))
	Expect(string(data[8:12])).To(Equal("WAVE"))
	channels := int(binary.LittleEndian.Uint16(data[22:24]))
	rate := int(binary.LittleEndian.Uint32(data[24:28]))
	// Find the data chunk (go-audio writes fmt first, data after).
	off := 12
	for off+8 <= len(data) {
		id := string(data[off : off+4])
		sz := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		if id == "data" {
			pcm := data[off+8:]
			if sz < len(pcm) {
				pcm = pcm[:sz]
			}
			var sum float64
			n := len(pcm) / 2
			for i := 0; i < n; i++ {
				s := float64(int16(binary.LittleEndian.Uint16(pcm[i*2:]))) / 32768.0
				sum += s * s
			}
			Expect(n).To(BeNumerically(">", 0))
			return rate, channels, math.Sqrt(sum / float64(n))
		}
		off += 8 + sz
	}
	Fail("no data chunk found in " + path)
	return 0, 0, 0
}

var _ = Describe("magpie-tts-cpp e2e", Label("e2e"), func() {
	var (
		loaded  bool
		backend *MagpieTtsCpp
	)

	BeforeEach(func() {
		modelPath := os.Getenv("MAGPIETTS_MODEL")
		if modelPath == "" {
			Skip("MAGPIETTS_MODEL not set; skipping e2e")
		}
		if !loaded {
			lib := os.Getenv("MAGPIETTS_LIBRARY")
			if lib == "" {
				lib = "./libgomagpiettscpp-fallback.so"
			}
			h, err := purego.Dlopen(lib, purego.RTLD_NOW|purego.RTLD_GLOBAL)
			Expect(err).ToNot(HaveOccurred())
			purego.RegisterLibFunc(&CppAbiVersion, h, "magpie_tts_capi_abi_version")
			purego.RegisterLibFunc(&CppLoad, h, "magpie_tts_capi_load")
			purego.RegisterLibFunc(&CppFree, h, "magpie_tts_capi_free")
			purego.RegisterLibFunc(&CppSynthesize, h, "magpie_tts_capi_synthesize")
			purego.RegisterLibFunc(&CppFreeAudio, h, "magpie_tts_capi_free_audio")
			purego.RegisterLibFunc(&CppLastError, h, "magpie_tts_capi_last_error")

			backend = &MagpieTtsCpp{}
			Expect(backend.Load(&pb.ModelOptions{ModelFile: modelPath})).To(Succeed())
			loaded = true
		}
	})

	It("synthesizes a non-silent 22.05 kHz mono WAV via TTS", func() {
		dst := GinkgoT().TempDir() + "/out.wav"
		Expect(backend.TTS(ttsReq("Hello world, this is a test.", "Aria", "en", dst))).To(Succeed())
		rate, channels, rms := wavRMS(dst)
		Expect(rate).To(Equal(22050))
		Expect(channels).To(Equal(1))
		Expect(rms).To(BeNumerically(">", 0.01), "audio should not be silent")
	})

	It("accepts a case-insensitive voice and a speaker index", func() {
		dst := GinkgoT().TempDir() + "/out.wav"
		Expect(backend.TTS(ttsReq("Short test.", "sofia", "en", dst))).To(Succeed())
		Expect(backend.TTS(ttsReq("Short test.", "1", "en", dst))).To(Succeed())
	})

	It("rejects an unknown voice before reaching the engine", func() {
		dst := GinkgoT().TempDir() + "/out.wav"
		err := backend.TTS(ttsReq("Short test.", "not-a-speaker", "en", dst))
		Expect(err).To(MatchError(ContainSubstring("unknown voice")))
	})

	It("streams a self-describing WAV via TTSStream", func() {
		results := make(chan []byte, 4096)
		done := make(chan error, 1)
		go func() { done <- backend.TTSStream(ttsReq("Hello there, streaming test.", "", "", ""), results) }()

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
		Expect(string(first[8:12])).To(Equal("WAVE"))
	})
})
