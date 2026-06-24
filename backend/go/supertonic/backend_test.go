package main

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var _ = Describe("voiceStylePath", func() {
	s := &SupertonicBackend{modelDir: "/models/st/onnx", voicesDir: "/models/st/voice_styles"}

	It("resolves a bare name under the resolved voicesDir", func() {
		Expect(s.voiceStylePath("M1")).To(Equal(filepath.Join("/models/st/voice_styles", "M1.json")))
	})
	It("keeps an explicit .json suffix", func() {
		Expect(s.voiceStylePath("M1.json")).To(Equal(filepath.Join("/models/st/voice_styles", "M1.json")))
	})
	It("honors absolute paths", func() {
		Expect(s.voiceStylePath("/abs/v.json")).To(Equal("/abs/v.json"))
	})
})

var _ = Describe("resolveVoicesDir", func() {
	It("prefers voice_styles under modelDir", func() {
		dir := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(dir, "voice_styles"), 0o755)).To(Succeed())
		Expect(resolveVoicesDir(dir)).To(Equal(filepath.Join(dir, "voice_styles")))
	})
	It("falls back to the sibling voice_styles next to an onnx subdir", func() {
		root := GinkgoT().TempDir()
		Expect(os.MkdirAll(filepath.Join(root, "voice_styles"), 0o755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(root, "onnx"), 0o755)).To(Succeed())
		Expect(resolveVoicesDir(filepath.Join(root, "onnx"))).To(Equal(filepath.Join(root, "voice_styles")))
	})
})

var _ = Describe("resolveLang", func() {
	It("accepts a valid request language", func() {
		s := &SupertonicBackend{defaultLang: "na"}
		Expect(s.resolveLang("ko")).To(Equal("ko"))
	})
	It("falls back to the model default for an invalid language", func() {
		s := &SupertonicBackend{defaultLang: "en"}
		Expect(s.resolveLang("zz")).To(Equal("en"))
	})
	It("falls back to na when nothing is valid", func() {
		s := &SupertonicBackend{defaultLang: ""}
		Expect(s.resolveLang("")).To(Equal("na"))
	})
})

var _ = Describe("pcmFloatToInt16LE", func() {
	It("clamps and encodes little-endian", func() {
		out := pcmFloatToInt16LE([]float32{0, 1.0, -1.0, 2.0})
		Expect(out).To(HaveLen(8))
		Expect(out[0:2]).To(Equal([]byte{0x00, 0x00})) // 0
		Expect(out[2:4]).To(Equal([]byte{0xff, 0x7f})) // 32767
		Expect(out[6:8]).To(Equal([]byte{0xff, 0x7f})) // clamp 2.0 -> 32767
	})
})

var _ = Describe("end-to-end synthesis", Ordered, func() {
	var modelDir string
	BeforeAll(func() {
		modelDir = os.Getenv("SUPERTONIC_MODEL_PATH")
		if modelDir == "" {
			Skip("set SUPERTONIC_MODEL_PATH to a supertonic model dir to run")
		}
		Expect(InitializeONNXRuntime()).To(Succeed())
	})

	It("synthesizes a wav file", func() {
		b := &SupertonicBackend{}
		Expect(b.Load(&pb.ModelOptions{ModelFile: modelDir, Options: []string{"supertonic.default_voice=F1"}})).To(Succeed())
		dst := filepath.Join(GinkgoT().TempDir(), "out.wav")
		lang := "en"
		Expect(b.TTS(&pb.TTSRequest{Text: "Hello from LocalAI.", Dst: dst, Language: &lang})).To(Succeed())
		info, err := os.Stat(dst)
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Size()).To(BeNumerically(">", 44)) // header + PCM
	})
})
