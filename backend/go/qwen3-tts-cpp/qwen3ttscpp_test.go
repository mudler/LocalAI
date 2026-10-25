package main

import (
	"bytes"
	"context"
	"encoding/binary"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"sync"
	"testing"
	"time"
	"unsafe"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestQwen3TtsCpp(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "qwen3-tts-cpp suite")
}

var _ = Describe("normalizeLanguage", func() {
	DescribeTable("maps caller language to qwentts language names",
		func(in, want string) {
			Expect(normalizeLanguage(in)).To(Equal(want))
		},
		Entry("empty stays empty", "", ""),
		Entry("auto maps to empty", "auto", ""),
		Entry("english full name", "English", "english"),
		Entry("english code", "en", "english"),
		Entry("locale suffix stripped", "en-US", "english"),
		Entry("underscore locale", "zh_CN", "chinese"),
		Entry("mandarin alias", "mandarin", "chinese"),
		Entry("japanese already full", "japanese", "japanese"),
		Entry("unknown passes through normalized", "xx", "xx"),
	)
})

var _ = Describe("resolveVoice", func() {
	It("treats a bare token as a named speaker", func() {
		sp, ref := resolveVoice("serena")
		Expect(sp).To(Equal("serena"))
		Expect(ref).To(BeEmpty())
	})
	It("treats an audio path as a clone reference (case-insensitive ext)", func() {
		sp, ref := resolveVoice("/x/ref.WAV")
		Expect(sp).To(BeEmpty())
		Expect(ref).To(Equal("/x/ref.WAV"))
	})
	It("recognizes mp3/flac/ogg/m4a", func() {
		for _, p := range []string{"a.mp3", "b.flac", "c.ogg", "d.m4a"} {
			sp, ref := resolveVoice(p)
			Expect(sp).To(BeEmpty())
			Expect(ref).To(Equal(p))
		}
	})
	It("returns empty for empty input", func() {
		sp, ref := resolveVoice("  ")
		Expect(sp).To(BeEmpty())
		Expect(ref).To(BeEmpty())
	})
})

var _ = Describe("parseOptions", func() {
	It("extracts codec, use_fa, clamp_fp16, seed", func() {
		o := parseOptions([]string{
			"tokenizer:tok.gguf", "use_fa:false", "clamp_fp16:true",
			"seed:7", "unknown:ignored",
		})
		Expect(o.codecPath).To(Equal("tok.gguf"))
		Expect(o.useFA).To(BeFalse())
		Expect(o.clampFP16).To(BeTrue())
		Expect(o.seed).To(Equal(int64(7)))
	})
	It("accepts codec: as an alias for tokenizer:", func() {
		Expect(parseOptions([]string{"codec:c.gguf"}).codecPath).To(Equal("c.gguf"))
	})
	It("defaults use_fa true and seed -1", func() {
		o := parseOptions(nil)
		Expect(o.useFA).To(BeTrue())
		Expect(o.seed).To(Equal(int64(-1)))
	})
})

var _ = Describe("parseSampling", func() {
	It("applies qt defaults when params are absent", func() {
		s := parseSampling(nil, -1)
		Expect(s.temperature).To(BeNumerically("~", 0.9, 1e-6))
		Expect(s.topK).To(Equal(50))
		Expect(s.topP).To(BeNumerically("~", 1.0, 1e-6))
		Expect(s.repPen).To(BeNumerically("~", 1.05, 1e-6))
		Expect(s.maxNew).To(Equal(2048))
		Expect(s.seed).To(Equal(int64(-1)))
	})
	It("reads overrides and falls back to default seed", func() {
		s := parseSampling(map[string]string{
			"temperature": "0.5", "top_k": "10", "top_p": "0.8",
			"repetition_penalty": "1.2", "max_new_tokens": "512",
		}, 99)
		Expect(s.temperature).To(BeNumerically("~", 0.5, 1e-6))
		Expect(s.topK).To(Equal(10))
		Expect(s.topP).To(BeNumerically("~", 0.8, 1e-6))
		Expect(s.repPen).To(BeNumerically("~", 1.2, 1e-6))
		Expect(s.maxNew).To(Equal(512))
		Expect(s.seed).To(Equal(int64(99)))
	})
	It("reads an explicit seed override", func() {
		Expect(parseSampling(map[string]string{"seed": "123"}, -1).seed).To(Equal(int64(123)))
	})
})

var _ = Describe("TTS request sampling", func() {
	It("forwards a request generation bound to the native synthesizer", func() {
		original := CppTTS
		DeferCleanup(func() { CppTTS = original })
		var received int
		CppTTS = func(_, _, _, _ string, _ unsafe.Pointer, _ int, _ string, _ int64, _ float32, _ int, _, _ float32, maxNew int, _ unsafe.Pointer) uintptr {
			received = maxNew
			return 0
		}
		b := &Qwen3TtsCpp{}
		err := b.TTS(&pb.TTSRequest{Text: "Bounded synthesis", Dst: "unused.wav", Params: map[string]string{"max_new_tokens": "128"}})
		Expect(err).To(MatchError("qwen3-tts: synthesis failed"))
		Expect(received).To(Equal(128))
	})
})

var _ = Describe("wavHeader24k", func() {
	It("emits a 44-byte streaming WAV header at 24 kHz mono 16-bit", func() {
		h := wavHeader24k()
		Expect(h).To(HaveLen(44))
		Expect(string(h[0:4])).To(Equal("RIFF"))
		Expect(string(h[8:12])).To(Equal("WAVE"))
		Expect(string(h[12:16])).To(Equal("fmt "))
		Expect(string(h[36:40])).To(Equal("data"))
		var sampleRate uint32
		Expect(binary.Read(bytes.NewReader(h[24:28]), binary.LittleEndian, &sampleRate)).To(Succeed())
		Expect(sampleRate).To(Equal(uint32(24000)))
	})
})

var _ = Describe("floatToPCM16LE", func() {
	It("clamps and converts float PCM to little-endian int16 bytes", func() {
		b := floatToPCM16LE([]float32{0, 1.0, -1.0, 2.0, -2.0})
		Expect(b).To(HaveLen(10))
		read := func(off int) int16 {
			var v int16
			_ = binary.Read(bytes.NewReader(b[off:off+2]), binary.LittleEndian, &v)
			return v
		}
		Expect(read(0)).To(Equal(int16(0)))
		Expect(read(2)).To(Equal(int16(32767)))
		Expect(read(4)).To(Equal(int16(-32767)))
		Expect(read(6)).To(Equal(int16(32767)))  // clamped from 2.0
		Expect(read(8)).To(Equal(int16(-32767))) // clamped from -2.0
	})
})

var _ = Describe("max_batch option", func() {
	It("defaults to 1 (single-sequence synthesis)", func() {
		Expect(parseOptions(nil).maxBatch).To(Equal(1))
	})
	It("reads max_batch and the parallel alias", func() {
		Expect(parseOptions([]string{"max_batch:4"}).maxBatch).To(Equal(4))
		Expect(parseOptions([]string{"parallel:2"}).maxBatch).To(Equal(2))
	})
	It("ignores values below 1 and non-numbers", func() {
		Expect(parseOptions([]string{"max_batch:0"}).maxBatch).To(Equal(1))
		Expect(parseOptions([]string{"max_batch:many"}).maxBatch).To(Equal(1))
	})
})

var _ = Describe("Locking", func() {
	It("keeps the request lock before Load and for max_batch 1", func() {
		b := &Qwen3TtsCpp{}
		Expect(b.Locking()).To(BeTrue())
		b.maxBatch.Store(1)
		Expect(b.Locking()).To(BeTrue())
	})
	It("drops the request lock for max_batch > 1", func() {
		b := &Qwen3TtsCpp{}
		b.maxBatch.Store(4)
		Expect(b.Locking()).To(BeFalse())
	})
})

var _ = Describe("TTSStream routing", func() {
	It("delivers each concurrent request its own chunks", func() {
		original := CppTTSStream
		DeferCleanup(func() { CppTTSStream = original })
		// Both fake syntheses wait until the other one has started, so the test
		// fails (deadlocks into the timeout) if requests were serialized.
		var started sync.WaitGroup
		started.Add(2)
		CppTTSStream = func(text, _, _, _ string, _ unsafe.Pointer, _ int, _ string, _ int64, _ float32, _ int, _, _ float32, _ int, _ uintptr, user uintptr) int {
			started.Done()
			started.Wait()
			v := float32(0.25)
			if text == "b" {
				v = -0.25
			}
			samples := []float32{v, v, v}
			for i := 0; i < 3; i++ {
				streamCallback(&samples[0], int32(len(samples)), user)
			}
			return 0
		}
		b := &Qwen3TtsCpp{}
		b.maxBatch.Store(2)
		run := func(text string) [][]byte {
			results := make(chan []byte)
			errc := make(chan error, 1)
			go func() { errc <- b.TTSStream(&pb.TTSRequest{Text: text}, results) }()
			var got [][]byte
			for chunk := range results {
				got = append(got, chunk)
			}
			Expect(<-errc).To(Succeed())
			return got
		}
		var gotA, gotB [][]byte
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer GinkgoRecover(); defer wg.Done(); gotA = run("a") }()
		go func() { defer GinkgoRecover(); defer wg.Done(); gotB = run("b") }()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		Eventually(done).WithTimeout(5 * time.Second).Should(BeClosed())

		expectChunks := func(got [][]byte, want []byte) {
			Expect(got).To(HaveLen(4)) // WAV header + 3 PCM chunks
			Expect(got[0]).To(Equal(wavHeader24k()))
			for _, c := range got[1:] {
				Expect(c).To(Equal(want))
			}
		}
		expectChunks(gotA, floatToPCM16LE([]float32{0.25, 0.25, 0.25}))
		expectChunks(gotB, floatToPCM16LE([]float32{-0.25, -0.25, -0.25}))
		streamRoutesMu.Lock()
		defer streamRoutesMu.Unlock()
		Expect(streamRoutes).To(BeEmpty())
	})
})

var _ = Describe("TTSLive with max_batch", func() {
	It("rejects incremental synthesis on a batched context", func() {
		original := CppTTSLive
		DeferCleanup(func() { CppTTSLive = original })
		CppTTSLive = func(_, _, _ string, _ unsafe.Pointer, _ int, _ string, _ int64, _ float32, _ int, _, _ float32, _ int, _, _, _ uintptr) int {
			Fail("native incremental synthesis must not be reached")
			return 1
		}
		b := &Qwen3TtsCpp{}
		b.maxBatch.Store(4)
		err := b.TTSLive(context.Background(),
			func() (*pb.TTSRequest, error) { return &pb.TTSRequest{Text: "hi"}, nil },
			func([]byte) error { return nil })
		Expect(err).To(MatchError(ContainSubstring("requires max_batch=1")))
	})
})
