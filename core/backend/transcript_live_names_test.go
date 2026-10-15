package backend

import (
	"github.com/mudler/LocalAI/core/services/voicerecognition"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("live speaker names", func() {
	It("puts the known voices in the session config", func() {
		o := liveOptions{}
		WithKnownVoices([]voicerecognition.KnownVoice{{Name: "Ada", Embedding: []float32{1, 0}, Model: "m.gguf"}})(&o)
		cfg := liveConfigProto("en", o)
		Expect(cfg.Language).To(Equal("en"))
		Expect(cfg.SampleRate).To(Equal(int32(liveSampleRate)))
		Expect(cfg.KnownVoices).To(HaveLen(1))
		Expect(cfg.KnownVoices[0].Name).To(Equal("Ada"))
		Expect(cfg.KnownVoices[0].Model).To(Equal("m.gguf"))
	})
	It("sends no known voices by default", func() {
		cfg := liveConfigProto("en", liveOptions{})
		Expect(cfg.KnownVoices).To(BeEmpty())
		Expect(cfg.Language).To(Equal("en"))
		Expect(cfg.SampleRate).To(Equal(int32(liveSampleRate)))
	})
	It("carries the speaker name from the backend event", func() {
		ev := liveEventFromProto(&proto.TranscriptLiveResponse{
			Speakers: []*proto.LiveSpeakerSegment{{Speaker: "0", Name: "Ada", Start: 1e9, End: 2e9}, {Speaker: "1", Start: 2e9, End: 3e9}},
		})
		Expect(ev.Speakers).To(HaveLen(2))
		Expect(ev.Speakers[0].Name).To(Equal("Ada"))
		Expect(ev.Speakers[0].Speaker).To(Equal("0"))
		Expect(ev.Speakers[1].Name).To(BeEmpty())
	})
})
