package main

import (
	"context"
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

// The SoundDetection specs drive it entirely against stubbed
// CppSoundStreamBegin / CppSoundStreamFeed / CppSoundStreamDrainScoresJSON /
// CppSoundStreamFree / CppFreeSoundSegments / CppSoundOptsDefault /
// CppNumClasses / CppFreeString / CppLastError (the same seam diarize_test.go
// and live_test.go use), so they run without libparakeet.so.

// soundCstrPool hands out NUL-terminated C-style strings backed by Go memory
// and keeps them alive for the duration of a spec (goStringFromCPtr reads
// through the raw pointer; mirrors diarize_test.go's diarizeCstrPool).
type soundCstrPool struct {
	mu   sync.Mutex
	bufs [][]byte
}

func (p *soundCstrPool) cstr(s string) uintptr {
	p.mu.Lock()
	defer p.mu.Unlock()
	b := append([]byte(s), 0)
	p.bufs = append(p.bufs, b)
	return uintptr(unsafe.Pointer(&b[0]))
}

// soundStubs swaps every C entry point SoundDetection touches and returns a
// restore func for AfterEach (mirrors diarize_test.go's diarizeStubs).
func soundStubs() (restore func()) {
	savedBegin := CppSoundStreamBegin
	savedFeed := CppSoundStreamFeed
	savedDrain := CppSoundStreamDrainScoresJSON
	savedFree := CppSoundStreamFree
	savedFreeSegs := CppFreeSoundSegments
	savedOptsDefault := CppSoundOptsDefault
	savedNumClasses := CppNumClasses
	savedFreeString := CppFreeString
	savedLastError := CppLastError
	return func() {
		CppSoundStreamBegin = savedBegin
		CppSoundStreamFeed = savedFeed
		CppSoundStreamDrainScoresJSON = savedDrain
		CppSoundStreamFree = savedFree
		CppFreeSoundSegments = savedFreeSegs
		CppSoundOptsDefault = savedOptsDefault
		CppNumClasses = savedNumClasses
		CppFreeString = savedFreeString
		CppLastError = savedLastError
	}
}

// soundWav writes a silent 16 kHz mono WAV of the given duration (seconds) to
// a fresh temp file and returns its path. decodeWavMono16k reads real audio
// bytes off disk, so SoundDetection needs a file on disk even though the
// stubbed C calls never look at its samples.
func soundWav(seconds float64) string {
	GinkgoHelper()
	path := filepath.Join(GinkgoT().TempDir(), "sound.wav")
	writeMono16kWav(path, int(seconds*16000))
	return path
}

// noopFeed is a CppSoundStreamFeed stub that always succeeds and returns no
// segments, for specs that only care about the drained window scores.
func noopFeed(s uintptr, pcm *float32, n int32, isLast int32, out *uintptr, nOut *int32) int32 {
	*out = 0
	*nOut = 0
	return 0
}

var _ = Describe("ParakeetCpp.SoundDetection", func() {
	var restore func()
	var pool *soundCstrPool

	BeforeEach(func() {
		restore = soundStubs()
		pool = &soundCstrPool{}
		CppFreeString = func(uintptr) {}
		CppFreeSoundSegments = func(uintptr) {}
		CppSoundOptsDefault = func(o *cSoundOpts) { *o = cSoundOpts{} }
	})
	AfterEach(func() { restore() })

	It("fails with FailedPrecondition when no sound model is loaded", func() {
		p := &ParakeetCpp{}
		_, err := p.SoundDetection(context.Background(), &pb.SoundDetectionRequest{Src: soundWav(1)})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.FailedPrecondition))
		Expect(err.Error()).To(ContainSubstring("model is not a sound"))
	})

	It("fails with Unimplemented when the loaded libparakeet.so has no sound_stream_begin symbol", func() {
		CppSoundStreamBegin = nil
		p := &ParakeetCpp{tagCtx: 42}
		_, err := p.SoundDetection(context.Background(), &pb.SoundDetectionRequest{Src: soundWav(1)})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})

	It("averages two windows per class and sorts descending", func() {
		CppNumClasses = func(uintptr) int32 { return 2 }
		CppSoundStreamBegin = func(tagger uintptr, o *cSoundOpts) uintptr { return 1 }
		CppSoundStreamFeed = noopFeed
		CppSoundStreamFree = func(uintptr) {}
		CppSoundStreamDrainScoresJSON = func(uintptr) uintptr {
			return pool.cstr(`[` +
				`{"start":0,"end":10,"tags":[{"index":0,"label":"Speech","score":0.8},{"index":1,"label":"Music","score":0.2}]},` +
				`{"start":10,"end":20,"tags":[{"index":0,"label":"Speech","score":0.4},{"index":1,"label":"Music","score":0.6}]}` +
				`]`)
		}

		p := &ParakeetCpp{tagCtx: 42}
		resp, err := p.SoundDetection(context.Background(), &pb.SoundDetectionRequest{Src: soundWav(1)})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Detections).To(HaveLen(2))
		Expect(resp.Detections[0].Label).To(Equal("Speech"))
		Expect(resp.Detections[0].Score).To(BeNumerically("~", 0.6, 1e-6))
		Expect(resp.Detections[1].Label).To(Equal("Music"))
		Expect(resp.Detections[1].Score).To(BeNumerically("~", 0.4, 1e-6))
	})

	It("drops classes scoring below threshold", func() {
		CppNumClasses = func(uintptr) int32 { return 2 }
		CppSoundStreamBegin = func(tagger uintptr, o *cSoundOpts) uintptr { return 1 }
		CppSoundStreamFeed = noopFeed
		CppSoundStreamFree = func(uintptr) {}
		CppSoundStreamDrainScoresJSON = func(uintptr) uintptr {
			return pool.cstr(`[{"start":0,"end":10,"tags":[` +
				`{"index":0,"label":"Speech","score":0.8},` +
				`{"index":1,"label":"Music","score":0.2}]}]`)
		}

		p := &ParakeetCpp{tagCtx: 42}
		resp, err := p.SoundDetection(context.Background(), &pb.SoundDetectionRequest{
			Src: soundWav(1), Threshold: 0.5,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Detections).To(HaveLen(1))
		Expect(resp.Detections[0].Label).To(Equal("Speech"))
	})

	It("keeps only the top_k entries", func() {
		CppNumClasses = func(uintptr) int32 { return 4 }
		CppSoundStreamBegin = func(tagger uintptr, o *cSoundOpts) uintptr { return 1 }
		CppSoundStreamFeed = noopFeed
		CppSoundStreamFree = func(uintptr) {}
		CppSoundStreamDrainScoresJSON = func(uintptr) uintptr {
			return pool.cstr(`[{"start":0,"end":10,"tags":[` +
				`{"index":0,"label":"A","score":0.9},` +
				`{"index":1,"label":"B","score":0.7},` +
				`{"index":2,"label":"C","score":0.5},` +
				`{"index":3,"label":"D","score":0.3}]}]`)
		}

		p := &ParakeetCpp{tagCtx: 42}
		resp, err := p.SoundDetection(context.Background(), &pb.SoundDetectionRequest{
			Src: soundWav(1), TopK: 3,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Detections).To(HaveLen(3))
		Expect(resp.Detections[0].Label).To(Equal("A"))
		Expect(resp.Detections[1].Label).To(Equal("B"))
		Expect(resp.Detections[2].Label).To(Equal("C"))
	})

	It("keeps all classes when top_k is 0", func() {
		CppNumClasses = func(uintptr) int32 { return 4 }
		CppSoundStreamBegin = func(tagger uintptr, o *cSoundOpts) uintptr { return 1 }
		CppSoundStreamFeed = noopFeed
		CppSoundStreamFree = func(uintptr) {}
		CppSoundStreamDrainScoresJSON = func(uintptr) uintptr {
			return pool.cstr(`[{"start":0,"end":10,"tags":[` +
				`{"index":0,"label":"A","score":0.9},` +
				`{"index":1,"label":"B","score":0.7},` +
				`{"index":2,"label":"C","score":0.5},` +
				`{"index":3,"label":"D","score":0.3}]}]`)
		}

		p := &ParakeetCpp{tagCtx: 42}
		resp, err := p.SoundDetection(context.Background(), &pb.SoundDetectionRequest{
			Src: soundWav(1), TopK: 0,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Detections).To(HaveLen(4))
	})

	It("passes window 10s, hop 10s and top_k = the tagger's class count to sound_stream_begin", func() {
		CppNumClasses = func(uintptr) int32 { return 527 }
		var gotOpts cSoundOpts
		CppSoundStreamBegin = func(tagger uintptr, o *cSoundOpts) uintptr {
			gotOpts = *o
			return 1
		}
		CppSoundStreamFeed = noopFeed
		CppSoundStreamFree = func(uintptr) {}
		CppSoundStreamDrainScoresJSON = func(uintptr) uintptr { return pool.cstr(`[]`) }

		p := &ParakeetCpp{tagCtx: 42}
		_, err := p.SoundDetection(context.Background(), &pb.SoundDetectionRequest{Src: soundWav(1)})
		Expect(err).ToNot(HaveOccurred())
		Expect(gotOpts.WindowSec).To(BeNumerically("==", 10))
		Expect(gotOpts.HopSec).To(BeNumerically("==", 10))
		Expect(gotOpts.TopK).To(Equal(int32(527)))
	})

	It("returns no detections without error for a short clip whose drain is empty", func() {
		CppNumClasses = func(uintptr) int32 { return 2 }
		CppSoundStreamBegin = func(tagger uintptr, o *cSoundOpts) uintptr { return 1 }
		CppSoundStreamFeed = noopFeed
		CppSoundStreamFree = func(uintptr) {}
		CppSoundStreamDrainScoresJSON = func(uintptr) uintptr { return pool.cstr(`[]`) }

		p := &ParakeetCpp{tagCtx: 42}
		resp, err := p.SoundDetection(context.Background(), &pb.SoundDetectionRequest{Src: soundWav(0.1)})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.Detections).To(BeEmpty())
	})

	It("surfaces last_error and still frees the stream when feed fails", func() {
		freed := false
		CppNumClasses = func(uintptr) int32 { return 2 }
		CppSoundStreamBegin = func(tagger uintptr, o *cSoundOpts) uintptr { return 1 }
		CppSoundStreamFeed = func(s uintptr, pcm *float32, n int32, isLast int32, out *uintptr, nOut *int32) int32 {
			*out = 0
			*nOut = 0
			return 1
		}
		CppSoundStreamDrainScoresJSON = func(uintptr) uintptr {
			Fail("drain_scores_json must not be called when feed failed")
			return 0
		}
		CppSoundStreamFree = func(uintptr) { freed = true }
		CppLastError = func(uintptr) string { return "boom" }

		p := &ParakeetCpp{tagCtx: 42}
		_, err := p.SoundDetection(context.Background(), &pb.SoundDetectionRequest{Src: soundWav(1)})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("boom"))
		Expect(freed).To(BeTrue())
	})

	It("returns Canceled without feeding when ctx is already cancelled, and frees the stream", func() {
		freed := false
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		CppNumClasses = func(uintptr) int32 { return 2 }
		CppSoundStreamBegin = func(tagger uintptr, o *cSoundOpts) uintptr { return 1 }
		CppSoundStreamFeed = func(s uintptr, pcm *float32, n int32, isLast int32, out *uintptr, nOut *int32) int32 {
			Fail("sound_stream_feed must not be called when ctx is already cancelled")
			return 0
		}
		CppSoundStreamDrainScoresJSON = func(uintptr) uintptr {
			Fail("drain_scores_json must not be called when ctx is already cancelled")
			return 0
		}
		CppSoundStreamFree = func(uintptr) { freed = true }

		p := &ParakeetCpp{tagCtx: 42}
		_, err := p.SoundDetection(ctx, &pb.SoundDetectionRequest{Src: soundWav(15)})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Canceled))
		Expect(freed).To(BeTrue())
	})

	It("stops feeding and frees the stream when ctx is cancelled mid-feed", func() {
		freed := false
		feedCount := 0
		ctx, cancel := context.WithCancel(context.Background())

		CppNumClasses = func(uintptr) int32 { return 2 }
		CppSoundStreamBegin = func(tagger uintptr, o *cSoundOpts) uintptr { return 1 }
		CppSoundStreamFeed = func(s uintptr, pcm *float32, n int32, isLast int32, out *uintptr, nOut *int32) int32 {
			feedCount++
			cancel() // cancel after the first feed so a second chunk would exist if not stopped
			*out = 0
			*nOut = 0
			return 0
		}
		CppSoundStreamDrainScoresJSON = func(uintptr) uintptr {
			Fail("drain_scores_json must not be called when the feed loop was cancelled")
			return 0
		}
		CppSoundStreamFree = func(uintptr) { freed = true }

		// Two 10 s chunks, so a second feed call would happen without the cancel.
		p := &ParakeetCpp{tagCtx: 42}
		_, err := p.SoundDetection(ctx, &pb.SoundDetectionRequest{Src: soundWav(15)})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Canceled))
		Expect(feedCount).To(Equal(1))
		Expect(freed).To(BeTrue())
	})

	It("wraps a decode failure as InvalidArgument", func() {
		// Every required symbol must be non-nil to clear SoundDetection's own
		// Unimplemented gate and reach the decode step this spec targets;
		// none of them may actually be called.
		fail := func(string) { Fail("no C call once the decode itself has failed") }
		CppNumClasses = func(uintptr) int32 { fail("num_classes"); return 0 }
		CppSoundStreamBegin = func(tagger uintptr, o *cSoundOpts) uintptr { fail("begin"); return 0 }
		CppSoundStreamFeed = func(s uintptr, pcm *float32, n int32, isLast int32, out *uintptr, nOut *int32) int32 {
			fail("feed")
			return 0
		}
		CppSoundStreamDrainScoresJSON = func(uintptr) uintptr { fail("drain"); return 0 }
		CppSoundStreamFree = func(uintptr) { fail("free") }

		p := &ParakeetCpp{tagCtx: 42}
		_, err := p.SoundDetection(context.Background(), &pb.SoundDetectionRequest{
			Src: filepath.Join(GinkgoT().TempDir(), "missing.wav"),
		})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
	})

	It("returns ModelNotLoaded without a C call when tagCtx is zeroed between the entry check and the call", func() {
		called := false
		CppNumClasses = func(uintptr) int32 { called = true; return 2 }

		p := &ParakeetCpp{tagCtx: 42}
		// Simulate a Free() racing between SoundDetection's own tagCtx==0
		// check and soundStreamDrain's lock, exactly as it zeroes tagCtx
		// under engineMu.
		p.tagCtx = 0
		_, _, err := p.soundStreamDrain(context.Background(), make([]float32, 10))
		Expect(grpcerrors.IsModelNotLoaded(err)).To(BeTrue())
		Expect(called).To(BeFalse(), "no C call once tagCtx was cleared")
	})
})

var _ = Describe("averageWindowScores", func() {
	It("returns nil for no windows", func() {
		Expect(averageWindowScores(nil, 2)).To(BeNil())
	})

	It("treats a class absent from a window as 0 in that window's contribution", func() {
		windows := []soundWindowJSON{
			{Tags: []soundTagJSON{{Index: 0, Label: "Speech", Score: 1.0}}},
			{Tags: []soundTagJSON{}}, // Speech absent this window
		}
		out := averageWindowScores(windows, 2)
		Expect(out).To(HaveLen(1))
		Expect(out[0].Index).To(Equal(0))
		Expect(out[0].Score).To(BeNumerically("~", 0.5, 1e-6))
	})

	It("ignores an out-of-range class index", func() {
		windows := []soundWindowJSON{
			{Tags: []soundTagJSON{{Index: 5, Label: "Bogus", Score: 1.0}}},
		}
		Expect(averageWindowScores(windows, 2)).To(BeEmpty())
	})
})

var _ = Describe("filterSoundDetections", func() {
	It("keeps everything when top_k is 0 and threshold is 0", func() {
		in := []classAvg{{Index: 0, Score: 0.1}, {Index: 1, Score: 0.9}}
		Expect(filterSoundDetections(in, 0, 0)).To(HaveLen(2))
	})

	It("drops entries below threshold before applying top_k", func() {
		in := []classAvg{
			{Index: 0, Score: 0.9},
			{Index: 1, Score: 0.4},
			{Index: 2, Score: 0.1},
		}
		out := filterSoundDetections(in, 0.3, 5)
		Expect(out).To(HaveLen(2))
	})
})
