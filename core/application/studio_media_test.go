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

	DescribeTable("describe_image uses the setting, then the chat's own model, then any vision model, then a cloud model as a guess",
		func(setting, chatModel string, models []config.ModelConfig, want string, wantGuess bool) {
			got, guess := chooseVisionModel(setting, chatModel, models)
			Expect(got).To(Equal(want), "chooseVisionModel(%q, %q)", setting, chatModel)
			Expect(guess).To(Equal(wantGuess), "guess for chooseVisionModel(%q, %q)", setting, chatModel)
		},
		Entry("the setting wins over a chat model that can see", "zeta-vl", "alpha-vl", all, "zeta-vl", false),
		Entry("the chat's own model when nothing is set", "", "zeta-vl", all, "zeta-vl", false),
		Entry("the chat's own model when the set model is gone", "removed", "zeta-vl", all, "zeta-vl", false),
		Entry("any vision model when the chat's model cannot see", "", "plain-chat", all, "alpha-vl", false),
		Entry("a cloud model that declares vision", "", "cloud",
			[]config.ModelConfig{proxyModel("cloud", config.FLAG_CHAT|config.FLAG_VISION)}, "cloud", false),
		Entry("the chat's cloud model as a guess when it does not declare vision", "", "cloud",
			[]config.ModelConfig{proxyModel("cloud", config.FLAG_CHAT)}, "cloud", true),
		Entry("not a cloud model in translate mode, which drops images", "", "cloud",
			[]config.ModelConfig{translateModel("cloud", config.FLAG_CHAT|config.FLAG_VISION)}, "", false),
		Entry("none for an unknown chat model and no vision model", "", "gone",
			[]config.ModelConfig{modelWith("plain-chat", config.FLAG_CHAT)}, "", false),
		Entry("an installed vision model before the guess", "", "cloud",
			[]config.ModelConfig{proxyModel("cloud", config.FLAG_CHAT), modelWith("alpha-vl", config.FLAG_CHAT|config.FLAG_VISION)}, "alpha-vl", false),
		Entry("the setting before the guess", "alpha-vl", "cloud",
			[]config.ModelConfig{proxyModel("cloud", config.FLAG_CHAT), modelWith("alpha-vl", config.FLAG_CHAT|config.FLAG_VISION)}, "alpha-vl", false),
		Entry("none for a local model that cannot see", "", "plain-chat",
			[]config.ModelConfig{modelWith("plain-chat", config.FLAG_CHAT)}, "", false),
		Entry("none when the chat has no model", "", "",
			[]config.ModelConfig{proxyModel("cloud", config.FLAG_CHAT)}, "", false),
	)
})

func translateModel(name string, flags config.ModelConfigUsecase) config.ModelConfig {
	m := proxyModel(name, flags)
	m.Proxy.Mode = config.ProxyModeTranslate
	return m
}

func proxyModel(name string, flags config.ModelConfigUsecase) config.ModelConfig {
	m := modelWith(name, flags)
	m.Backend = "cloud-proxy"
	m.Proxy.UpstreamURL = "https://upstream.example/v1/chat/completions"
	return m
}
