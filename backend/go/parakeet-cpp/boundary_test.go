package main

import (
	"math/rand/v2"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("utteranceBoundary (decode end-of-utterance latch)", func() {
	It("starts open: a fresh decode is not on a boundary", func() {
		var b utteranceBoundary
		Expect(b).To(Equal(boundaryOpen))
		Expect(b.ended()).To(BeFalse())
	})

	DescribeTable("single feed transition from the open state",
		func(r streamFeedResult, wantEnded bool) {
			Expect(boundaryOpen.observe(r).ended()).To(Equal(wantEnded))
		},
		Entry("<EOU> closes it", streamFeedResult{Eou: true}, true),
		Entry("<EOU> with text closes it (eou wins)", streamFeedResult{Delta: "hi", Eou: true}, true),
		Entry("<EOB> stays open (backchannel is not a turn boundary)", streamFeedResult{Eob: true}, false),
		Entry("plain text stays open", streamFeedResult{Delta: "hello"}, false),
		Entry("words-only stays open", streamFeedResult{Words: []transcriptWord{{W: "x"}}}, false),
		Entry("empty feed is a no-op (stays open)", streamFeedResult{}, false),
	)

	DescribeTable("single feed transition from the closed state",
		func(r streamFeedResult, wantEnded bool) {
			Expect(boundaryClosed.observe(r).ended()).To(Equal(wantEnded))
		},
		Entry("another <EOU> stays closed", streamFeedResult{Eou: true}, true),
		Entry("trailing speech reopens it", streamFeedResult{Delta: "and more"}, false),
		Entry("words reopen it", streamFeedResult{Words: []transcriptWord{{W: "x"}}}, false),
		Entry("a backchannel <EOB> reopens it", streamFeedResult{Eob: true}, false),
		Entry("empty feed is a no-op (stays closed)", streamFeedResult{}, true),
	)

	It("is a latch: <EOU> then trailing speech reopens, then <EOU> closes again", func() {
		b := boundaryOpen
		b = b.observe(streamFeedResult{Delta: "turn one", Eou: true})
		Expect(b.ended()).To(BeTrue())
		b = b.observe(streamFeedResult{Delta: " and more"})
		Expect(b.ended()).To(BeFalse(), "trailing speech without an EOU is an open utterance")
		b = b.observe(streamFeedResult{Eou: true})
		Expect(b.ended()).To(BeTrue())
	})

	It("treats a backchannel before a real EOU correctly", func() {
		b := boundaryOpen
		b = b.observe(streamFeedResult{Delta: "uh huh", Eob: true})
		Expect(b.ended()).To(BeFalse(), "a backchannel must not masquerade as a turn boundary")
		b = b.observe(streamFeedResult{Delta: "done", Eou: true})
		Expect(b.ended()).To(BeTrue())
	})

	It("matches the reference fold over seeded random feed sequences", func() {
		// The invariant: after any sequence of feeds, ended() is true iff the
		// last feed that carried ANY event was an <EOU>. <EOU> takes priority
		// when a feed carries both an EOU and speech; empty feeds are ignored.
		for seed := uint64(1); seed <= 200; seed++ {
			rng := rand.New(rand.NewPCG(seed, seed*2654435761))
			b := boundaryOpen
			lastWasEou := false // reference: did the last meaningful feed end on EOU?
			steps := rng.IntN(30)
			for i := 0; i < steps; i++ {
				var r streamFeedResult
				switch rng.IntN(5) {
				case 0:
					r = streamFeedResult{Eou: true}
				case 1:
					r = streamFeedResult{Eob: true}
				case 2:
					r = streamFeedResult{Delta: "w"}
				case 3:
					r = streamFeedResult{Delta: "w", Eou: true} // eou + speech, eou wins
				case 4:
					r = streamFeedResult{} // empty: no-op
				}
				b = b.observe(r)
				if r.Eou {
					lastWasEou = true
				} else if r.Eob || r.Delta != "" || len(r.Words) > 0 {
					lastWasEou = false
				}
			}
			Expect(b.ended()).To(Equal(lastWasEou),
				"seed %d: latch disagreed with the reference fold", seed)
		}
	})
})
