package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"

	"github.com/go-audio/wav"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// GGUF metadata value type tags (subset) from the GGUF spec.
const (
	ggufTypeUint32 uint32 = 4
	ggufTypeString uint32 = 8
)

type ggufKV struct {
	key   string
	vtype uint32
	val   any
}

// writeMinimalGGUF emits a valid, tensor-less GGUF file carrying only the given
// metadata key-values. Enough for the header-only parse path piperSampleRate
// uses; avoids pulling a real multi-MB voice into the test.
func writeMinimalGGUF(path string, kvs []ggufKV) error {
	var b bytes.Buffer
	b.WriteString("GGUF")                                // magic
	_ = binary.Write(&b, binary.LittleEndian, uint32(3)) // version
	_ = binary.Write(&b, binary.LittleEndian, uint64(0)) // tensor count
	_ = binary.Write(&b, binary.LittleEndian, uint64(len(kvs)))
	for _, kv := range kvs {
		_ = binary.Write(&b, binary.LittleEndian, uint64(len(kv.key)))
		b.WriteString(kv.key)
		_ = binary.Write(&b, binary.LittleEndian, kv.vtype)
		switch v := kv.val.(type) {
		case uint32:
			_ = binary.Write(&b, binary.LittleEndian, v)
		case string:
			_ = binary.Write(&b, binary.LittleEndian, uint64(len(v)))
			b.WriteString(v)
		}
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// wavSampleRate decodes the WAV header at path and returns its sample rate.
func wavSampleRate(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	dec := wav.NewDecoder(f)
	dec.ReadInfo()
	return int(dec.SampleRate), nil
}

var _ = Describe("piper sample rate", func() {
	Context("piperSampleRate", func() {
		It("reads piper.sample_rate from a piper GGUF (medium = 22050)", func() {
			p := filepath.Join(GinkgoT().TempDir(), "voice.gguf")
			Expect(writeMinimalGGUF(p, []ggufKV{
				{key: "general.architecture", vtype: ggufTypeString, val: "piper"},
				{key: "piper.sample_rate", vtype: ggufTypeUint32, val: uint32(22050)},
			})).To(Succeed())

			rate, ok := piperSampleRate(p)
			Expect(ok).To(BeTrue(), "piper.sample_rate should be found")
			Expect(rate).To(Equal(22050))
		})

		It("reads the low-quality rate (16000)", func() {
			p := filepath.Join(GinkgoT().TempDir(), "voice.gguf")
			Expect(writeMinimalGGUF(p, []ggufKV{
				{key: "piper.sample_rate", vtype: ggufTypeUint32, val: uint32(16000)},
			})).To(Succeed())

			rate, ok := piperSampleRate(p)
			Expect(ok).To(BeTrue())
			Expect(rate).To(Equal(16000))
		})

		It("returns ok=false for a non-piper GGUF (no piper.sample_rate key)", func() {
			p := filepath.Join(GinkgoT().TempDir(), "other.gguf")
			Expect(writeMinimalGGUF(p, []ggufKV{
				{key: "general.architecture", vtype: ggufTypeString, val: "vibevoice"},
			})).To(Succeed())

			_, ok := piperSampleRate(p)
			Expect(ok).To(BeFalse())
		})

		It("returns ok=false for an unreadable/non-GGUF file", func() {
			p := filepath.Join(GinkgoT().TempDir(), "garbage.gguf")
			Expect(os.WriteFile(p, []byte("not a gguf"), 0o644)).To(Succeed())

			_, ok := piperSampleRate(p)
			Expect(ok).To(BeFalse())
		})
	})

	// End-to-end through the built .so. Gated on CRISPASR_PIPER_MODEL_PATH (a
	// real piper voice GGUF) like the other model-backed specs; never runs in
	// default CI. Proves CrispASR's piper backend output rate flows into the
	// WAV header instead of the hardcoded 24 kHz default.
	Context("piper TTS end-to-end", func() {
		It("writes the WAV at the model's native piper.sample_rate", func() {
			model := os.Getenv("CRISPASR_PIPER_MODEL_PATH")
			if model == "" {
				Skip("set CRISPASR_PIPER_MODEL_PATH to run the piper e2e spec")
			}
			ensureLibLoaded()

			expected, ok := piperSampleRate(model)
			Expect(ok).To(BeTrue(), "model should carry piper.sample_rate metadata")

			w := &CrispASR{}
			Expect(w.Load(&pb.ModelOptions{
				ModelFile: model,
				Options:   []string{"backend:piper"},
				Threads:   4,
			})).To(Succeed())

			dst := filepath.Join(GinkgoT().TempDir(), "piper.wav")
			Expect(w.TTS(&pb.TTSRequest{Text: "Hello from CrispASR piper.", Dst: dst})).To(Succeed())

			info, err := os.Stat(dst)
			Expect(err).ToNot(HaveOccurred())
			Expect(info.Size()).To(BeNumerically(">", 1024), "expected a non-trivial WAV")

			rate, err := wavSampleRate(dst)
			Expect(err).ToNot(HaveOccurred())
			Expect(rate).To(Equal(expected),
				"WAV header rate must equal the model's native piper.sample_rate, not the 24k default")
		})
	})

	Context("writeWAV", func() {
		It("writes the WAV header at the given sample rate (22050 for piper, not the 24k default)", func() {
			dst := filepath.Join(GinkgoT().TempDir(), "out.wav")
			pcm := make([]float32, 220) // 10 ms of silence is enough for a header
			Expect(writeWAV(dst, pcm, 22050)).To(Succeed())

			rate, err := wavSampleRate(dst)
			Expect(err).ToNot(HaveOccurred())
			Expect(rate).To(Equal(22050))
		})

		It("writes a 16000 Hz header for low-quality piper voices", func() {
			dst := filepath.Join(GinkgoT().TempDir(), "out.wav")
			pcm := make([]float32, 160)
			Expect(writeWAV(dst, pcm, 16000)).To(Succeed())

			rate, err := wavSampleRate(dst)
			Expect(err).ToNot(HaveOccurred())
			Expect(rate).To(Equal(16000))
		})
	})
})
