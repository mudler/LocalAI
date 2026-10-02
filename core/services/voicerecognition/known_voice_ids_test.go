// SPDX-License-Identifier: MIT
package voicerecognition

import (
	ginkgo "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("known voice registration IDs", func() {
	ginkgo.It("retains independent registrations sharing a display name", func() {
		selected := SelectKnownVoices([]Entry{
			{Metadata: Metadata{ID: "a", Name: "Ada", Model: "speaker.gguf"}, Embedding: []float32{1, 0}},
			{Metadata: Metadata{ID: "b", Name: "Ada", Model: "speaker.gguf"}, Embedding: []float32{0, 1}},
		}, "speaker.gguf")
		Expect(selected.Voices).To(HaveLen(2))
		Expect(selected.Voices[0].ID).To(Equal("a"))
		Expect(selected.Voices[1].ID).To(Equal("b"))
		Expect(selected.Voices[0].Embedding).To(Equal([]float32{1, 0}))
		Expect(selected.Voices[1].Embedding).To(Equal([]float32{0, 1}))
	})
})
