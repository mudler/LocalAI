package openai

import (
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("resolveOutputModalities", func() {
	It("defaults to audio when neither session nor response specify", func() {
		got := resolveOutputModalities(nil, nil)
		Expect(got).To(ConsistOf(types.ModalityAudio))
	})

	It("uses session modalities when response omits them", func() {
		sess := []types.Modality{types.ModalityText}
		got := resolveOutputModalities(sess, nil)
		Expect(got).To(ConsistOf(types.ModalityText))
	})

	It("response modalities override session", func() {
		sess := []types.Modality{types.ModalityAudio}
		resp := []types.Modality{types.ModalityText}
		got := resolveOutputModalities(sess, resp)
		Expect(got).To(ConsistOf(types.ModalityText))
	})

	It("returns false from modalitiesContainAudio for text-only", func() {
		Expect(modalitiesContainAudio([]types.Modality{types.ModalityText})).To(BeFalse())
	})

	It("returns true from modalitiesContainAudio for audio (default)", func() {
		Expect(modalitiesContainAudio([]types.Modality{types.ModalityAudio})).To(BeTrue())
	})

	It("returns true when both audio and text are present", func() {
		Expect(modalitiesContainAudio([]types.Modality{types.ModalityText, types.ModalityAudio})).To(BeTrue())
	})
})
