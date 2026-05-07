package main

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/ebitengine/purego"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	libLoadOnce sync.Once
	libLoadErr  error
)

// ensureLibLoaded mirrors main.go's bootstrap so a Go test can drive the
// bridge without spinning up the gRPC server. Skips the test cleanly if the
// shared library isn't present (e.g. running before `make backends/whisper`).
func ensureLibLoaded(t *testing.T) {
	t.Helper()
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
	})
	if libLoadErr != nil {
		t.Skipf("whisper library not loadable: %v", libLoadErr)
	}
}

// TestAudioTranscriptionCancel ensures a context cancel mid-flight aborts
// whisper_full and surfaces codes.Canceled. The follow-up call asserts that
// the C-side abort flag resets cleanly so the next request still succeeds.
//
// Skipped unless WHISPER_MODEL_PATH and WHISPER_AUDIO_PATH are set.
func TestAudioTranscriptionCancel(t *testing.T) {
	modelPath := os.Getenv("WHISPER_MODEL_PATH")
	audioPath := os.Getenv("WHISPER_AUDIO_PATH")
	if modelPath == "" || audioPath == "" {
		t.Skip("set WHISPER_MODEL_PATH and WHISPER_AUDIO_PATH to run this test")
	}
	ensureLibLoaded(t)

	w := &Whisper{}
	if err := w.Load(&pb.ModelOptions{ModelFile: modelPath}); err != nil {
		t.Fatalf("Load: %v", err)
	}

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
	if err == nil {
		t.Fatalf("expected error, got nil (transcription completed in %s — try a longer audio file)", elapsed)
	}
	if st, ok := status.FromError(err); !ok || st.Code() != codes.Canceled {
		t.Fatalf("expected codes.Canceled, got %v", err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("cancellation took %s, expected <5s", elapsed)
	}

	// Subsequent transcription must succeed — proves g_abort reset.
	res, err := w.AudioTranscription(context.Background(), &pb.TranscriptRequest{
		Dst:      audioPath,
		Threads:  4,
		Language: "en",
	})
	if err != nil {
		t.Fatalf("post-cancel transcription failed: %v", err)
	}
	if res.Text == "" {
		t.Fatalf("post-cancel transcription returned empty text")
	}
}
