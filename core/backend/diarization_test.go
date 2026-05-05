package backend

import (
	"github.com/mudler/LocalAI/pkg/grpc/proto"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("diarizationResultFromProto", func() {
	It("normalises raw backend speaker labels to SPEAKER_NN in first-seen order", func() {
		in := &proto.DiarizeResponse{
			Duration: 10.5,
			Language: "en",
			Segments: []*proto.DiarizeSegment{
				{Start: 0.0, End: 1.0, Speaker: "5", Text: "hi"},
				{Start: 1.0, End: 2.0, Speaker: "2"},
				{Start: 2.0, End: 3.5, Speaker: "5"},
				{Start: 3.5, End: 4.0, Speaker: ""}, // empty → coerced to "0"
			},
		}

		got := diarizationResultFromProto(in)

		Expect(got.Task).To(Equal("diarize"))
		Expect(got.NumSpeakers).To(Equal(3), "expected 3 distinct speakers (5, 2, 0)")
		Expect(got.Duration).To(BeEquivalentTo(10.5))
		Expect(got.Language).To(Equal("en"))
		Expect(got.Segments).To(HaveLen(4))

		// First-seen-order normalisation: "5"→SPEAKER_00, "2"→SPEAKER_01, ""→SPEAKER_02
		want := []struct {
			speaker string
			label   string
		}{
			{"SPEAKER_00", "5"},
			{"SPEAKER_01", "2"},
			{"SPEAKER_00", "5"},
			{"SPEAKER_02", "0"},
		}
		for i, w := range want {
			Expect(got.Segments[i].Speaker).To(Equal(w.speaker), "seg[%d].speaker", i)
			Expect(got.Segments[i].Label).To(Equal(w.label), "seg[%d].label", i)
		}

		// Per-speaker totals reflect cumulative speech duration and segment count.
		Expect(got.Speakers).To(HaveLen(3))
		byID := map[string]float64{}
		countByID := map[string]int{}
		for _, sp := range got.Speakers {
			byID[sp.Id] = sp.TotalSpeechDuration
			countByID[sp.Id] = sp.SegmentCount
		}
		Expect(byID["SPEAKER_00"]).To(BeNumerically("~", 2.5, 0.001), "1.0 + 1.5")
		Expect(byID["SPEAKER_01"]).To(BeNumerically("~", 1.0, 0.001))
		Expect(countByID["SPEAKER_00"]).To(Equal(2))
		Expect(countByID["SPEAKER_01"]).To(Equal(1))
		Expect(countByID["SPEAKER_02"]).To(Equal(1))
	})

	It("returns a non-nil result with a non-nil segments slice for nil input", func() {
		got := diarizationResultFromProto(nil)
		Expect(got).ToNot(BeNil())
		Expect(got.Segments).ToNot(BeNil())
		Expect(got.Segments).To(BeEmpty())
	})

	It("keeps the backend speaker count when no segments are returned", func() {
		// Backend reports a non-zero NumSpeakers but no segments (early stop,
		// silence-only audio after VAD trim). Surface the backend's count.
		in := &proto.DiarizeResponse{NumSpeakers: 2, Duration: 5}
		got := diarizationResultFromProto(in)
		Expect(got.NumSpeakers).To(Equal(2))
		Expect(got.Segments).To(BeEmpty())
	})
})
