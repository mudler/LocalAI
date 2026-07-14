package openai

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// dropInspectedPrefix is what stands between the VAD loop's buffer clears and
// cutting the first word off an utterance: the no-speech clear must keep the
// holdback tail (silero hasn't crossed its onset threshold yet) and both
// clears must keep audio appended while the tick ran (the VAD never saw it).
var _ = Describe("dropInspectedPrefix", func() {
	It("keeps the holdback tail of the inspected window and everything appended mid-tick", func() {
		inspected := []byte{1, 2, 3, 4, 5, 6}
		appended := []byte{7, 8}
		buf := append(append([]byte(nil), inspected...), appended...)

		out := dropInspectedPrefix(buf, len(inspected), 2)

		Expect(out).To(Equal([]byte{5, 6, 7, 8}), "older confirmed-silent head dropped, possible onset + fresh audio kept")
	})

	It("returns the buffer unchanged when the inspected window fits in the holdback", func() {
		buf := []byte{1, 2, 3}

		Expect(dropInspectedPrefix(buf, len(buf), 4)).To(Equal(buf))
		Expect(dropInspectedPrefix(buf, len(buf), len(buf))).To(Equal(buf))
	})

	It("drops the whole inspected window with zero holdback, keeping only mid-tick appends", func() {
		// The commit-time clear: the inspected audio was committed, audio
		// appended while the tick ran belongs to the next turn.
		buf := []byte{1, 2, 3, 4}

		Expect(dropInspectedPrefix(buf, 4, 0)).To(BeEmpty())
		Expect(dropInspectedPrefix(append(buf, 9), 4, 0)).To(Equal([]byte{9}))
	})

	It("clamps when told more was inspected than the buffer holds", func() {
		buf := []byte{1, 2}

		Expect(dropInspectedPrefix(buf, 10, 0)).To(BeEmpty())
	})

	It("returns a copy, not a sub-slice, when bytes are dropped", func() {
		buf := []byte{1, 2, 3, 4}

		out := dropInspectedPrefix(buf, 4, 2)

		Expect(out).To(Equal([]byte{3, 4}))
		buf[2] = 99
		Expect(out).To(Equal([]byte{3, 4}), "mutating the old backing array must not leak into the published buffer")
	})
})
