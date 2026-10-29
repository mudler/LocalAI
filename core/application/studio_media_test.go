// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

func modelWith(name string, flags config.ModelConfigUsecase) config.ModelConfig {
	f := flags
	return config.ModelConfig{Name: name, KnownUsecases: &f}
}

var _ = Describe("the helper model", func() {
	all := []config.ModelConfig{
		modelWith("zeta-vl", config.FLAG_CHAT|config.FLAG_VISION),
		modelWith("alpha-vl", config.FLAG_CHAT|config.FLAG_VISION),
		modelWith("whisper-base", config.FLAG_TRANSCRIPT),
		modelWith("plain-chat", config.FLAG_CHAT),
	}

	DescribeTable("is the setting or the first installed model with the capability",
		func(setting string, flag config.ModelConfigUsecase, want string) {
			Expect(chooseHelperModel(setting, all, flag)).To(Equal(want), "chooseHelperModel(%q)", setting)
		},
		Entry("the first by name when nothing is set", "", config.FLAG_VISION, "alpha-vl"),
		Entry("the setting when that model exists", "zeta-vl", config.FLAG_VISION, "zeta-vl"),
		Entry("the fallback when the set model is gone", "removed", config.FLAG_VISION, "alpha-vl"),
		Entry("transcription", "", config.FLAG_TRANSCRIPT, "whisper-base"),
		Entry("none when no model can", "", config.FLAG_SOUND_GENERATION, ""),
	)
})
