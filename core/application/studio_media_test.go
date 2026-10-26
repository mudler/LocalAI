// SPDX-License-Identifier: MIT
//go:build auth

package application

import (
	"testing"

	"github.com/mudler/LocalAI/core/config"
)

func modelWith(name string, flags config.ModelConfigUsecase) config.ModelConfig {
	f := flags
	return config.ModelConfig{Name: name, KnownUsecases: &f}
}

func TestTheHelperModelIsTheSettingOrTheFirstInstalledModelWithTheCapability(t *testing.T) {
	all := []config.ModelConfig{
		modelWith("zeta-vl", config.FLAG_CHAT|config.FLAG_VISION),
		modelWith("alpha-vl", config.FLAG_CHAT|config.FLAG_VISION),
		modelWith("whisper-base", config.FLAG_TRANSCRIPT),
		modelWith("plain-chat", config.FLAG_CHAT),
	}
	cases := []struct {
		name, setting string
		flag          config.ModelConfigUsecase
		want          string
	}{
		{"the first by name when nothing is set", "", config.FLAG_VISION, "alpha-vl"},
		{"the setting when that model exists", "zeta-vl", config.FLAG_VISION, "zeta-vl"},
		{"the fallback when the set model is gone", "removed", config.FLAG_VISION, "alpha-vl"},
		{"transcription", "", config.FLAG_TRANSCRIPT, "whisper-base"},
		{"none when no model can", "", config.FLAG_SOUND_GENERATION, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := chooseHelperModel(tc.setting, all, tc.flag); got != tc.want {
				t.Fatalf("chooseHelperModel(%q) = %q, want %q", tc.setting, got, tc.want)
			}
		})
	}
}
