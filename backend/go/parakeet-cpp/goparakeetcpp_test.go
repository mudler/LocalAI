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

func TestParakeetCpp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "parakeet-cpp Backend Suite")
}

var (
	libLoadOnce sync.Once
	libLoadErr  error
)

// ensureLibLoaded mirrors main.go's bootstrap so a Go test can drive
// the C-API bridge without spinning up the gRPC server. Skips the
// current spec when libparakeet.so isn't loadable from cwd
// ($LD_LIBRARY_PATH or a symlink in ./).
func ensureLibLoaded() {
	libLoadOnce.Do(func() {
		libName := os.Getenv("PARAKEET_LIBRARY")
		if libName == "" {
			libName = "libparakeet.so"
		}
		lib, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			libLoadErr = err
			return
		}
		purego.RegisterLibFunc(&CppAbiVersion, lib, "parakeet_capi_abi_version")
		purego.RegisterLibFunc(&CppLoad, lib, "parakeet_capi_load")
		purego.RegisterLibFunc(&CppFree, lib, "parakeet_capi_free")
		purego.RegisterLibFunc(&CppTranscribePath, lib, "parakeet_capi_transcribe_path")
		purego.RegisterLibFunc(&CppTranscribePathJSON, lib, "parakeet_capi_transcribe_path_json")
		if sym, err := purego.Dlsym(lib, "parakeet_capi_transcribe_pcm_batch_json"); err == nil && sym != 0 {
			purego.RegisterLibFunc(&CppTranscribePcmBatchJSON, lib, "parakeet_capi_transcribe_pcm_batch_json")
		}
		purego.RegisterLibFunc(&CppStreamBegin, lib, "parakeet_capi_stream_begin")
		purego.RegisterLibFunc(&CppStreamFeed, lib, "parakeet_capi_stream_feed")
		purego.RegisterLibFunc(&CppStreamFinalize, lib, "parakeet_capi_stream_finalize")
		purego.RegisterLibFunc(&CppStreamFree, lib, "parakeet_capi_stream_free")
		if sym, err := purego.Dlsym(lib, "parakeet_capi_stream_feed_json"); err == nil && sym != 0 {
			purego.RegisterLibFunc(&CppStreamFeedJSON, lib, "parakeet_capi_stream_feed_json")
			purego.RegisterLibFunc(&CppStreamFinalizeJSON, lib, "parakeet_capi_stream_finalize_json")
		}
		purego.RegisterLibFunc(&CppFreeString, lib, "parakeet_capi_free_string")
		purego.RegisterLibFunc(&CppLastError, lib, "parakeet_capi_last_error")
	})
	if libLoadErr != nil {
		Skip("libparakeet.so not loadable: " + libLoadErr.Error())
	}
}

// fixturesOrSkip returns the model + audio paths or skips the spec if
// either env var is unset. The smoke test never runs in default CI; it
// needs a real parakeet GGUF and a 16 kHz mono WAV on disk.
func fixturesOrSkip() (string, string) {
	modelPath := os.Getenv("PARAKEET_BACKEND_TEST_MODEL")
	audioPath := os.Getenv("PARAKEET_BACKEND_TEST_WAV")
	if modelPath == "" || audioPath == "" {
		Skip("set PARAKEET_BACKEND_TEST_MODEL and PARAKEET_BACKEND_TEST_WAV to run this spec")
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

var _ = Describe("ParakeetCpp", func() {
	Context("AudioTranscription", func() {
		It("transcribes a WAV via the parakeet C-API", func() {
			modelPath, audioPath := fixturesOrSkip()
			ensureLibLoaded()

			p := &ParakeetCpp{}
			Expect(p.Load(&pb.ModelOptions{ModelFile: modelPath})).To(Succeed())
			defer func() { _ = p.Free() }()

			res, err := p.AudioTranscription(context.Background(), &pb.TranscriptRequest{
				Dst: audioPath,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.TrimSpace(res.Text)).ToNot(BeEmpty(),
				"expected non-empty transcript for %s", audioPath)
			// NeMo-faithful segmentation: one or more punctuation-delimited
			// segments, each with text and a monotonically-advancing time span.
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
				// Default (no granularities) is segment-level: no per-word timings.
				Expect(seg.Words).To(BeEmpty(),
					"word timings are opt-in via timestamp_granularities")
			}
		})

		It("emits word-level timestamps when granularity=word", func() {
			modelPath, audioPath := fixturesOrSkip()
			ensureLibLoaded()

			p := &ParakeetCpp{}
			Expect(p.Load(&pb.ModelOptions{ModelFile: modelPath})).To(Succeed())
			defer func() { _ = p.Free() }()

			res, err := p.AudioTranscription(context.Background(), &pb.TranscriptRequest{
				Dst:                    audioPath,
				TimestampGranularities: []string{"word"},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Segments).ToNot(BeEmpty())
			// With word granularity every segment carries its own words, and each
			// segment's span tracks its first/last word; word starts advance
			// monotonically across the whole transcript.
			totalWords := 0
			var prevStart int64 = -1
			for i, seg := range res.Segments {
				Expect(seg.Words).ToNot(BeEmpty(),
					"segment %d must carry per-word timestamps with granularity=word", i)
				Expect(seg.Start).To(Equal(seg.Words[0].Start),
					"segment %d start tracks its first word", i)
				Expect(seg.End).To(Equal(seg.Words[len(seg.Words)-1].End),
					"segment %d end tracks its last word", i)
				for _, w := range seg.Words {
					Expect(w.End).To(BeNumerically(">=", w.Start))
					Expect(w.Start).To(BeNumerically(">=", prevStart))
					prevStart = w.Start
					totalWords++
				}
			}
			Expect(totalWords).To(BeNumerically(">", 0))
			Expect(res.Segments[0].Words[0].Start).To(BeNumerically(">=", int64(0)))
		})
	})

	Context("convertToWavMono16k", func() {
		// The non-batched transcription path hands a file path to the C
		// library's WAV-only audio loader, so it must convert first.
		// utils.AudioToWav passes an already-16kHz/mono/16-bit WAV through
		// without ffmpeg, which lets us exercise the helper (and the
		// regression: the direct path used to skip conversion entirely)
		// without a model, the C library, or ffmpeg.
		It("returns a decodable 16kHz mono WAV copy and cleans it up", func() {
			dir := GinkgoT().TempDir()
			src := filepath.Join(dir, "input.wav")
			writeMono16kWav(src, 16000) // 1s of silence at 16 kHz

			converted, cleanup, err := convertToWavMono16k(src)
			Expect(err).ToNot(HaveOccurred())

			// It must produce a fresh temp file, not return the original path.
			Expect(converted).ToNot(Equal(src))
			Expect(converted).To(BeAnExistingFile())

			pcm, _, err := decodeWavMono16k(converted)
			Expect(err).ToNot(HaveOccurred())
			Expect(pcm).To(HaveLen(16000), "round-trips the sample count")

			cleanup()
			Expect(converted).ToNot(BeAnExistingFile(), "cleanup removes the temp dir")
		})

		It("errors on a non-existent input rather than passing the path through", func() {
			_, _, err := convertToWavMono16k(filepath.Join(GinkgoT().TempDir(), "missing.mp3"))
			Expect(err).To(HaveOccurred())
		})
	})

	Context("AudioTranscriptionStream", func() {
		It("streams deltas and a closing FinalResult from a cache-aware model", func() {
			// Streaming needs a cache-aware streaming model (e.g.
			// realtime_eou); the offline test model would fail stream_begin.
			modelPath := os.Getenv("PARAKEET_BACKEND_TEST_STREAM_MODEL")
			audioPath := os.Getenv("PARAKEET_BACKEND_TEST_WAV")
			if modelPath == "" || audioPath == "" {
				Skip("set PARAKEET_BACKEND_TEST_STREAM_MODEL (cache-aware streaming model) and PARAKEET_BACKEND_TEST_WAV")
			}
			ensureLibLoaded()

			p := &ParakeetCpp{}
			Expect(p.Load(&pb.ModelOptions{ModelFile: modelPath})).To(Succeed())
			defer func() { _ = p.Free() }()

			results := make(chan *pb.TranscriptStreamResponse, 64)
			errCh := make(chan error, 1)
			go func() {
				errCh <- p.AudioTranscriptionStream(context.Background(),
					&pb.TranscriptRequest{Dst: audioPath}, results)
			}()

			var deltas []string
			var final *pb.TranscriptResult
			for r := range results {
				if r.Delta != "" {
					deltas = append(deltas, r.Delta)
				}
				if r.FinalResult != nil {
					final = r.FinalResult
				}
			}
			Expect(<-errCh).ToNot(HaveOccurred())

			Expect(final).ToNot(BeNil(), "expected a closing FinalResult")
			Expect(strings.TrimSpace(final.Text)).ToNot(BeEmpty(),
				"expected a non-empty streamed transcript")
			Expect(final.Segments).ToNot(BeEmpty(),
				"FinalResult always carries at least one segment")
			// The concatenated deltas reconstruct the final transcript.
			Expect(strings.TrimSpace(strings.Join(deltas, ""))).To(Equal(strings.TrimSpace(final.Text)),
				"deltas should reconstruct the final text")
		})
	})
})
