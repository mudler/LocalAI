package main

import (
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These specs drive Diarize with include_sounds against stubbed scene stream
// entry points, the same seam live_test.go uses, so they run without
// libparakeet.so or model files.

var _ = Describe("ParakeetCpp.Diarize include_sounds", func() {
	var (
		restoreDiar func()
		restore     func()
		pool        *diarizeCstrPool
	)

	BeforeEach(func() {
		restoreDiar = diarizeStubs()
		pool = &diarizeCstrPool{}
		sOpts, sBegin, sFeed, sFree := CppSceneOptsDefault, CppSceneStreamBegin, CppSceneStreamFeedJSON, CppSceneStreamFree
		restore = func() {
			CppSceneOptsDefault, CppSceneStreamBegin, CppSceneStreamFeedJSON, CppSceneStreamFree = sOpts, sBegin, sFeed, sFree
		}
		CppSceneOptsDefault = func(o *cSceneOpts) { *o = cSceneOpts{Sound: cSoundOpts{TopK: 5}} }
		CppFreeString = func(uintptr) {}
		CppDiarizePCM = func(uintptr, *float32, int32, int32) uintptr {
			return pool.cstr(`{"speakers":8,"segments":[{"speaker":0,"start":0.00,"end":3.00}]}`)
		}
	})
	AfterEach(func() {
		restore()
		restoreDiar()
	})

	It("rejects the request with Unimplemented and a stable code when no sound model is loaded", func() {
		p := &ParakeetCpp{diarCtx: 42}
		_, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(1), IncludeSounds: true})
		Expect(err).To(HaveOccurred())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
		Expect(err.Error()).To(ContainSubstring(grpcerrors.SoundEventsUnsupportedCode))
		Expect(err.Error()).To(ContainSubstring("sound_model"))
	})

	It("does not touch the sound path when include_sounds is off", func() {
		CppSceneStreamBegin = func(uintptr, uintptr, uintptr, *cSceneOpts) uintptr {
			Fail("no scene stream may start without include_sounds")
			return 0
		}
		p := &ParakeetCpp{diarCtx: 42, tagCtx: 43}
		resp, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(1)})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.SoundsIncluded).To(BeFalse())
		Expect(resp.Sounds).To(BeEmpty())
	})

	It("returns closed events from a tagger-only scene stream, sorted, with the peak as confidence", func() {
		var gotDiar, gotTag uintptr
		var gotOpts cSceneOpts
		CppSceneStreamBegin = func(asr, diar, tag uintptr, o *cSceneOpts) uintptr {
			gotDiar, gotTag, gotOpts = diar, tag, *o
			return 77
		}
		freed := uintptr(0)
		CppSceneStreamFree = func(s uintptr) { freed = s }
		feeds := 0
		var lastFlags []bool
		CppSceneStreamFeedJSON = func(s uintptr, _ *float32, n int32, isLast int32) uintptr {
			feeds++
			lastFlags = append(lastFlags, isLast == 1)
			if isLast == 1 {
				return pool.cstr(`{"speakers":[],"sounds":[{"index":99,"label":"Dog","start":12.5,"end":14.0,"peak":0.7}]}`)
			}
			return pool.cstr(`{"speakers":[],"sounds":[{"index":3,"label":"Cough","start":2.0,"end":2.5,"peak":0.91}]}`)
		}

		p := &ParakeetCpp{diarCtx: 42, tagCtx: 43}
		// 15 s of audio is two 10 s feeds, the second flushing open events.
		resp, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(15), IncludeSounds: true})
		Expect(err).ToNot(HaveOccurred())
		Expect(gotDiar).To(BeZero(), "the sound stream must not borrow the diarization model")
		Expect(gotTag).To(Equal(uintptr(43)))
		Expect(gotOpts.Sound.TopK).To(Equal(int32(0)))
		Expect(feeds).To(Equal(2))
		Expect(lastFlags).To(Equal([]bool{false, true}))
		Expect(freed).To(Equal(uintptr(77)))

		Expect(resp.SoundsIncluded).To(BeTrue())
		Expect(resp.Sounds).To(HaveLen(2))
		Expect(resp.Sounds[0].Label).To(Equal("Cough"))
		Expect(resp.Sounds[0].Start).To(BeNumerically("~", 2.0, 0.001))
		Expect(resp.Sounds[0].End).To(BeNumerically("~", 2.5, 0.001))
		Expect(resp.Sounds[0].Confidence).To(BeNumerically("~", 0.91, 0.001))
		Expect(resp.Sounds[1].Label).To(Equal("Dog"))
		Expect(resp.Segments).To(HaveLen(1), "speaker segments are unaffected")
	})

	It("reports an empty list as included when nothing was heard", func() {
		CppSceneStreamBegin = func(uintptr, uintptr, uintptr, *cSceneOpts) uintptr { return 1 }
		CppSceneStreamFree = func(uintptr) {}
		CppSceneStreamFeedJSON = func(uintptr, *float32, int32, int32) uintptr {
			return pool.cstr(`{"speakers":[],"sounds":[]}`)
		}
		p := &ParakeetCpp{diarCtx: 42, tagCtx: 43}
		resp, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(2), IncludeSounds: true})
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.SoundsIncluded).To(BeTrue())
		Expect(resp.Sounds).To(BeEmpty())
	})

	It("frees the stream and returns the error when a feed fails", func() {
		CppSceneStreamBegin = func(uintptr, uintptr, uintptr, *cSceneOpts) uintptr { return 5 }
		freed := false
		CppSceneStreamFree = func(uintptr) { freed = true }
		CppSceneStreamFeedJSON = func(uintptr, *float32, int32, int32) uintptr { return 0 }
		p := &ParakeetCpp{diarCtx: 42, tagCtx: 43}
		_, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(2), IncludeSounds: true})
		Expect(err).To(HaveOccurred())
		Expect(freed).To(BeTrue())
	})
})

var _ = Describe("diarizeSoundsToProto", func() {
	It("sorts by start then end then label and never returns nil", func() {
		Expect(diarizeSoundsToProto(nil)).ToNot(BeNil())
		out := diarizeSoundsToProto([]sceneSoundJSON{
			{Label: "b", Start: 5, End: 6, Peak: 0.5},
			{Label: "a", Start: 1, End: 4, Peak: 0.6},
			{Label: "a", Start: 1, End: 2, Peak: 0.7},
		})
		Expect(out).To(HaveLen(3))
		Expect(out[0].End).To(BeNumerically("~", 2, 0.001))
		Expect(out[1].End).To(BeNumerically("~", 4, 0.001))
		Expect(out[2].Label).To(Equal("b"))
	})
})
