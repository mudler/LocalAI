package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ebitengine/purego"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCrispASR(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CrispASR Backend Suite")
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
		libName := os.Getenv("CRISPASR_LIBRARY")
		if libName == "" {
			libName = "./libgocrispasr-fallback.so"
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
		purego.RegisterLibFunc(&CppSetCodecPath, gosd, "set_codec_path")
		purego.RegisterLibFunc(&CppTranscribe, gosd, "transcribe")
		purego.RegisterLibFunc(&CppGetSegmentText, gosd, "get_segment_text")
		purego.RegisterLibFunc(&CppGetSegmentStart, gosd, "get_segment_t0")
		purego.RegisterLibFunc(&CppGetSegmentEnd, gosd, "get_segment_t1")
		purego.RegisterLibFunc(&CppGetBackend, gosd, "get_backend")
		purego.RegisterLibFunc(&CppSetAbort, gosd, "set_abort")
		purego.RegisterLibFunc(&CppTTSSynthesize, gosd, "tts_synthesize")
		purego.RegisterLibFunc(&CppTTSFree, gosd, "tts_free")
		purego.RegisterLibFunc(&CppTTSSetVoice, gosd, "tts_set_voice")
		purego.RegisterLibFunc(&CppTTSSetVoiceFile, gosd, "tts_set_voice_file")
	})
	if libLoadErr != nil {
		Skip("whisper library not loadable: " + libLoadErr.Error())
	}
}

// fixturesOrSkip returns the model + audio paths or skips the spec if either
// env var is unset. The test never runs in default CI — it requires a real
// whisper model and a long audio file (~3 minutes) on disk.
func fixturesOrSkip() (string, string) {
	modelPath := os.Getenv("CRISPASR_MODEL_PATH")
	audioPath := os.Getenv("CRISPASR_AUDIO_PATH")
	if modelPath == "" || audioPath == "" {
		Skip("set CRISPASR_MODEL_PATH and CRISPASR_AUDIO_PATH to run this spec")
	}
	return modelPath, audioPath
}

// ttsModelOrSkip returns the TTS model path or skips the spec when the env var
// is unset. Like the transcription fixtures, this never runs in default CI — it
// needs a real TTS model (e.g. a vibevoice GGUF) on disk.
func ttsModelOrSkip() string {
	modelPath := os.Getenv("CRISPASR_TTS_MODEL_PATH")
	if modelPath == "" {
		Skip("set CRISPASR_TTS_MODEL_PATH to run this spec")
	}
	return modelPath
}

var _ = Describe("CrispASR", func() {
	Context("AudioTranscription cancellation", func() {
		It("returns codes.Canceled on a pre-cancelled context and still succeeds afterwards", func() {
			modelPath, audioPath := fixturesOrSkip()
			ensureLibLoaded()

			w := &CrispASR{}
			Expect(w.Load(&pb.ModelOptions{ModelFile: modelPath})).To(Succeed())

			// The session transcribe is blocking and exposes no abort hook, so
			// a mid-decode cancel can't interrupt it. The contract we can rely
			// on is the pre-call ctx.Err() check: a context cancelled before
			// the call must yield codes.Canceled without starting a decode.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			_, err := w.AudioTranscription(ctx, &pb.TranscriptRequest{
				Dst:      audioPath,
				Threads:  4,
				Language: "en",
			})
			Expect(err).To(HaveOccurred(), "expected pre-cancelled context to fail")
			st, ok := status.FromError(err)
			Expect(ok).To(BeTrue(), "expected gRPC status error, got %v", err)
			Expect(st.Code()).To(Equal(codes.Canceled), "expected codes.Canceled, got %v", err)

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

			w := &CrispASR{}
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

			// One delta per non-empty segment is emitted after the blocking
			// decode returns (the session API has no per-decode callback), so a
			// multi-segment clip MUST produce >=2 delta events, and
			// concat(deltas) MUST equal final.Text exactly.
			Expect(len(deltas)).To(BeNumerically(">=", 2),
				"expected multiple deltas from a multi-segment clip, got %d (assembled=%q)",
				len(deltas), assembled.String())
			Expect(finalSegmentCount).To(BeNumerically(">=", 2),
				"expected final to carry multiple segments")
			Expect(assembled.String()).To(Equal(finalText),
				"concat(deltas) must equal final.Text")
		})
	})

	Context("TTS", func() {
		It("synthesizes a non-empty WAV", func() {
			ttsModel := ttsModelOrSkip()
			ensureLibLoaded()

			w := &CrispASR{}
			Expect(w.Load(&pb.ModelOptions{ModelFile: ttsModel})).To(Succeed())

			dst := filepath.Join(GinkgoT().TempDir(), "out.wav")
			Expect(w.TTS(&pb.TTSRequest{Text: "Hello from CrispASR.", Dst: dst})).To(Succeed())

			info, err := os.Stat(dst)
			Expect(err).ToNot(HaveOccurred(), "synthesized WAV should exist at %q", dst)
			// A real 24 kHz mono WAV is a 44-byte header plus samples; anything
			// this small would mean an empty/failed synth.
			Expect(info.Size()).To(BeNumerically(">", 1024),
				"expected a non-trivial WAV, got %d bytes", info.Size())
		})
	})
})
