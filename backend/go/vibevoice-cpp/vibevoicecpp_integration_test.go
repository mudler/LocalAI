package main

// Real end-to-end streaming integration test for the vibevoice-cpp
// backend. It drives the actual Go -> purego -> C path against the real
// model, proving that TTSStream delivers audio incrementally (measurable
// time-to-first-audio) while TTS only yields anything after full
// synthesis. Gated behind VIBEVOICE_IT=1 so normal `go test` and CI stay
// unaffected - it needs the built engine .so and ~1.7 GB of model files.
//
// Run:
//
//	VIBEVOICE_IT=1 \
//	VIBEVOICECPP_LIBRARY=<abs path to libgovibevoicecpp-fallback.so> \
//	  go test ./... -run TestVibevoiceCpp -v -timeout 600s
//
// Optional overrides (default to the staged bundle under
// ~/_git/vibevoice-models):
//
//	VIBEVOICE_IT_MODEL      vibevoice-realtime-0.5B-q8_0.gguf (abs path)
//	VIBEVOICE_IT_TOKENIZER  tokenizer.gguf (abs path)
//	VIBEVOICE_IT_VOICE      voice-en-Carter_man.gguf (abs path)

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// itDefaultModelDir is where Task 6 staged the model bundle. Individual
// files can be overridden via the VIBEVOICE_IT_* env vars.
const itDefaultModelDir = "/home/mudler/_git/vibevoice-models"

// itLibLoaded guards the one-time purego Dlopen + RegisterLibFunc into
// the package-level Cpp* vars. purego cannot free a loaded library and
// the Cpp* symbols are process-global, so we bind exactly once.
var itLibLoaded sync.Once

// integrationOrSkip enforces the VIBEVOICE_IT=1 gate and binds the C ABI
// symbols into the package vars the backend calls (mirrors main.go's
// libFuncs list). It Skip()s the spec when the gate is off or the .so /
// model files are missing, so the suite stays green in every other run.
func integrationOrSkip() (model, tokenizer, voice string) {
	if os.Getenv("VIBEVOICE_IT") != "1" {
		Skip("VIBEVOICE_IT!=1, skipping real-model streaming integration test")
	}

	lib := os.Getenv("VIBEVOICECPP_LIBRARY")
	if lib == "" {
		Skip("VIBEVOICECPP_LIBRARY not set, cannot dlopen the engine .so")
	}
	if _, err := os.Stat(lib); err != nil {
		Skip("engine .so not found at " + lib)
	}

	model = itEnvOr("VIBEVOICE_IT_MODEL", filepath.Join(itDefaultModelDir, "vibevoice-realtime-0.5B-q8_0.gguf"))
	tokenizer = itEnvOr("VIBEVOICE_IT_TOKENIZER", filepath.Join(itDefaultModelDir, "tokenizer.gguf"))
	voice = itEnvOr("VIBEVOICE_IT_VOICE", filepath.Join(itDefaultModelDir, "voice-en-Carter_man.gguf"))
	for _, p := range []string{model, tokenizer, voice} {
		if _, err := os.Stat(p); err != nil {
			Skip("model file missing: " + p)
		}
	}

	itLibLoaded.Do(func() {
		handle, err := purego.Dlopen(lib, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		Expect(err).ToNot(HaveOccurred(), "dlopen %s", lib)
		// Mirror the libFuncs list in main.go verbatim so the test binds
		// the exact same symbols the production backend binary does.
		purego.RegisterLibFunc(&CppLoad, handle, "vv_capi_load")
		purego.RegisterLibFunc(&CppTTS, handle, "vv_capi_tts")
		purego.RegisterLibFunc(&CppTTSStream, handle, "vv_capi_tts_stream")
		purego.RegisterLibFunc(&CppASR, handle, "vv_capi_asr")
		purego.RegisterLibFunc(&CppUnload, handle, "vv_capi_unload")
		purego.RegisterLibFunc(&CppVersion, handle, "vv_capi_version")
	})
	return model, tokenizer, voice
}

func itEnvOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// itParseWavPCM returns the int16 PCM samples from a standard WAV file by
// locating the "data" sub-chunk, so it works regardless of how many
// bytes of header/metadata the engine wrote.
func itParseWavPCM(b []byte) []int16 {
	// Find the "data" sub-chunk id, then its 4-byte little-endian size.
	idx := -1
	for i := 12; i+8 <= len(b); i += 2 {
		if string(b[i:i+4]) == "data" {
			idx = i
			break
		}
	}
	Expect(idx).To(BeNumerically(">=", 0), "no data chunk in WAV")
	pcmStart := idx + 8
	size := int(b[idx+4]) | int(b[idx+5])<<8 | int(b[idx+6])<<16 | int(b[idx+7])<<24
	if size <= 0 || pcmStart+size > len(b) {
		size = len(b) - pcmStart
	}
	n := size / 2
	out := make([]int16, n)
	for i := 0; i < n; i++ {
		out[i] = int16(uint16(b[pcmStart+i*2]) | uint16(b[pcmStart+i*2+1])<<8)
	}
	return out
}

// itRMS computes the root-mean-square of int16 PCM as a sanity signal:
// > 0 means non-silent, and NaN/Inf-free means well-formed samples.
func itRMS(pcm []int16) float64 {
	if len(pcm) == 0 {
		return 0
	}
	var sumSq float64
	for _, s := range pcm {
		v := float64(s)
		sumSq += v * v
	}
	return sqrt(sumSq / float64(len(pcm)))
}

// sqrt via Newton's method to avoid dragging math into the file's tiny
// need (keeps the dependency surface of the test minimal).
func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 40; i++ {
		z = 0.5 * (z + x/z)
	}
	return z
}

var _ = Describe("VibeVoice-cpp real-model streaming (VIBEVOICE_IT=1)", Ordered, func() {
	// The paragraph is deliberately long (multiple sentences) so the
	// engine decodes several streaming windows and TTFA is meaningfully
	// earlier than full synthesis.
	const paragraph = "The quick brown fox jumps over the lazy dog near the riverbank. " +
		"A gentle breeze carried the sound of distant bells across the quiet valley. " +
		"Streaming synthesis lets you hear the very first words while the rest is still being generated."

	var v *VibevoiceCpp

	BeforeAll(func() {
		model, tokenizer, voice := integrationOrSkip()

		v = &VibevoiceCpp{}
		err := v.Load(&pb.ModelOptions{
			ModelFile: model,
			ModelPath: filepath.Dir(model),
			Options: []string{
				"tokenizer=" + tokenizer,
				"voice=" + voice,
			},
			Threads: 4,
		})
		Expect(err).ToNot(HaveOccurred(), "Load must succeed with the real model")
	})

	It("streams audio incrementally and beats the batch path to first audio", func() {
		// ---- Streaming run -------------------------------------------
		results := make(chan []byte, 256)
		streamErr := make(chan error, 1)

		start := time.Now()
		go func() {
			streamErr <- v.TTSStream(&pb.TTSRequest{Text: paragraph}, results)
		}()

		var (
			header      []byte
			ttfa        time.Duration
			totalStream time.Duration
			chunkCount  int
			pcmBytes    int
			firstPCM    bool
		)
		for buf := range results {
			if header == nil {
				header = buf
				continue
			}
			if len(buf) == 0 {
				continue
			}
			if !firstPCM {
				ttfa = time.Since(start)
				firstPCM = true
			}
			chunkCount++
			pcmBytes += len(buf)
		}
		totalStream = time.Since(start)
		Expect(<-streamErr).ToNot(HaveOccurred(), "TTSStream returned an error")

		// First message must be a 44-byte streaming WAV header.
		Expect(header).To(HaveLen(44), "first stream message must be the WAV header")
		Expect(string(header[0:4])).To(Equal("RIFF"))
		Expect(string(header[8:12])).To(Equal("WAVE"))

		// Streaming invariants: multiple windows, real audio, early
		// delivery (first audio strictly before the stream completes).
		Expect(chunkCount).To(BeNumerically(">=", 2), "expected multiple streamed PCM windows")
		Expect(pcmBytes).To(BeNumerically(">", 0), "no PCM bytes streamed")
		Expect(firstPCM).To(BeTrue(), "never received a PCM chunk")
		Expect(ttfa).To(BeNumerically("<", totalStream), "TTFA must precede stream completion")

		// ---- Batch baseline ------------------------------------------
		tmp, err := os.MkdirTemp("", "vv-it-batch-*")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = os.RemoveAll(tmp) })
		dst := filepath.Join(tmp, "batch.wav")

		batchStart := time.Now()
		Expect(v.TTS(&pb.TTSRequest{Text: paragraph, Dst: dst})).To(Succeed())
		totalBatch := time.Since(batchStart)

		wav, err := os.ReadFile(dst)
		Expect(err).ToNot(HaveOccurred())
		Expect(len(wav)).To(BeNumerically(">", 44), "batch wav has no PCM payload")
		Expect(string(wav[0:4])).To(Equal("RIFF"))
		Expect(string(wav[8:12])).To(Equal("WAVE"))
		batchPCM := itParseWavPCM(wav)
		Expect(len(batchPCM)).To(BeNumerically(">", 0), "batch produced no samples")
		rms := itRMS(batchPCM)
		Expect(rms).To(BeNumerically(">", 0), "batch audio is silent")
		Expect(rms).To(BeNumerically("<", 40000), "batch rms out of int16 range (corrupt samples)")

		// ---- Headline numbers ----------------------------------------
		// Emit to both GinkgoWriter and stderr: GinkgoWriter needs
		// -ginkgo.v to surface, stderr is always captured so the headline
		// TTFA vs batch numbers are never lost in an unattended run.
		ratio := float64(ttfa) / float64(totalBatch)
		for _, out := range []io.Writer{GinkgoWriter, os.Stderr} {
			fmt.Fprintf(out, "\n================ vibevoice-cpp streaming TTFA ================\n")
			fmt.Fprintf(out, "input words        : ~%d\n", len(paragraph)/6)
			fmt.Fprintf(out, "TTFA (first audio) : %v\n", ttfa)
			fmt.Fprintf(out, "total_stream       : %v\n", totalStream)
			fmt.Fprintf(out, "total_batch        : %v\n", totalBatch)
			fmt.Fprintf(out, "stream chunks      : %d\n", chunkCount)
			fmt.Fprintf(out, "stream PCM bytes   : %d\n", pcmBytes)
			fmt.Fprintf(out, "batch samples/rms  : %d / %.1f\n", len(batchPCM), rms)
			fmt.Fprintf(out, "TTFA / total_batch : %.3f (first audio in this fraction of batch's deliver time)\n", ratio)
			fmt.Fprintf(out, "==============================================================\n\n")
		}
	})
})
