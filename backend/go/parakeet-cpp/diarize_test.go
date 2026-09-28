package main

import (
	"path/filepath"
	"sync"
	"unsafe"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The Diarize specs drive it entirely against stubbed CppDiarizePCM /
// CppTranscribeAndDiarizeJSON / CppFreeString / CppLastError (the same seam
// live_test.go and roles_test.go use), so they run without libparakeet.so.

// diarizeCstrPool hands out NUL-terminated C-style strings backed by Go
// memory and keeps them alive for the duration of a spec (goStringFromCPtr
// reads through the raw pointer; mirrors live_test.go's liveCstrPool).
type diarizeCstrPool struct {
	mu   sync.Mutex
	bufs [][]byte
}

func (p *diarizeCstrPool) cstr(s string) uintptr {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := append([]byte(s), 0)
	p.bufs = append(p.bufs, b)
	return uintptr(unsafe.Pointer(&b[0]))
}

// diarizeStubs swaps every C entry point Diarize touches and returns a
// restore func for AfterEach (mirrors live_test.go's liveStubs).
func diarizeStubs() (restore func()) {
	savedDiarize := CppDiarizePCM
	savedTranscribeAndDiarize := CppTranscribeAndDiarizeJSON
	savedFreeString := CppFreeString
	savedLastError := CppLastError
	return func() {
		CppDiarizePCM = savedDiarize
		CppTranscribeAndDiarizeJSON = savedTranscribeAndDiarize
		CppFreeString = savedFreeString
		CppLastError = savedLastError
	}
}

// diarizeWav writes a silent 16 kHz mono WAV of the given duration (seconds)
// to a fresh temp file and returns its path. decodeWavMono16k reads real
// audio bytes off disk, so Diarize needs a file on disk even though the
// stubbed C calls never look at its samples.
func diarizeWav(seconds float64) string {
	GinkgoHelper()
	path := filepath.Join(GinkgoT().TempDir(), "diarize.wav")
	writeMono16kWav(path, int(seconds*16000))
	return path
}

var _ = Describe("ParakeetCpp.Diarize", func() {
	var restore func()
	var pool *diarizeCstrPool

	BeforeEach(func() {
		restore = diarizeStubs()
		pool = &diarizeCstrPool{}
	})
	AfterEach(func() { restore() })

	It("fails with FailedPrecondition when no diarization model is loaded", func() {
		p := &ParakeetCpp{}
		_, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(1)})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		Expect(err.Error()).To(ContainSubstring("model is not a diarization model"))
	})

	It("fails with Unimplemented when the loaded libparakeet.so has no diarize_pcm symbol", func() {
		CppDiarizePCM = nil
		p := &ParakeetCpp{diarCtx: 42}
		_, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(1)})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})

	It("maps plain segments with sequential ids, decimal speaker labels and distinct speaker count", func() {
		CppDiarizePCM = func(ctx uintptr, samples *float32, n int32, sampleRate int32) uintptr {
			return pool.cstr(`{"speakers":8,"segments":[` +
				`{"speaker":0,"start":0.00,"end":3.00},` +
				`{"speaker":1,"start":3.00,"end":6.00}]}`)
		}
		CppFreeString = func(uintptr) {}

		p := &ParakeetCpp{diarCtx: 42}
		resp, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(6)})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Segments).To(HaveLen(2))
		Expect(resp.Segments[0].Id).To(Equal(int32(0)))
		Expect(resp.Segments[0].Speaker).To(Equal("0"))
		Expect(resp.Segments[1].Id).To(Equal(int32(1)))
		Expect(resp.Segments[1].Speaker).To(Equal("1"))
		Expect(resp.Segments[0].Text).To(BeEmpty())
		Expect(resp.NumSpeakers).To(Equal(int32(2)))
		Expect(resp.Duration).To(BeNumerically("~", 6.0, 0.01))
	})

	It("fills text from utterances when include_text is set with an ASR companion, and maps speaker -1 to unknown", func() {
		CppDiarizePCM = func(ctx uintptr, samples *float32, n int32, sampleRate int32) uintptr {
			Fail("diarize_pcm must not be called when include_text has an ASR companion to pair with")
			return 0
		}
		CppTranscribeAndDiarizeJSON = func(asr, diar uintptr, samples *float32, n int32, sampleRate int32) uintptr {
			return pool.cstr(`{"speakers":8,"utterances":[` +
				`{"speaker":0,"text":"hello there","start":0.00,"end":1.00,"conf":0.9},` +
				`{"speaker":-1,"text":"mumble","start":1.00,"end":1.50,"conf":0.4}],"words":[]}`)
		}
		CppFreeString = func(uintptr) {}

		p := &ParakeetCpp{diarCtx: 42, ctxPtr: 7}
		resp, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(2), IncludeText: true})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Segments).To(HaveLen(2))
		Expect(resp.Segments[0].Speaker).To(Equal("0"))
		Expect(resp.Segments[0].Text).To(Equal("hello there"))
		Expect(resp.Segments[1].Speaker).To(Equal("unknown"))
		Expect(resp.Segments[1].Text).To(Equal("mumble"))
	})

	It("falls back to plain segments without error when include_text is set but no ASR companion is loaded", func() {
		diarizeCalled := false
		CppDiarizePCM = func(ctx uintptr, samples *float32, n int32, sampleRate int32) uintptr {
			diarizeCalled = true
			return pool.cstr(`{"speakers":8,"segments":[{"speaker":0,"start":0.00,"end":1.00}]}`)
		}
		CppFreeString = func(uintptr) {}
		CppTranscribeAndDiarizeJSON = func(asr, diar uintptr, samples *float32, n int32, sampleRate int32) uintptr {
			Fail("transcribe_and_diarize_json must not be called without an ASR companion")
			return 0
		}

		p := &ParakeetCpp{diarCtx: 42} // no ctxPtr companion
		resp, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(1), IncludeText: true})
		Expect(err).ToNot(HaveOccurred())
		Expect(diarizeCalled).To(BeTrue())
		Expect(resp.Segments).To(HaveLen(1))
		Expect(resp.Segments[0].Text).To(BeEmpty())
	})

	It("drops a segment shorter than min_duration_on", func() {
		CppDiarizePCM = func(ctx uintptr, samples *float32, n int32, sampleRate int32) uintptr {
			return pool.cstr(`{"speakers":8,"segments":[` +
				`{"speaker":0,"start":0.30,"end":0.50},` + // 0.2s, at 0.3
				`{"speaker":0,"start":1.00,"end":2.00}]}`) // 1.0s, kept
		}
		CppFreeString = func(uintptr) {}

		p := &ParakeetCpp{diarCtx: 42}
		resp, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(3), MinDurationOn: 0.3})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Segments).To(HaveLen(1))
		Expect(resp.Segments[0].Id).To(Equal(int32(0)))
		Expect(resp.Segments[0].Start).To(BeNumerically("~", 1.0, 0.001))
	})

	It("merges same-speaker segments across a short gap but not across a speaker change", func() {
		CppDiarizePCM = func(ctx uintptr, samples *float32, n int32, sampleRate int32) uintptr {
			return pool.cstr(`{"speakers":8,"segments":[` +
				`{"speaker":0,"start":0.00,"end":1.00},` +
				`{"speaker":0,"start":1.30,"end":2.00},` + // 0.3s gap, same speaker: merges
				`{"speaker":1,"start":2.10,"end":3.00}]}`) // 0.1s gap, different speaker: stays separate
		}
		CppFreeString = func(uintptr) {}

		p := &ParakeetCpp{diarCtx: 42}
		resp, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(3), MinDurationOff: 0.5})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Segments).To(HaveLen(2))
		Expect(resp.Segments[0].Id).To(Equal(int32(0)))
		Expect(resp.Segments[0].Speaker).To(Equal("0"))
		Expect(resp.Segments[0].Start).To(BeNumerically("~", 0.0, 0.001))
		Expect(resp.Segments[0].End).To(BeNumerically("~", 2.0, 0.001))
		Expect(resp.Segments[1].Id).To(Equal(int32(1)))
		Expect(resp.Segments[1].Speaker).To(Equal("1"))
	})

	It("surfaces last_error when the C call returns NULL", func() {
		CppDiarizePCM = func(ctx uintptr, samples *float32, n int32, sampleRate int32) uintptr {
			return 0
		}
		CppLastError = func(ctx uintptr) string { return "boom" }

		p := &ParakeetCpp{diarCtx: 42}
		_, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(1)})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("boom"))
	})
})
