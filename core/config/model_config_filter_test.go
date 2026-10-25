// SPDX-License-Identifier: MIT
package config_test

import (
	. "github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("FilterMayMatchLooseFiles", func() {
	It("is false for every usecase filter, which rejects files without a config", func() {
		for _, flag := range []ModelConfigUsecase{
			FLAG_CHAT, FLAG_COMPLETION, FLAG_EDIT, FLAG_EMBEDDINGS, FLAG_RERANK, FLAG_IMAGE,
			FLAG_TRANSCRIPT, FLAG_TTS, FLAG_SOUND_GENERATION, FLAG_TOKENIZE, FLAG_VAD, FLAG_VIDEO,
			FLAG_DETECTION, FLAG_VISION, FLAG_FACE_RECOGNITION, FLAG_SPEAKER_RECOGNITION,
			FLAG_AUDIO_TRANSFORM, FLAG_DIARIZATION, FLAG_DEPTH, FLAG_FORECAST, FLAG_3D, FLAG_UPSCALE, FLAG_LLM,
		} {
			Expect(FilterMayMatchLooseFiles(BuildUsecaseFilterFn(flag))).To(BeFalse(), "flag %d", flag)
		}
	})
	It("is true for nil, the match-all filter and name filters", func() {
		Expect(FilterMayMatchLooseFiles(nil)).To(BeTrue())
		Expect(FilterMayMatchLooseFiles(NoFilterFn)).To(BeTrue())
		Expect(FilterMayMatchLooseFiles(BuildUsecaseFilterFn(FLAG_ANY))).To(BeTrue())
		name, err := BuildNameFilterFn("model")
		Expect(err).NotTo(HaveOccurred())
		Expect(FilterMayMatchLooseFiles(name)).To(BeTrue())
	})
})
