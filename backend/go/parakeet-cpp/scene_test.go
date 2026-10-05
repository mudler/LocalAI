package main

import (
	"unsafe"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The sceneBegin spec drives it entirely against stubbed
// CppSceneOptsDefault/CppSceneStreamBegin (the same seam live_test.go uses
// for the full live-session scene specs), so it runs without libparakeet.so.

var _ = Describe("ParakeetCpp.sceneBegin", func() {
	It("forces Sound.TopK to 0 and carries p.diarLatency into the begin opts", func() {
		savedOptsDefault := CppSceneOptsDefault
		savedBegin := CppSceneStreamBegin
		defer func() {
			CppSceneOptsDefault = savedOptsDefault
			CppSceneStreamBegin = savedBegin
		}()

		// parakeet_capi_scene_opts_default's real default is top_k = 5 (a
		// sound-window score history); simulate that here so the test proves
		// sceneBegin overrides it rather than merely never setting it.
		CppSceneOptsDefault = func(o *cSceneOpts) {
			*o = cSceneOpts{Sound: cSoundOpts{TopK: 5}}
		}
		var gotOpts cSceneOpts
		CppSceneStreamBegin = func(asr, diar, tagger uintptr, o *cSceneOpts) uintptr {
			gotOpts = *o
			return 1
		}

		p := &ParakeetCpp{diarCtx: 42, diarLatency: diarLatencyVeryLow}
		h := p.sceneBegin(nil)
		Expect(h.s).ToNot(BeZero())
		Expect(gotOpts.Sound.TopK).To(Equal(int32(0)),
			"the live scene path never drains sound scores (see sound.go's SoundDetection, "+
				"which does); a nonzero top_k leaves the C side's per-window score queue "+
				"growing for the session's lifetime")
		Expect(gotOpts.DiarLatency).To(Equal(diarLatencyVeryLow))
	})
})

var _ = Describe("scene stream with speaker names", func() {
	var (
		restore   func()
		pool      *liveCstrPool
		freedRegs []uintptr
	)
	BeforeEach(func() {
		sOpts, sBegin, sBeginSpk, sFeed, sFree := CppSceneOptsDefault, CppSceneStreamBegin, CppSceneStreamBeginSpeaker, CppSceneStreamFeedJSON, CppSceneStreamFree
		sNew, sRegFree, sAdd, sDim, sStr := CppSpeakerRegistryNew, CppSpeakerRegistryFree, CppSpeakerRegistryAddEmbedding, CppSpeakerDim, CppFreeString
		restore = func() {
			CppSceneOptsDefault, CppSceneStreamBegin, CppSceneStreamBeginSpeaker, CppSceneStreamFeedJSON, CppSceneStreamFree = sOpts, sBegin, sBeginSpk, sFeed, sFree
			CppSpeakerRegistryNew, CppSpeakerRegistryFree, CppSpeakerRegistryAddEmbedding, CppSpeakerDim, CppFreeString = sNew, sRegFree, sAdd, sDim, sStr
		}
		pool = &liveCstrPool{}
		freedRegs = nil
		CppSceneOptsDefault = func(o *cSceneOpts) { o.Size = int32(unsafe.Sizeof(*o)) }
		CppSpeakerDim = func(uintptr) int32 { return 2 }
		CppSpeakerRegistryNew = func() uintptr { return 9 }
		CppSpeakerRegistryFree = func(r uintptr) { freedRegs = append(freedRegs, r) }
		CppSpeakerRegistryAddEmbedding = func(uintptr, string, *float32, int32) int32 { return 0 }
		CppFreeString = func(uintptr) {}
	})
	AfterEach(func() { restore() })

	It("replays duplicate display names independently and translates realtime matches", func() {
		vectors := map[string]float32{}
		CppSpeakerRegistryAddEmbedding = func(_ uintptr, key string, emb *float32, _ int32) int32 { vectors[key] = *emb; return 0 }
		CppSceneStreamBeginSpeaker = func(_, _, _, _, _ uintptr, _ *cSceneOpts) uintptr { return 55 }
		CppSceneStreamFeedJSON = func(uintptr, *float32, int32, int32) uintptr {
			return pool.cstr(`{"speakers":[{"speaker":0,"start":0,"end":2},{"speaker":1,"start":2,"end":4}],"names":{"0":{"name":"a","score":0.9},"1":{"name":"b","score":0.8}}}`)
		}
		p := &ParakeetCpp{diarCtx: 1, spkCtx: 2}
		h := p.sceneBegin([]*pb.KnownVoice{{Id: "a", Name: "Ada", Embedding: []float32{1, 0}}, {Id: "b", Name: "Ada", Embedding: []float32{0, 1}}})
		doc, err := p.sceneFeed(h, nil, true)
		Expect(err).NotTo(HaveOccurred())
		Expect(vectors).To(Equal(map[string]float32{"a": 1, "b": 0}))
		out := liveSpeakersToProto(doc.Speakers, doc.Names)
		Expect(out[0].Name).To(Equal("Ada"))
		Expect(out[1].Name).To(Equal("Ada"))
		Expect(out[0].Speaker).NotTo(Equal(out[1].Speaker))
	})

	It("begins a speaker scene stream with the threshold and margin when voices are given", func() {
		var gotOpts cSceneOpts
		var gotReg, gotSpk uintptr
		CppSceneStreamBeginSpeaker = func(asr, diar, tag, spk, reg uintptr, o *cSceneOpts) uintptr {
			gotOpts, gotReg, gotSpk = *o, reg, spk
			return 55
		}
		CppSceneStreamBegin = func(asr, diar, tag uintptr, o *cSceneOpts) uintptr {
			Fail("plain begin must not be used")
			return 0
		}
		p := &ParakeetCpp{diarCtx: 1, spkCtx: 2, speakerAccept: 0.7, speakerMargin: 0.05}
		h := p.sceneBegin([]*pb.KnownVoice{{Name: "Ada", Embedding: []float32{1, 0}}})
		Expect(h.s).To(Equal(uintptr(55)))
		Expect(h.reg).To(Equal(uintptr(9)))
		Expect(gotReg).To(Equal(uintptr(9)))
		Expect(gotSpk).To(Equal(uintptr(2)))
		Expect(gotOpts.SpeakerAcceptThreshold).To(BeNumerically("~", 0.7, 1e-6))
		Expect(gotOpts.SpeakerMargin).To(BeNumerically("~", 0.05, 1e-6))
	})

	It("uses the plain scene begin without voices or without a speaker model", func() {
		plain := 0
		CppSceneStreamBegin = func(asr, diar, tag uintptr, o *cSceneOpts) uintptr { plain++; return 56 }
		CppSceneStreamBeginSpeaker = func(asr, diar, tag, spk, reg uintptr, o *cSceneOpts) uintptr {
			Fail("speaker begin must not be used")
			return 0
		}
		p := &ParakeetCpp{diarCtx: 1, spkCtx: 2}
		Expect(p.sceneBegin(nil).s).To(Equal(uintptr(56)))
		p2 := &ParakeetCpp{diarCtx: 1}
		Expect(p2.sceneBegin([]*pb.KnownVoice{{Name: "Ada", Embedding: []float32{1, 0}}}).s).To(Equal(uintptr(56)))
		Expect(plain).To(Equal(2))
	})

	It("frees the registry when the speaker begin fails, and degrades to no scene stream", func() {
		CppSceneStreamBeginSpeaker = func(asr, diar, tag, spk, reg uintptr, o *cSceneOpts) uintptr { return 0 }
		lastErr := CppLastError
		defer func() { CppLastError = lastErr }()
		CppLastError = func(uintptr) string { return "registry encoder family differs" }
		p := &ParakeetCpp{diarCtx: 1, spkCtx: 2}
		h := p.sceneBegin([]*pb.KnownVoice{{Name: "Ada", Embedding: []float32{1, 0}}})
		Expect(h.s).To(Equal(uintptr(0)))
		Expect(freedRegs).To(Equal([]uintptr{9}))
	})

	It("frees the stream before its registry", func() {
		var order []string
		CppSceneStreamFree = func(uintptr) { order = append(order, "stream") }
		CppSpeakerRegistryFree = func(uintptr) { order = append(order, "registry") }
		(&ParakeetCpp{}).sceneFree(sceneStreamHandle{s: 55, reg: 9})
		Expect(order).To(Equal([]string{"stream", "registry"}))
	})

	It("turns the names of the feed document into live speaker segment names", func() {
		segs := liveSpeakersToProto(
			[]sceneSpeakerJSON{{Speaker: 0, Start: 0, End: 1}, {Speaker: 1, Start: 1, End: 2}},
			map[string]speakerNameJSON{"0": {Name: "Ada", Score: 0.9}, "1": {Name: ""}})
		Expect(segs[0].Name).To(Equal("Ada"))
		Expect(segs[0].Speaker).To(Equal("0"))
		Expect(segs[1].Name).To(BeEmpty())
		Expect(liveSpeakersToProto([]sceneSpeakerJSON{{Speaker: 0}}, nil)[0].Name).To(BeEmpty())
	})

	It("decodes the names map of a scene feed", func() {
		CppSceneStreamFeedJSON = func(s uintptr, pcm *float32, n, last int32) uintptr {
			return pool.cstr(`{"speakers":[{"speaker":0,"start":0.0,"end":1.0}],"sounds":[],"names":{"0":{"name":"Ada","score":0.9}}}`)
		}
		p := &ParakeetCpp{diarCtx: 1, spkCtx: 2}
		doc, err := p.sceneFeed(sceneStreamHandle{s: 55, diar: 1, spk: 2}, []float32{0}, false)
		Expect(err).ToNot(HaveOccurred())
		Expect(doc.Names["0"].Name).To(Equal("Ada"))
	})

	It("refuses to feed a stream whose speaker model was freed", func() {
		p := &ParakeetCpp{diarCtx: 1}
		_, err := p.sceneFeed(sceneStreamHandle{s: 55, diar: 1, spk: 2}, []float32{0}, false)
		Expect(err).To(HaveOccurred())
	})
})
