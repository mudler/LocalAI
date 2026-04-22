package main

import (
	"os"
	"path/filepath"
	"testing"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSherpaBackend(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Sherpa-ONNX Backend Suite")
}

// Load libsherpa-shim + libsherpa-onnx-c-api via purego before any spec
// runs — otherwise any Load/TTS/VAD/AudioTranscription call hits a nil
// function pointer. LD_LIBRARY_PATH must contain the directory holding
// both .so files; test.sh sets this.
var _ = BeforeSuite(func() {
	Expect(loadSherpaLibs()).To(Succeed())
})

var _ = Describe("Sherpa-ONNX", func() {
	Context("lifecycle", func() {
		It("is locking (C API is not thread safe)", func() {
			Expect((&SherpaBackend{}).Locking()).To(BeTrue())
		})

		It("errors loading a non-existent model", func() {
			tmpDir, err := os.MkdirTemp("", "sherpa-test-nonexistent")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tmpDir)

			err = (&SherpaBackend{}).Load(&pb.ModelOptions{
				ModelFile: filepath.Join(tmpDir, "non-existent-model.onnx"),
			})
			Expect(err).To(HaveOccurred())
		})

		It("errors loading a non-existent ASR model", func() {
			tmpDir, err := os.MkdirTemp("", "sherpa-test-asr")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tmpDir)

			err = (&SherpaBackend{}).Load(&pb.ModelOptions{
				ModelFile: filepath.Join(tmpDir, "model.onnx"),
				Type:      "asr",
			})
			Expect(err).To(HaveOccurred())
		})

		It("dispatches Load by Type", func() {
			tmpDir, err := os.MkdirTemp("", "sherpa-test-dispatch")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tmpDir)

			modelFile := filepath.Join(tmpDir, "model.onnx")
			for _, typ := range []string{"", "asr", "vad"} {
				err := (&SherpaBackend{}).Load(&pb.ModelOptions{ModelFile: modelFile, Type: typ})
				Expect(err).To(HaveOccurred(), "Type=%q", typ)
			}
		})
	})

	Context("method errors without loaded model", func() {
		It("rejects TTS", func() {
			tmpDir, err := os.MkdirTemp("", "sherpa-test-tts")
			Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tmpDir)

			err = (&SherpaBackend{}).TTS(&pb.TTSRequest{
				Text: "should fail — no model loaded",
				Dst:  filepath.Join(tmpDir, "output.wav"),
			})
			Expect(err).To(HaveOccurred())
		})

		It("rejects AudioTranscription", func() {
			_, err := (&SherpaBackend{}).AudioTranscription(&pb.TranscriptRequest{
				Dst: "/tmp/nonexistent.wav",
			})
			Expect(err).To(HaveOccurred())
		})

		It("rejects VAD", func() {
			_, err := (&SherpaBackend{}).VAD(&pb.VADRequest{
				Audio: []float32{0.1, 0.2, 0.3},
			})
			Expect(err).To(HaveOccurred())
		})
	})

	Context("type detection", func() {
		DescribeTable("isASRType",
			func(input string, want bool) {
				Expect(isASRType(input)).To(Equal(want))
			},
			Entry("asr", "asr", true),
			Entry("ASR", "ASR", true),
			Entry("Asr", "Asr", true),
			Entry("transcription", "transcription", true),
			Entry("Transcription", "Transcription", true),
			Entry("transcribe", "transcribe", true),
			Entry("Transcribe", "Transcribe", true),
			Entry("tts", "tts", false),
			Entry("empty", "", false),
			Entry("other", "other", false),
			Entry("vad", "vad", false),
		)

		DescribeTable("isVADType",
			func(input string, want bool) {
				Expect(isVADType(input)).To(Equal(want))
			},
			Entry("vad", "vad", true),
			Entry("VAD", "VAD", true),
			Entry("Vad", "Vad", true),
			Entry("asr", "asr", false),
			Entry("tts", "tts", false),
			Entry("empty", "", false),
			Entry("other", "other", false),
		)
	})

	Context("option parsing", func() {
		It("parses float options with fallback on bad input", func() {
			opts := &pb.ModelOptions{Options: []string{
				"vad.threshold=0.3",
				"tts.length_scale=1.25",
				"bad.number=not-a-float",
			}}
			Expect(findOptionFloat(opts, "vad.threshold=", 0.5)).To(BeNumerically("~", 0.3, 1e-6))
			Expect(findOptionFloat(opts, "tts.length_scale=", 1.0)).To(BeNumerically("~", 1.25, 1e-6))
			Expect(findOptionFloat(opts, "missing.key=", 0.7)).To(BeNumerically("~", 0.7, 1e-6))
			Expect(findOptionFloat(opts, "bad.number=", 9.9)).To(BeNumerically("~", 9.9, 1e-6))
		})

		It("parses int options with fallback on bad input", func() {
			opts := &pb.ModelOptions{Options: []string{
				"asr.sample_rate=22050",
				"online.chunk_samples=800",
				"bad.int=4.2",
			}}
			Expect(findOptionInt(opts, "asr.sample_rate=", 16000)).To(Equal(int32(22050)))
			Expect(findOptionInt(opts, "online.chunk_samples=", 1600)).To(Equal(int32(800)))
			Expect(findOptionInt(opts, "missing.key=", 42)).To(Equal(int32(42)))
			Expect(findOptionInt(opts, "bad.int=", 100)).To(Equal(int32(100)))
		})

		It("parses bool options (0/1, true/false, yes/no, on/off)", func() {
			opts := &pb.ModelOptions{Options: []string{
				"online.enable_endpoint=0",
				"asr.sense_voice.use_itn=True",
				"feature.on=yes",
				"feature.off=Off",
				"feature.bad=maybe",
			}}
			Expect(findOptionBool(opts, "online.enable_endpoint=", 1)).To(Equal(int32(0)))
			Expect(findOptionBool(opts, "asr.sense_voice.use_itn=", 0)).To(Equal(int32(1)))
			Expect(findOptionBool(opts, "feature.on=", 0)).To(Equal(int32(1)))
			Expect(findOptionBool(opts, "feature.off=", 1)).To(Equal(int32(0)))
			Expect(findOptionBool(opts, "feature.bad=", 1)).To(Equal(int32(1)))
			Expect(findOptionBool(opts, "missing.key=", 1)).To(Equal(int32(1)))
		})
	})
})
