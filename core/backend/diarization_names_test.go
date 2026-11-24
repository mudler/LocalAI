package backend

import (
	"encoding/json"

	"github.com/mudler/LocalAI/core/services/voicerecognition"
	"github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("diarization names", func() {
	It("adds name and name_score next to the normalized speaker, and keeps SPEAKER_NN", func() {
		res := diarizationResultFromProto(&proto.DiarizeResponse{
			Duration: 10,
			Segments: []*proto.DiarizeSegment{
				{Speaker: "0", Start: 0, End: 4, Name: "Ada", NameScore: 0.93},
				{Speaker: "1", Start: 4, End: 8},
				{Speaker: "0", Start: 8, End: 10, Name: "Ada", NameScore: 0.93},
			},
		})
		Expect(res.Segments[0].Speaker).To(Equal("SPEAKER_00"))
		Expect(res.Segments[0].Name).To(Equal("Ada"))
		Expect(res.Segments[0].NameScore).To(BeNumerically("~", 0.93, 1e-6))
		Expect(res.Segments[1].Speaker).To(Equal("SPEAKER_01"))
		Expect(res.Segments[1].Name).To(BeEmpty())
		Expect(res.Speakers[0].Name).To(Equal("Ada"))
		Expect(res.Speakers[1].Name).To(BeEmpty())
	})

	It("marshals a result with no names byte for byte as before", func() {
		res := diarizationResultFromProto(&proto.DiarizeResponse{
			Duration: 10,
			Segments: []*proto.DiarizeSegment{{Speaker: "0", Start: 0, End: 1}},
		})
		b, err := json.Marshal(res)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(b)).To(Equal(`{"task":"diarize","duration":10,"num_speakers":1,` +
			`"segments":[{"id":0,"speaker":"SPEAKER_00","label":"0","start":0,"end":1}],` +
			`"speakers":[{"id":"SPEAKER_00","label":"0","total_speech_duration":1,"segment_count":1}]}`))
	})

	It("sends the known voices to the backend", func() {
		req := DiarizationRequest{Audio: "a.wav", KnownVoices: []voicerecognition.KnownVoice{
			{Name: "Ada", Embedding: []float32{1, 0}, Model: "m.gguf"},
		}}
		p := req.toProto(0, "x")
		Expect(p.KnownVoices).To(HaveLen(1))
		Expect(p.KnownVoices[0].Name).To(Equal("Ada"))
		Expect(p.KnownVoices[0].Embedding).To(Equal([]float32{1, 0}))
		Expect(p.KnownVoices[0].Model).To(Equal("m.gguf"))
		Expect((&DiarizationRequest{Audio: "a.wav"}).toProto(0, "x").KnownVoices).To(BeEmpty())
	})
})
