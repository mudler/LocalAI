package main

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWhisper(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Whisper Backend Suite")
}

var (
	libLoadOnce sync.Once
	libLoadErr  error
)

// ensureLibLoaded mirrors main.go's bootstrap so a Go test can drive the
// bridge without spinning up the gRPC server. Skips the current spec when the
// shared library isn't present (e.g. running before `make backends/whisper`).
func ensureLibLoaded() {
	libLoadOnce.Do(func() {
		libName := os.Getenv("WHISPER_LIBRARY")
		if libName == "" {
			libName = "./libgowhisper-fallback.so"
		}
		if _, err := os.Stat(libName); err != nil {
			libLoadErr = err
			return
		}
		gosd, err := purego.Dlopen(libName, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			libLoadErr = err
			return
		}
		purego.RegisterLibFunc(&CppLoadModel, gosd, "load_model")
		purego.RegisterLibFunc(&CppTranscribe, gosd, "transcribe")
		purego.RegisterLibFunc(&CppGetSegmentText, gosd, "get_segment_text")
		purego.RegisterLibFunc(&CppGetSegmentStart, gosd, "get_segment_t0")
		purego.RegisterLibFunc(&CppGetSegmentEnd, gosd, "get_segment_t1")
		purego.RegisterLibFunc(&CppNTokens, gosd, "n_tokens")
		purego.RegisterLibFunc(&CppGetTokenID, gosd, "get_token_id")
		purego.RegisterLibFunc(&CppGetSegmentSpeakerTurnNext, gosd, "get_segment_speaker_turn_next")
		purego.RegisterLibFunc(&CppSetAbort, gosd, "set_abort")
		purego.RegisterLibFunc(&CppSetNewSegmentCallback, gosd, "set_new_segment_callback")
	})
	if libLoadErr != nil {
		Skip("whisper library not loadable: " + libLoadErr.Error())
	}
}

// fixturesOrSkip returns the model + audio paths or skips the spec if either
// env var is unset. The test never runs in default CI — it requires a real
// whisper model and a long audio file (~3 minutes) on disk.
func fixturesOrSkip() (string, string) {
	modelPath := os.Getenv("WHISPER_MODEL_PATH")
	audioPath := os.Getenv("WHISPER_AUDIO_PATH")
	if modelPath == "" || audioPath == "" {
		Skip("set WHISPER_MODEL_PATH and WHISPER_AUDIO_PATH to run this spec")
	}
	return modelPath, audioPath
}

var _ = Describe("Whisper", func() {
	Context("AudioTranscription cancellation", func() {
		It("returns codes.Canceled and resets the abort flag for the next call", func() {
			modelPath, audioPath := fixturesOrSkip()
			ensureLibLoaded()

			w := &Whisper{}
			Expect(w.Load(&pb.ModelOptions{ModelFile: modelPath})).To(Succeed())

			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				time.Sleep(100 * time.Millisecond)
				cancel()
			}()

			start := time.Now()
			_, err := w.AudioTranscription(ctx, &pb.TranscriptRequest{
				Dst:      audioPath,
				Threads:  4,
				Language: "en",
			})
			elapsed := time.Since(start)

			Expect(err).To(HaveOccurred(), "transcription completed in %s without cancel — try a longer audio file", elapsed)
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue(), "expected gRPC status error, got %v", err)
			Expect(st.Code()).To(Equal(codes.Canceled), "expected codes.Canceled, got %v", err)
			Expect(elapsed).To(BeNumerically("<", 5*time.Second), "cancellation took %s, expected <5s", elapsed)

			// Subsequent transcription must succeed — proves g_abort reset.
			res, err := w.AudioTranscription(context.Background(), &pb.TranscriptRequest{
				Dst:      audioPath,
				Threads:  4,
				Language: "en",
			})
			Expect(err).ToNot(HaveOccurred(), "post-cancel transcription failed")
			Expect(res.Text).ToNot(BeEmpty(), "post-cancel transcription returned empty text")
		})
	})

	Context("AudioTranscriptionStream", func() {
		It("emits multiple deltas progressively for a multi-segment clip", func() {
			modelPath, audioPath := fixturesOrSkip()
			ensureLibLoaded()

			// The streaming method dispatches through the package-level
			// goNewSegmentCb. main.go normally builds it; in this test
			// process main() is never called, so build it here lazily.
			// purego.NewCallback returns a stable pointer; calling it once
			// per process is correct.
			if goNewSegmentCb == 0 {
				goNewSegmentCb = purego.NewCallback(onNewSegment)
			}

			w := &Whisper{}
			Expect(w.Load(&pb.ModelOptions{ModelFile: modelPath})).To(Succeed())

			results := make(chan *pb.TranscriptStreamResponse, 64)
			done := make(chan error, 1)
			go func() {
				done <- w.AudioTranscriptionStream(context.Background(), &pb.TranscriptRequest{
					Dst:      audioPath,
					Threads:  4,
					Language: "en",
					Stream:   true,
				}, results)
			}()

			var deltas []string
			var assembled strings.Builder
			var finalText string
			var finalSegmentCount int
			for chunk := range results {
				if d := chunk.GetDelta(); d != "" {
					deltas = append(deltas, d)
					assembled.WriteString(d)
				}
				if final := chunk.GetFinalResult(); final != nil {
					finalText = final.GetText()
					finalSegmentCount = len(final.GetSegments())
				}
			}
			Expect(<-done).ToNot(HaveOccurred())

			// The whisper-specific bar: real streaming via new_segment_callback
			// fires once per decoded segment, so a multi-segment clip MUST
			// produce >=2 delta events. A faked-streaming impl (run
			// whisper_full to completion, then walk the segment list) would
			// also pass len(deltas) >= 1, which is why the generic e2e spec
			// is not strict enough.
			Expect(len(deltas)).To(BeNumerically(">=", 2),
				"expected multiple deltas from a multi-segment clip, got %d (assembled=%q)",
				len(deltas), assembled.String())
			Expect(finalSegmentCount).To(BeNumerically(">=", 2),
				"expected final to carry multiple segments")
			Expect(assembled.String()).To(Equal(finalText),
				"concat(deltas) must equal final.Text")
		})
	})
})
