package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ebitengine/purego"
	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestMossTranscribeCpp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "moss-transcribe-cpp Backend Suite")
}

var (
	libLoadOnce sync.Once
	libLoadErr  error
)

// ensureLibLoaded mirrors main.go's bootstrap so a Go test can drive the C-API
// bridge without spinning up the gRPC server. Skips the current spec when
// libmoss-transcribe.so isn't loadable from cwd ($LD_LIBRARY_PATH or a symlink
// in ./).
func ensureLibLoaded() {
	libLoadOnce.Do(func() {
		libName := os.Getenv("MOSS_TRANSCRIBE_LIBRARY")
		if libName == "" {
			libName = "libmoss-transcribe.so"
		}
		lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			libLoadErr = err
			return
		}
		purego.RegisterLibFunc(&CppAbiVersion, lib, "moss_transcribe_capi_abi_version")
		purego.RegisterLibFunc(&CppLoad, lib, "moss_transcribe_capi_load")
		purego.RegisterLibFunc(&CppFree, lib, "moss_transcribe_capi_free")
		purego.RegisterLibFunc(&CppTranscribePath, lib, "moss_transcribe_capi_transcribe_path")
		purego.RegisterLibFunc(&CppTranscribePcm, lib, "moss_transcribe_capi_transcribe_pcm")
		purego.RegisterLibFunc(&CppFreeString, lib, "moss_transcribe_capi_free_string")
		purego.RegisterLibFunc(&CppLastError, lib, "moss_transcribe_capi_last_error")
	})
	if libLoadErr != nil {
		Skip("libmoss-transcribe.so not loadable: " + libLoadErr.Error())
	}
}

// fixturesOrSkip returns the model + audio paths or skips the spec if either
// env var is unset. The smoke test never runs in default CI; it needs a real
// MOSS GGUF and a WAV on disk.
func fixturesOrSkip() (string, string) {
	modelPath := os.Getenv("MOSS_BACKEND_TEST_MODEL")
	audioPath := os.Getenv("MOSS_BACKEND_TEST_WAV")
	if modelPath == "" || audioPath == "" {
		Skip("set MOSS_BACKEND_TEST_MODEL and MOSS_BACKEND_TEST_WAV to run this spec")
	}
	return modelPath, audioPath
}

// writeMono16kWav writes `samples` frames of 16 kHz mono 16-bit silence to
// path. The result is already in AudioToWav's target format, so the conversion
// helper copies it through without invoking ffmpeg.
func writeMono16kWav(path string, samples int) {
	GinkgoHelper()
	f, err := os.Create(path)
	Expect(err).ToNot(HaveOccurred())
	enc := wav.NewEncoder(f, 16000, 16, 1, 1)
	buf := &audio.IntBuffer{
		Format:         &audio.Format{NumChannels: 1, SampleRate: 16000},
		SourceBitDepth: 16,
		Data:           make([]int, samples),
	}
	Expect(enc.Write(buf)).To(Succeed())
	Expect(enc.Close()).To(Succeed())
	Expect(f.Close()).To(Succeed())
}

var _ = Describe("MossTranscribeCpp", func() {
	Context("ABI / load smoke (needs libmoss-transcribe.so)", func() {
		It("reports a positive ABI version", func() {
			ensureLibLoaded()
			Expect(CppAbiVersion()).To(BeNumerically(">=", 1))
		})

		It("transcribes a WAV into speaker-labelled segments", func() {
			modelPath, audioPath := fixturesOrSkip()
			ensureLibLoaded()

			m := &MossTranscribeCpp{}
			Expect(m.Load(&pb.ModelOptions{ModelFile: modelPath})).To(Succeed())
			defer func() { _ = m.Free() }()

			res, err := m.AudioTranscription(context.Background(), &pb.TranscriptRequest{
				Dst: audioPath,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.TrimSpace(res.Text)).ToNot(BeEmpty(),
				"expected non-empty transcript for %s", audioPath)
			Expect(res.Segments).ToNot(BeEmpty(), "expected at least one segment")
			var prevEnd int64
			for i, seg := range res.Segments {
				Expect(strings.TrimSpace(seg.Text)).ToNot(BeEmpty(),
					"segment %d must have text", i)
				Expect(seg.End).To(BeNumerically(">=", seg.Start),
					"segment %d end must not precede its start", i)
				Expect(seg.Start).To(BeNumerically(">=", prevEnd),
					"segments must be in time order")
				prevEnd = seg.End
			}
		})
	})

	Context("Load validation", func() {
		It("rejects an empty ModelFile", func() {
			m := &MossTranscribeCpp{}
			Expect(m.Load(&pb.ModelOptions{})).To(HaveOccurred())
		})
	})

	Context("AudioTranscription guards (no C library required)", func() {
		It("returns ModelNotLoaded when no context is loaded", func() {
			m := &MossTranscribeCpp{}
			_, err := m.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: "x.wav"})
			Expect(err).To(HaveOccurred())
		})

		It("errors when the audio path is empty", func() {
			m := &MossTranscribeCpp{ctxPtr: 1}
			_, err := m.AudioTranscription(context.Background(), &pb.TranscriptRequest{})
			Expect(err).To(HaveOccurred())
		})
	})

	Context("convertToWavMono16k", func() {
		It("returns a decodable 16kHz mono WAV copy and cleans it up", func() {
			dir := GinkgoT().TempDir()
			src := filepath.Join(dir, "input.wav")
			writeMono16kWav(src, 16000) // 1s of silence at 16 kHz

			converted, cleanup, err := convertToWavMono16k(src)
			Expect(err).ToNot(HaveOccurred())

			Expect(converted).ToNot(Equal(src))
			Expect(converted).To(BeAnExistingFile())

			cleanup()
			Expect(converted).ToNot(BeAnExistingFile(), "cleanup removes the temp dir")
		})

		It("errors on a non-existent input rather than passing the path through", func() {
			_, _, err := convertToWavMono16k(filepath.Join(GinkgoT().TempDir(), "missing.mp3"))
			Expect(err).To(HaveOccurred())
		})
	})
})
