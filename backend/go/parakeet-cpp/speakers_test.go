package main

import (
	"context"
	"os"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func sseg(spk int, start, end float64) diarizeSegmentJSON {
	return diarizeSegmentJSON{Speaker: spk, Start: start, End: end}
}

// speakerTurnsOf collapses consecutive repeats: [0 0 1 0] -> [0 1 0].
func speakerTurnsOf(labels []string) []string {
	var out []string
	for _, l := range labels {
		if len(out) == 0 || out[len(out)-1] != l {
			out = append(out, l)
		}
	}
	return out
}

var _ = Describe("transcript speaker labels", func() {
	Context("assignSpeakers", func() {
		It("picks the speaker with the largest overlap", func() {
			words := []transcriptWord{tw("a", 0.5, 0.8)}
			Expect(assignSpeakers(words, []diarizeSegmentJSON{sseg(0, 0.0, 0.6), sseg(1, 0.55, 2.0)})).
				To(Equal([]int{1}))
		})

		It("snaps a word just outside a segment to it, but not a far one", func() {
			words := []transcriptWord{tw("well", 19.92, 20.00), tw("far", 25.0, 25.2)}
			segs := []diarizeSegmentJSON{sseg(0, 14.78, 18.75), sseg(1, 20.10, 23.60)}
			Expect(assignSpeakers(words, segs)).To(Equal([]int{1, -1}))
		})
	})

	It("splits segments at speaker turns and labels segments and words", func() {
		doc := transcriptJSON{
			Text:  "hi there. hello",
			Words: []transcriptWord{tw("hi", 0.0, 0.3), tw("there.", 0.3, 0.6), tw("hello", 1.0, 1.4)},
		}
		// Punctuation gives [hi there.] [hello]; the turn after "hi" splits the first.
		opts := &pb.TranscriptRequest{TimestampGranularities: []string{"word"}}
		res := transcriptResultWithSpeakers(doc, opts, 0, []int{0, 1, 1})
		Expect(res.Segments).To(HaveLen(3))
		Expect([]string{res.Segments[0].Speaker, res.Segments[1].Speaker, res.Segments[2].Speaker}).
			To(Equal([]string{"0", "1", "1"}))
		Expect(res.Segments[1].Text).To(Equal("there."))
		Expect(res.Segments[2].Words[0].Speaker).To(Equal("1"))
		for i, seg := range res.Segments {
			Expect(seg.Id).To(Equal(int32(i)))
		}
	})

	It("leaves speakers empty without diarization and for unknown words", func() {
		doc := transcriptJSON{Text: "hi.", Words: []transcriptWord{tw("hi.", 0, 0.3)}}
		Expect(transcriptResultFromDoc(doc, &pb.TranscriptRequest{}, 0).Segments[0].Speaker).To(BeEmpty())
		Expect(transcriptResultWithSpeakers(doc, &pb.TranscriptRequest{}, 0, []int{-1}).Segments[0].Speaker).
			To(BeEmpty())
	})

	It("picks the speaker covering most of an utterance", func() {
		words := []transcriptWord{tw("a", 0, 0.2), tw("b", 0.2, 1.5), tw("c", 1.5, 1.6)}
		Expect(majoritySpeaker(words, []int{0, 1, 0})).To(Equal(1))
		Expect(majoritySpeaker(words, []int{-1, -1, -1})).To(Equal(-1))
	})

	It("only diarizes with a companion, a capable library and diarize=true", func() {
		saved := CppDiarizePCM
		defer func() { CppDiarizePCM = saved }()
		CppDiarizePCM = func(uintptr, *float32, int32, int32) uintptr { return 0 }
		p := &ParakeetCpp{ctxPtr: 1, diarCtx: 2}
		Expect(p.wantSpeakers(true)).To(BeTrue())
		Expect(p.wantSpeakers(false)).To(BeFalse())
		Expect((&ParakeetCpp{ctxPtr: 1}).wantSpeakers(true)).To(BeFalse())
		CppDiarizePCM = nil
		Expect(p.wantSpeakers(true)).To(BeFalse())
	})
})

var _ = Describe("ParakeetCpp transcript speakers (real models)", func() {
	It("labels unary and stream=true transcripts with a diarization_model companion", func() {
		diarModel := os.Getenv("PARAKEET_BACKEND_TEST_DIAR_MODEL")
		asrModel := os.Getenv("PARAKEET_BACKEND_TEST_MODEL")
		wavPath := os.Getenv("PARAKEET_BACKEND_TEST_DIAR_WAV")
		if diarModel == "" || asrModel == "" || wavPath == "" {
			Skip("set PARAKEET_BACKEND_TEST_DIAR_MODEL, PARAKEET_BACKEND_TEST_MODEL and " +
				"PARAKEET_BACKEND_TEST_DIAR_WAV (a multi-speaker 16 kHz WAV)")
		}
		ensureLibLoaded()
		if CppDiarizePCM == nil || CppModelKind == nil {
			Skip("libparakeet.so has no diarization / model-kind C-API")
		}
		p := &ParakeetCpp{}
		Expect(p.Load(&pb.ModelOptions{
			ModelFile: asrModel,
			Options:   []string{"diarization_model:" + diarModel},
		})).To(Succeed())
		defer func() { _ = p.Free() }()

		res, err := p.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: wavPath, Diarize: true})
		Expect(err).ToNot(HaveOccurred())
		labels := make([]string, len(res.Segments))
		for i, s := range res.Segments {
			Expect(s.Speaker).ToNot(BeEmpty(), "segment %d %q", i, s.Text)
			labels[i] = s.Speaker
		}
		// parakeet.cpp's tests/fixtures/two_speakers.wav alternates A-B-A-B.
		Expect(speakerTurnsOf(labels)).To(Equal([]string{"0", "1", "0", "1"}))

		plain, err := p.AudioTranscription(context.Background(), &pb.TranscriptRequest{Dst: wavPath})
		Expect(err).ToNot(HaveOccurred())
		for _, s := range plain.Segments {
			Expect(s.Speaker).To(BeEmpty())
		}
		Expect(plain.Text).To(Equal(res.Text))
	})
})
