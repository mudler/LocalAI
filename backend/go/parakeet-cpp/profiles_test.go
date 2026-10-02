// SPDX-License-Identifier: MIT
package main

import (
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ = Describe("profile capability", func() {
	It("rejects opt-in without support instead of silently returning plain output", func() {
		restore := diarizeStubs()
		defer restore()
		CppDiarizePCM = func(uintptr, *float32, int32, int32) uintptr { return 0 }
		p := &ParakeetCpp{diarCtx: 1}
		_, err := p.Diarize(&pb.DiarizeRequest{IncludeSpeakerProfiles: true})
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})
})
var _ = Describe("profile export transport", func() {
	It("exports with no registry and copies trusted metadata without freeing borrowed identity", func() {
		restore := diarizeStubs()
		defer restore()
		oldExport, oldIdentity := CppDiarizeProfilesPCMJSON, CppSpeakerIdentity
		defer func() { CppDiarizeProfilesPCMJSON, CppSpeakerIdentity = oldExport, oldIdentity }()
		pool := &diarizeCstrPool{}
		identity := pool.cstr("sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		CppSpeakerIdentity = func(ctx uintptr) uintptr { Expect(ctx).To(Equal(uintptr(2))); return identity }
		CppSpeakerDim = func(uintptr) int32 { return 2 }
		freed := 0
		CppFreeString = func(ptr uintptr) { Expect(ptr).NotTo(Equal(identity)); freed++ }
		CppDiarizePCM = func(uintptr, *float32, int32, int32) uintptr { Fail("legacy function called"); return 0 }
		CppDiarizeProfilesPCMJSON = func(d, s, r uintptr, pcm *float32, n, hz int32, a, m float32) uintptr {
			Expect(r).To(BeZero())
			Expect(s).To(Equal(uintptr(2)))
			return pool.cstr(`{"segments":[{"speaker":0,"start":0,"end":3}],"speaker_profiles":{"version":1,"encoder":{"identity":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","dimension":2},"speakers":[]}}`)
		}
		p := &ParakeetCpp{diarCtx: 1, spkCtx: 2}
		out, err := p.Diarize(&pb.DiarizeRequest{Dst: diarizeWav(3), IncludeSpeakerProfiles: true})
		Expect(err).NotTo(HaveOccurred())
		Expect(out.SpeakerProfilesJson).To(ContainSubstring(`"version":1`))
		Expect(freed).To(Equal(1))
		meta, err := p.Status()
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.SpeakerEncoder.Dimension).To(Equal(int32(2)))
		Expect(meta.SpeakerEncoder.Identity).To(HavePrefix("sha256:"))
		Expect(freed).To(Equal(1))
		p.spkCtx = 0
		meta, err = p.Status()
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.SpeakerEncoder).To(BeNil())
	})
	It("maps both offline and realtime matches without changing speaker slots", func() {
		voices := []*pb.KnownVoice{{Id: "one", Name: "Ada"}, {Id: "two", Name: "Ada"}}
		names := map[string]speakerNameJSON{"0": {Name: "one", Score: .9}, "1": {Name: "two", Score: .8}}
		translateNames(names, voiceNames(voices))
		live := liveSpeakersToProto([]sceneSpeakerJSON{{Speaker: 0}, {Speaker: 1}}, names)
		Expect(live[0].Name).To(Equal("Ada"))
		Expect(live[1].Name).To(Equal("Ada"))
		Expect(names["0"].Score).To(Equal(float32(.9)))
		Expect(names["1"].Score).To(Equal(float32(.8)))
	})
})

var _ = Describe("combined transcript profile export", func() {
	var p *ParakeetCpp
	var pool *diarizeCstrPool
	var restore func()
	var profileCalls, asrCalls int
	var profileRaw string
	BeforeEach(func() {
		restore = diarizeStubs()
		oldExport, oldIdentity, oldBatch := CppDiarizeProfilesPCMJSON, CppSpeakerIdentity, CppTranscribePcmBatchJSON
		DeferCleanup(func() {
			restore()
			CppDiarizeProfilesPCMJSON, CppSpeakerIdentity, CppTranscribePcmBatchJSON = oldExport, oldIdentity, oldBatch
		})
		pool = &diarizeCstrPool{}
		p = &ParakeetCpp{diarCtx: 1, spkCtx: 2, ctxPtr: 3}
		profileCalls, asrCalls = 0, 0
		profileRaw = `{"segments":[{"speaker":7,"start":0,"end":1},{"speaker":2,"start":1,"end":3}],"names":{"7":{"name":"one","score":0.9},"2":{"name":"two","score":0.8}},"speaker_profiles":{"version":1,"encoder":{"identity":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","dimension":2},"speakers":[{"speaker":2,"embedding":[0,1]},{"speaker":7,"embedding":[1,0]}]}}`
		CppSpeakerIdentity = func(uintptr) uintptr { return pool.cstr("identity") }
		CppSpeakerDim = func(uintptr) int32 { return 2 }
		CppFreeString = func(uintptr) {}
		CppLastError = func(uintptr) string { return "inference failed" }
		CppDiarizePCM = func(uintptr, *float32, int32, int32) uintptr { Fail("second diarization"); return 0 }
		CppTranscribeAndDiarizeJSON = func(uintptr, uintptr, *float32, int32, int32) uintptr { Fail("second diarization"); return 0 }
		CppDiarizeProfilesPCMJSON = func(d, s, r uintptr, pcm *float32, n, hz int32, a, m float32) uintptr {
			profileCalls++
			return pool.cstr(profileRaw)
		}
		CppTranscribePcmBatchJSON = func(ctx uintptr, pcm []float32, sizes []int32, clips, hz, decoder int32) uintptr {
			asrCalls++
			Expect(ctx).To(Equal(uintptr(3)))
			Expect(clips).To(Equal(int32(1)))
			Expect(p.engineMu.TryLock()).To(BeFalse())
			return pool.cstr(`[{"text":"Hello there","words":[{"w":"Hello","start":0.1,"end":0.9},{"w":"there","start":1.2,"end":2}]}]`)
		}
	})
	request := func() *pb.DiarizeRequest {
		return &pb.DiarizeRequest{Dst: diarizeWav(3), IncludeText: true, IncludeSpeakerProfiles: true}
	}
	It("exports text and profiles with an empty registry using raw sparse slots", func() {
		out, err := p.Diarize(request())
		Expect(err).NotTo(HaveOccurred())
		Expect(profileCalls).To(Equal(1))
		Expect(asrCalls).To(Equal(1))
		Expect(out.Segments).To(HaveLen(2))
		Expect(out.Segments[0].Speaker).To(Equal("7"))
		Expect(out.Segments[0].Text).To(Equal("Hello"))
		Expect(out.Segments[1].Speaker).To(Equal("2"))
		Expect(out.Segments[1].Text).To(Equal("there"))
		Expect(out.SpeakerProfilesJson).To(ContainSubstring(`"speaker":2,"embedding":[0,1]`))
		Expect(out.SpeakerProfilesJson).To(ContainSubstring(`"speaker":7,"embedding":[1,0]`))
	})
	It("keeps duplicate display names independent through replay and attribution", func() {
		CppSpeakerRegistryNew = func() uintptr { return 4 }
		CppSpeakerRegistryFree = func(uintptr) {}
		ids := []string{}
		CppSpeakerRegistryAddEmbedding = func(r uintptr, id string, v *float32, n int32) int32 {
			ids = append(ids, id)
			if id == "one" {
				Expect(*v).To(Equal(float32(1)))
			} else {
				Expect(*v).To(BeZero())
			}
			return 0
		}
		req := request()
		req.KnownVoices = []*pb.KnownVoice{{Id: "one", Name: "Ada", Embedding: []float32{1, 0}}, {Id: "two", Name: "Ada", Embedding: []float32{0, 1}}}
		out, err := p.Diarize(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(ids).To(Equal([]string{"one", "two"}))
		Expect(out.Segments[0].Name).To(Equal("Ada"))
		Expect(out.Segments[1].Name).To(Equal("Ada"))
		Expect(out.Segments[0].NameScore).To(Equal(float32(.9)))
		Expect(out.Segments[1].NameScore).To(Equal(float32(.8)))
		Expect(out.Segments[0].Speaker).To(Equal("7"))
		Expect(out.Segments[1].Speaker).To(Equal("2"))
	})
	It("propagates profile failure", func() {
		CppDiarizeProfilesPCMJSON = func(uintptr, uintptr, uintptr, *float32, int32, int32, float32, float32) uintptr { return 0 }
		_, err := p.Diarize(request())
		Expect(err).To(MatchError(ContainSubstring("inference failed")))
	})
	It("propagates ASR failure", func() {
		CppTranscribePcmBatchJSON = func(uintptr, []float32, []int32, int32, int32, int32) uintptr { return 0 }
		_, err := p.Diarize(request())
		Expect(err).To(MatchError(ContainSubstring("inference failed")))
	})
	It("rejects missing ASR capability when an ASR companion is loaded", func() {
		CppTranscribePcmBatchJSON = nil
		_, err := p.Diarize(request())
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})
	It("rejects transcript text without timestamped words", func() {
		CppTranscribePcmBatchJSON = func(uintptr, []float32, []int32, int32, int32, int32) uintptr {
			return pool.cstr(`[{"text":"missing words"}]`)
		}
		_, err := p.Diarize(request())
		Expect(err).To(MatchError(ContainSubstring("timestamped words")))
	})
	It("preserves no-ASR fallback with profiles and no text", func() {
		p.ctxPtr = 0
		out, err := p.Diarize(request())
		Expect(err).NotTo(HaveOccurred())
		Expect(asrCalls).To(BeZero())
		Expect(profileCalls).To(Equal(1))
		Expect(out.Segments[0].Text).To(BeEmpty())
		Expect(out.SpeakerProfilesJson).NotTo(BeEmpty())
	})
	It("keeps the legacy combined call when profile export is off", func() {
		CppTranscribeAndDiarizeJSON = func(uintptr, uintptr, *float32, int32, int32) uintptr {
			return pool.cstr(`{"utterances":[{"speaker":5,"text":"legacy","start":0,"end":3}]}`)
		}
		req := request()
		req.IncludeSpeakerProfiles = false
		out, err := p.Diarize(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(profileCalls).To(BeZero())
		Expect(asrCalls).To(BeZero())
		Expect(out.SpeakerProfilesJson).To(BeEmpty())
		Expect(out.Segments[0].Text).To(Equal("legacy"))
		Expect(out.Segments[0].Speaker).To(Equal("5"))
	})
})
