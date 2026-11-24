package openai

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
)

// Warmup delegates to backend.PreloadStages (its concurrency, nil-skipping and
// error-joining semantics are pinned in core/backend). These specs pin the
// wiring instead: each realtime model type must warm exactly its configured
// stages under the right pipeline-role labels. No backends are installed, so
// every attempted stage fails to load — the joined error is the proof of which
// stages were attempted and how they were labeled.
var _ = Describe("realtime model Warmup wiring", func() {
	newLoader := func() (*model.ModelLoader, *config.ApplicationConfig) {
		systemState, err := system.GetSystemState(system.WithModelPath(GinkgoT().TempDir()))
		Expect(err).ToNot(HaveOccurred())
		appConfig := config.NewApplicationConfig(config.WithSystemState(systemState))
		return model.NewModelLoader(systemState), appConfig
	}

	It("wrappedModel warms every configured stage under its pipeline role", func() {
		ml, appConfig := newLoader()
		m := &wrappedModel{
			VADConfig:            &config.ModelConfig{Name: "vad-m"},
			TranscriptionConfig:  &config.ModelConfig{Name: "stt-m"},
			LLMConfig:            &config.ModelConfig{Name: "llm-m"},
			TTSConfig:            &config.ModelConfig{Name: "tts-m"},
			SoundDetectionConfig: &config.ModelConfig{Name: "ced-m"},
			modelLoader:          ml,
			appConfig:            appConfig,
		}

		err := m.Warmup(context.Background())
		Expect(err).To(HaveOccurred())
		for _, stage := range []string{"vad (vad-m)", "transcription (stt-m)", "llm (llm-m)", "tts (tts-m)", "sound_detection (ced-m)"} {
			Expect(err.Error()).To(ContainSubstring(stage))
		}
	})

	It("transcriptOnlyModel warms its stages and skips absent ones", func() {
		ml, appConfig := newLoader()
		m := &transcriptOnlyModel{
			VADConfig:           &config.ModelConfig{Name: "vad-m"},
			TranscriptionConfig: &config.ModelConfig{Name: "stt-m"},
			// SoundDetectionConfig nil: an absent stage must be skipped, not
			// fail the warm-up.
			modelLoader: ml,
			appConfig:   appConfig,
		}

		err := m.Warmup(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("vad (vad-m)"))
		Expect(err.Error()).To(ContainSubstring("transcription (stt-m)"))
		Expect(err.Error()).ToNot(ContainSubstring("sound_detection"))
	})
})
