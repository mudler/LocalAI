package main

import (
	"path/filepath"
	"sync"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
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
	savedNamedDiarize := CppDiarizeNamedPCMJSON
	savedNamedText := CppTranscribeAndDiarizeNamedJSON
	savedRegNew := CppSpeakerRegistryNew
	savedRegFree := CppSpeakerRegistryFree
	savedRegAdd := CppSpeakerRegistryAddEmbedding
	savedRegLastError := CppSpeakerRegistryLastError
	savedSpeakerDim := CppSpeakerDim
	return func() {
		CppDiarizeNamedPCMJSON = savedNamedDiarize
		CppTranscribeAndDiarizeNamedJSON = savedNamedText
		CppSpeakerRegistryNew = savedRegNew
		CppSpeakerRegistryFree = savedRegFree
		CppSpeakerRegistryAddEmbedding = savedRegAdd
		CppSpeakerRegistryLastError = savedRegLastError
		CppSpeakerDim = savedSpeakerDim
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

	It("reports last_error from both contexts when the include_text C call returns NULL", func() {
		// Diarize's Unimplemented gate checks CppDiarizePCM regardless of
		// wantText, so it needs a non-nil (never called) stub here too.
		CppDiarizePCM = func(ctx uintptr, samples *float32, n int32, sampleRate int32) uintptr {
			Fail("diarize_pcm must not be called when include_text has an ASR companion to pair with")
			return 0
		}
		CppTranscribeAndDiarizeJSON = func(asr, diar uintptr, samples *float32, n int32, sampleRate int32) uintptr {
			return 0
		}
		CppLastError = func(ctx uintptr) string {
			if ctx == 7 {
				return "asr side broke"
			}
			return "diar side broke"
		}

		p := &ParakeetCpp{diarCtx: 42, ctxPtr: 7}
		_, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(1), IncludeText: true})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("asr side broke"))
		Expect(err.Error()).To(ContainSubstring("diar side broke"))
	})

	It("wraps a decode failure as InvalidArgument", func() {
		CppDiarizePCM = func(ctx uintptr, samples *float32, n int32, sampleRate int32) uintptr {
			Fail("decode must fail before any C call is made")
			return 0
		}

		p := &ParakeetCpp{diarCtx: 42}
		_, err := p.Diarize(&pb.DiarizeRequest{Dst: filepath.Join(GinkgoT().TempDir(), "missing.wav")})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("returns ModelNotLoaded without a C call when diarCtx is zeroed between the entry check and the call", func() {
		called := false
		CppDiarizePCM = func(ctx uintptr, samples *float32, n int32, sampleRate int32) uintptr {
			called = true
			return pool.cstr(`{"speakers":8,"segments":[]}`)
		}
		CppFreeString = func(uintptr) {}

		p := &ParakeetCpp{diarCtx: 42}
		// Simulate a Free() racing between Diarize's own diarCtx==0 check and
		// diarizeCall's lock, exactly as it zeroes diarCtx under engineMu.
		p.diarCtx = 0
		_, err := p.diarizeCall(make([]float32, 10), false, 0)
		Expect(grpcerrors.IsModelNotLoaded(err)).To(BeTrue())
		Expect(called).To(BeFalse(), "no C call once diarCtx was cleared")
	})

	It("merges same-speaker segments across an intervening different speaker (A, B, A)", func() {
		CppDiarizePCM = func(ctx uintptr, samples *float32, n int32, sampleRate int32) uintptr {
			return pool.cstr(`{"speakers":8,"segments":[` +
				`{"speaker":0,"start":0.00,"end":1.00},` +
				`{"speaker":1,"start":1.05,"end":1.20},` + // short B segment sits between the two A's
				`{"speaker":0,"start":1.30,"end":2.00}]}`) // 0.1s gap from the first A: same speaker, merges
		}
		CppFreeString = func(uintptr) {}

		p := &ParakeetCpp{diarCtx: 42}
		resp, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(3), MinDurationOff: 0.5})
		Expect(err).ToNot(HaveOccurred())
		// The two speaker-0 segments merge into one spanning 0.00-2.00, and
		// the timeline re-sort puts speaker 1's untouched segment in between.
		Expect(resp.Segments).To(HaveLen(2))
		Expect(resp.Segments[0].Speaker).To(Equal("0"))
		Expect(resp.Segments[0].Start).To(BeNumerically("~", 0.0, 0.001))
		Expect(resp.Segments[0].End).To(BeNumerically("~", 2.0, 0.001))
		Expect(resp.Segments[1].Speaker).To(Equal("1"))
		Expect(resp.Segments[1].Start).To(BeNumerically("~", 1.05, 0.001))
		Expect(resp.Segments[1].End).To(BeNumerically("~", 1.20, 0.001))
	})
	Describe("with known voices", func() {
		var freed []uintptr
		var used string
		ada := []*pb.KnownVoice{{Name: "Ada", Embedding: []float32{1, 0}}}
		BeforeEach(func() {
			freed, used = nil, ""
			CppFreeString = func(uintptr) {}
			CppSpeakerDim = func(uintptr) int32 { return 2 }
			CppSpeakerRegistryNew = func() uintptr { return 9 }
			CppSpeakerRegistryFree = func(r uintptr) { freed = append(freed, r) }
			CppSpeakerRegistryAddEmbedding = func(uintptr, string, *float32, int32) int32 { return 0 }
			CppDiarizePCM = func(uintptr, *float32, int32, int32) uintptr {
				used = "plain"
				return pool.cstr(`{"speakers":8,"segments":[{"speaker":0,"start":0.5,"end":2}]}`)
			}
			CppDiarizeNamedPCMJSON = func(diar, spk, reg uintptr, s *float32, n, sr int32, accept, margin float32) uintptr {
				used = "named"
				Expect(reg).To(Equal(uintptr(9)))
				Expect(accept).To(BeNumerically("~", 0.7, 1e-6))
				Expect(margin).To(BeNumerically("~", 0.05, 1e-6))
				return pool.cstr(`{"speakers":8,"segments":[{"speaker":0,"start":0.5,"end":2.0},{"speaker":1,"start":2.5,"end":4.0}],` +
					`"names":{"0":{"name":"Ada","score":0.93},"1":{"name":"","score":0.2}}}`)
			}
		})

		It("puts the registered names on the segments and frees the registry", func() {
			p := &ParakeetCpp{diarCtx: 1, spkCtx: 2, speakerAccept: 0.7, speakerMargin: 0.05}
			res, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(5), KnownVoices: ada})
			Expect(err).ToNot(HaveOccurred())
			Expect(used).To(Equal("named"))
			Expect(res.Segments).To(HaveLen(2))
			Expect(res.Segments[0].Name).To(Equal("Ada"))
			Expect(res.Segments[0].NameScore).To(BeNumerically("~", 0.93, 1e-6))
			Expect(res.Segments[1].Name).To(BeEmpty())
			Expect(freed).To(Equal([]uintptr{9}))
		})
		It("uses the plain path when the request has no known voices", func() {
			p := &ParakeetCpp{diarCtx: 1, spkCtx: 2}
			res, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(5)})
			Expect(err).ToNot(HaveOccurred())
			Expect(used).To(Equal("plain"))
			Expect(res.Segments[0].Name).To(BeEmpty())
			Expect(freed).To(BeEmpty())
		})
		It("uses the plain path when no speaker model is loaded, even with known voices", func() {
			p := &ParakeetCpp{diarCtx: 1}
			_, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(5), KnownVoices: ada})
			Expect(err).ToNot(HaveOccurred())
			Expect(used).To(Equal("plain"))
		})
		It("uses the plain path, without a registry, when no voice has an embedding", func() {
			p := &ParakeetCpp{diarCtx: 1, spkCtx: 2}
			_, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(5),
				KnownVoices: []*pb.KnownVoice{{Name: "Ada"}, {Embedding: []float32{1, 0}}}})
			Expect(err).ToNot(HaveOccurred())
			Expect(used).To(Equal("plain"))
			Expect(freed).To(Equal([]uintptr{9})) // the empty registry built for it is released, once
		})
		It("takes the plain path, without failing, when the only voice has the wrong size", func() {
			p := &ParakeetCpp{diarCtx: 1, spkCtx: 2}
			res, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(5),
				KnownVoices: []*pb.KnownVoice{{Name: "Ada", Embedding: []float32{1, 0, 0}}}})
			Expect(err).ToNot(HaveOccurred())
			Expect(used).To(Equal("plain"))
			Expect(res.Segments[0].Name).To(BeEmpty())
			Expect(freed).To(Equal([]uintptr{9}))
		})
		It("takes the plain path when the C side refuses the only voice", func() {
			CppSpeakerRegistryAddEmbedding = func(uintptr, string, *float32, int32) int32 { return 1 }
			CppSpeakerRegistryLastError = func(uintptr) string { return "nope" }
			p := &ParakeetCpp{diarCtx: 1, spkCtx: 2}
			_, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(5), KnownVoices: ada})
			Expect(err).ToNot(HaveOccurred())
			Expect(used).To(Equal("plain"))
			Expect(freed).To(Equal([]uintptr{9}))
		})
		It("reports a missing v10 symbol instead of silently dropping the names", func() {
			CppDiarizeNamedPCMJSON = nil
			p := &ParakeetCpp{diarCtx: 1, spkCtx: 2}
			_, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(5), KnownVoices: ada})
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.Unimplemented))
			Expect(freed).To(Equal([]uintptr{9}))
		})
		It("reports a missing named transcribe symbol on the include_text path", func() {
			CppTranscribeAndDiarizeJSON = func(asr, diar uintptr, s *float32, n, sr int32) uintptr { return 0 }
			CppTranscribeAndDiarizeNamedJSON = nil
			p := &ParakeetCpp{diarCtx: 1, spkCtx: 2, ctxPtr: 3}
			_, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(5), IncludeText: true, KnownVoices: ada})
			Expect(err).To(HaveOccurred())
			Expect(status.Code(err)).To(Equal(codes.Unimplemented))
			Expect(freed).To(Equal([]uintptr{9}))
		})
		It("names utterances on the include_text path", func() {
			// wantText also requires the plain text symbol, present in any library that has the named one.
			CppTranscribeAndDiarizeJSON = func(asr, diar uintptr, s *float32, n, sr int32) uintptr { return 0 }
			CppTranscribeAndDiarizeNamedJSON = func(asr, diar, spk, reg uintptr, s *float32, n, sr int32) uintptr {
				used = "named-text"
				return pool.cstr(`{"speakers":8,"names":{"0":{"name":"Ada","score":0.9}},"utterances":[{"speaker":0,"name":"Ada","text":"hello","start":0.5,"end":2.0,"conf":0.9},{"speaker":-1,"text":"hm","start":2.5,"end":3.0,"conf":0.5}],"words":[]}`)
			}
			p := &ParakeetCpp{diarCtx: 1, spkCtx: 2, ctxPtr: 3}
			res, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(5), IncludeText: true, KnownVoices: ada})
			Expect(err).ToNot(HaveOccurred())
			Expect(used).To(Equal("named-text"))
			Expect(res.Segments[0].Name).To(Equal("Ada"))
			Expect(res.Segments[0].Text).To(Equal("hello"))
			Expect(res.Segments[1].Name).To(BeEmpty()) // speaker -1 has no name
			Expect(freed).To(Equal([]uintptr{9}))
		})
		It("keeps the name when close segments of one speaker are merged", func() {
			p := &ParakeetCpp{diarCtx: 1, spkCtx: 2, speakerAccept: 0.7, speakerMargin: 0.05}
			CppDiarizeNamedPCMJSON = func(diar, spk, reg uintptr, s *float32, n, sr int32, a, m float32) uintptr {
				return pool.cstr(`{"speakers":8,"segments":[{"speaker":0,"start":0.5,"end":2.0},{"speaker":0,"start":2.1,"end":3.0}],"names":{"0":{"name":"Ada","score":0.9}}}`)
			}
			res, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(5), MinDurationOff: 0.5, KnownVoices: ada})
			Expect(err).ToNot(HaveOccurred())
			Expect(res.Segments).To(HaveLen(1))
			Expect(res.Segments[0].Name).To(Equal("Ada"))
			Expect(res.Segments[0].NameScore).To(BeNumerically("~", 0.9, 1e-6))
		})
		It("includes the speaker context message when the named call fails", func() {
			CppDiarizeNamedPCMJSON = func(diar, spk, reg uintptr, s *float32, n, sr int32, a, m float32) uintptr { return 0 }
			CppLastError = func(ctx uintptr) string {
				switch ctx {
				case 1:
					return "diar side broke"
				case 2:
					return "speaker side broke"
				}
				return ""
			}
			p := &ParakeetCpp{diarCtx: 1, spkCtx: 2}
			_, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(5), KnownVoices: ada})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("diar side broke"))
			Expect(err.Error()).To(ContainSubstring("speaker side broke"))
			Expect(freed).To(Equal([]uintptr{9}))
		})
	})
})
