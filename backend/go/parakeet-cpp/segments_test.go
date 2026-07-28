package main

import (
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func tw(text string, start, end float64) transcriptWord {
	return transcriptWord{W: text, Start: start, End: end}
}

var _ = Describe("splitWordsIntoSegments (NeMo get_segment_offsets parity)", func() {
	seps := []rune{'.', '?', '!'}

	It("splits on sentence-ending punctuation, including the delimiter word", func() {
		words := []transcriptWord{tw("hello", 0, 0.4), tw("world.", 0.4, 0.8), tw("bye", 1.0, 1.3)}
		segs := splitWordsIntoSegments(words, seps, 0)
		Expect(segs).To(HaveLen(2))
		Expect(segs[0]).To(HaveLen(2))
		Expect(segs[0][1].W).To(Equal("world."))
		Expect(segs[1]).To(HaveLen(1))
		Expect(segs[1][0].W).To(Equal("bye"))
	})

	It("keeps a single segment with no terminal punctuation and gap off", func() {
		words := []transcriptWord{tw("a", 0, 0.2), tw("b", 0.2, 0.4), tw("c", 5.0, 5.2)}
		segs := splitWordsIntoSegments(words, seps, 0)
		Expect(segs).To(HaveLen(1))
	})

	It("splits on the gap rule when enabled, the gapped word starting the next segment", func() {
		words := []transcriptWord{tw("a", 0, 0.2), tw("b", 0.2, 0.4), tw("c", 5.0, 5.2)}
		segs := splitWordsIntoSegments(words, seps, 1.0) // c is 4.6s after b
		Expect(segs).To(HaveLen(2))
		Expect(segs[0]).To(HaveLen(2)) // a b
		Expect(segs[1]).To(HaveLen(1)) // c
		Expect(segs[1][0].W).To(Equal("c"))
	})

	It("checks the gap rule before punctuation (NeMo elif order)", func() {
		// "b." would terminate, but c is far after it -> gap closes [a b.] at b.
		words := []transcriptWord{tw("a", 0, 0.2), tw("b.", 0.2, 0.4), tw("c", 9.0, 9.2)}
		segs := splitWordsIntoSegments(words, seps, 1.0)
		Expect(segs).To(HaveLen(2))
		Expect(segs[0]).To(HaveLen(2))
		Expect(segs[1][0].W).To(Equal("c"))
	})

	It("still splits on punctuation when the gap rule is enabled but does not fire", func() {
		words := []transcriptWord{tw("hi.", 0, 0.4), tw("bye", 0.4, 0.8)}
		segs := splitWordsIntoSegments(words, seps, 5.0) // gap never reached
		Expect(segs).To(HaveLen(2))
		Expect(segs[0][0].W).To(Equal("hi."))
	})

	It("returns nothing for empty input", func() {
		Expect(splitWordsIntoSegments(nil, seps, 0)).To(BeEmpty())
	})
})

var _ = Describe("transcriptResultFromDoc (multi-segment)", func() {
	doc := transcriptJSON{
		Text:     "hello world. bye now",
		FrameSec: 0.08,
		Words: []transcriptWord{
			{W: "hello", Start: 0.0, End: 0.4},
			{W: "world.", Start: 0.4, End: 0.8},
			{W: "bye", Start: 1.0, End: 1.3},
			{W: "now", Start: 1.3, End: 1.6},
		},
		Tokens: []transcriptToken{{ID: 1, T: 0.1}, {ID: 2, T: 0.5}, {ID: 3, T: 1.1}, {ID: 4, T: 1.4}},
	}

	It("emits one segment per punctuation-delimited group with start/end", func() {
		res := transcriptResultFromDoc(doc, &pb.TranscriptRequest{}, 0)
		Expect(res.Segments).To(HaveLen(2))
		Expect(res.Segments[0].Text).To(Equal("hello world."))
		Expect(res.Segments[0].Start).To(Equal(int64(0)))
		Expect(res.Segments[0].End).To(Equal(secondsToNanos(0.8)))
		Expect(res.Segments[1].Text).To(Equal("bye now"))
		Expect(res.Segments[1].Start).To(Equal(secondsToNanos(1.0)))
		Expect(res.Segments[1].Id).To(Equal(int32(1)))
	})

	It("assigns tokens to the segment whose time window contains them", func() {
		res := transcriptResultFromDoc(doc, &pb.TranscriptRequest{}, 0)
		Expect(res.Segments[0].Tokens).To(Equal([]int32{1, 2}))
		Expect(res.Segments[1].Tokens).To(Equal([]int32{3, 4}))
	})

	It("attaches per-segment words only when word granularity requested", func() {
		plain := transcriptResultFromDoc(doc, &pb.TranscriptRequest{}, 0)
		Expect(plain.Segments[0].Words).To(BeEmpty())
		withWords := transcriptResultFromDoc(doc, &pb.TranscriptRequest{TimestampGranularities: []string{"word"}}, 0)
		Expect(withWords.Segments[0].Words).To(HaveLen(2))
	})

	It("falls back to a single text segment when there are no words", func() {
		res := transcriptResultFromDoc(transcriptJSON{Text: "hi"}, &pb.TranscriptRequest{}, 0)
		Expect(res.Segments).To(HaveLen(1))
		Expect(res.Segments[0].Text).To(Equal("hi"))
	})
})

var _ = Describe("streaming segment assembly", func() {
	It("closes a segment with start/end from its words on EOU", func() {
		acc := &streamSegmenter{}
		acc.add(streamFeedResult{Delta: "hello world", Eou: true, Words: []transcriptWord{
			{W: "hello", Start: 0.0, End: 0.4}, {W: "world", Start: 0.4, End: 0.9},
		}})
		segs := acc.segments()
		Expect(segs).To(HaveLen(1))
		Expect(segs[0].Text).To(Equal("hello world"))
		Expect(segs[0].Start).To(Equal(int64(0)))
		Expect(segs[0].End).To(Equal(secondsToNanos(0.9)))
	})

	It("buffers words across feeds until EOU", func() {
		acc := &streamSegmenter{}
		acc.add(streamFeedResult{Delta: "hi", Words: []transcriptWord{{W: "hi", Start: 0, End: 0.3}}})
		Expect(acc.segments()).To(BeEmpty())
		acc.add(streamFeedResult{Delta: "there", Eou: true, Words: []transcriptWord{{W: "there", Start: 0.3, End: 0.7}}})
		Expect(acc.segments()).To(HaveLen(1))
		Expect(acc.segments()[0].Text).To(Equal("hi there"))
	})

	// ABI v5 split <EOB> (backchannel) out of the "eou" flag into its own "eob"
	// field; a backchannel must still close the segment as it did in v4.
	It("closes a segment on EOB (backchannel) too", func() {
		acc := &streamSegmenter{}
		acc.add(streamFeedResult{Delta: "uh huh", Eob: true, Words: []transcriptWord{
			{W: "uh", Start: 0.0, End: 0.2}, {W: "huh", Start: 0.2, End: 0.5},
		}})
		segs := acc.segments()
		Expect(segs).To(HaveLen(1))
		Expect(segs[0].Text).To(Equal("uh huh"))
		Expect(segs[0].End).To(Equal(secondsToNanos(0.5)))
	})

	// Older text-only libparakeet.so: no per-word timings, so a segment is cut
	// from the delta text on each <EOU>/<EOB> (no timestamps), one per utterance.
	It("falls back to text segments when the feed carries no words", func() {
		acc := &streamSegmenter{}
		acc.add(streamFeedResult{Delta: "first turn", Eou: true})
		acc.add(streamFeedResult{Delta: "second turn", Eou: true})
		segs := acc.segments()
		Expect(segs).To(HaveLen(2))
		Expect(segs[0].Text).To(Equal("first turn"))
		Expect(segs[1].Text).To(Equal("second turn"))
		Expect(segs[0].Start).To(Equal(int64(0)), "no per-word timing on the text path")
		Expect(segs[0].End).To(Equal(int64(0)))
	})
})
