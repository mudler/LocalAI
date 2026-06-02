package main

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/ebitengine/purego"
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
			Expect(res.Segments).To(HaveLen(1),
				"synthesises a single whole-clip segment")
			Expect(res.Segments[0].Text).To(Equal(res.Text),
				"single segment text must equal the top-level text")
			// Default (no granularities) is segment-level: no per-word timings.
			Expect(res.Segments[0].Words).To(BeEmpty(),
				"word timings are opt-in via timestamp_granularities")
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
			Expect(res.Segments).To(HaveLen(1))
			seg := res.Segments[0]
			Expect(seg.Words).ToNot(BeEmpty(),
				"expected per-word timestamps with granularity=word")
			// Monotonic, non-negative timings spanning the segment.
			Expect(seg.Words[0].Start).To(BeNumerically(">=", int64(0)))
			Expect(seg.End).To(BeNumerically(">=", seg.Start))
			Expect(seg.Words[len(seg.Words)-1].End).To(Equal(seg.End),
				"segment end tracks the last word")
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
