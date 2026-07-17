package backend

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("pipelineStages", func() {
	seed := func(dir string, names ...string) *config.ModelConfigLoader {
		for _, n := range names {
			yaml := "name: " + n + "\nbackend: fake-backend\n"
			Expect(os.WriteFile(filepath.Join(dir, n+".yaml"), []byte(yaml), 0o644)).To(Succeed())
		}
		cl := config.NewModelConfigLoader(dir)
		Expect(cl.LoadModelConfigsFromPath(dir)).To(Succeed())
		return cl
	}

	It("resolves only the populated stages, in load order", func() {
		dir := GinkgoT().TempDir()
		cl := seed(dir, "vad-m", "stt-m", "llm-m", "tts-m")

		stages, err := pipelineStages(cl, &config.Pipeline{
			VAD:           "vad-m",
			Transcription: "stt-m",
			LLM:           "llm-m",
			TTS:           "tts-m",
		}, dir)
		Expect(err).ToNot(HaveOccurred())

		roles := make([]string, len(stages))
		names := make([]string, len(stages))
		for i, s := range stages {
			roles[i] = s.Role
			names[i] = s.Cfg.Name
		}
		Expect(roles).To(Equal([]string{"vad", "transcription", "llm", "tts"}))
		Expect(names).To(Equal([]string{"vad-m", "stt-m", "llm-m", "tts-m"}))
	})

	It("skips unset stages and includes sound_detection and voice_recognition when set", func() {
		dir := GinkgoT().TempDir()
		cl := seed(dir, "stt-m", "ced", "spk")

		stages, err := pipelineStages(cl, &config.Pipeline{
			Transcription:    "stt-m",
			SoundDetection:   "ced",
			VoiceRecognition: &config.PipelineVoiceRecognition{Model: "spk"},
		}, dir)
		Expect(err).ToNot(HaveOccurred())

		roles := make([]string, len(stages))
		for i, s := range stages {
			roles[i] = s.Role
		}
		Expect(roles).To(ConsistOf("transcription", "sound_detection", "voice_recognition"))
	})

	It("returns nil for a pipeline with no stages (not a pipeline)", func() {
		dir := GinkgoT().TempDir()
		cl := seed(dir)

		stages, err := pipelineStages(cl, &config.Pipeline{}, dir)
		Expect(err).ToNot(HaveOccurred())
		Expect(stages).To(BeNil())
	})
})

var _ = Describe("PreloadStages", func() {
	var (
		mu   sync.Mutex
		seen []string
	)

	// stubLoader swaps the loadStage seam for a recorder so no real backends
	// are spawned; errFor injects per-model failures.
	stubLoader := func(errFor map[string]error) {
		loadStage = func(_ context.Context, _ *model.ModelLoader, cfg config.ModelConfig, _ *config.ApplicationConfig) error {
			mu.Lock()
			seen = append(seen, cfg.Name)
			mu.Unlock()
			return errFor[cfg.Name]
		}
	}

	BeforeEach(func() {
		seen = nil
	})
	AfterEach(func() {
		loadStage = PreloadModel
	})

	mkStage := func(role, name string) PreloadStage {
		return PreloadStage{Role: role, Cfg: &config.ModelConfig{Name: name}}
	}

	It("loads every present stage, skips absent (nil-config) ones, and returns the loaded names", func() {
		stubLoader(nil)

		loaded, err := PreloadStages(context.Background(), nil, nil, []PreloadStage{
			mkStage("vad", "vad-m"),
			{Role: "transcription"}, // absent stage
			mkStage("llm", "llm-m"),
		})

		Expect(err).ToNot(HaveOccurred())
		Expect(loaded).To(ConsistOf("vad-m", "llm-m"))
		// Barrier: every stage has run by the time PreloadStages returns, so
		// reading seen without the lock here is safe.
		Expect(seen).To(ConsistOf("vad-m", "llm-m"))
	})

	It("reports a joined error naming each failed stage while still loading the rest", func() {
		stubLoader(map[string]error{
			"vad-m": errors.New("vad boom"),
			"tts-m": errors.New("tts boom"),
		})

		loaded, err := PreloadStages(context.Background(), nil, nil, []PreloadStage{
			mkStage("vad", "vad-m"),
			mkStage("llm", "llm-m"),
			mkStage("tts", "tts-m"),
		})

		// Every stage ran (a failure does not cancel the others)...
		Expect(seen).To(ConsistOf("vad-m", "llm-m", "tts-m"))
		// ...the stage that loaded fine is reported as loaded...
		Expect(loaded).To(ConsistOf("llm-m"))
		// ...and the joined error names every broken stage and its cause.
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("vad (vad-m)"))
		Expect(err.Error()).To(ContainSubstring("vad boom"))
		Expect(err.Error()).To(ContainSubstring("tts (tts-m)"))
		Expect(err.Error()).To(ContainSubstring("tts boom"))
		Expect(err.Error()).ToNot(ContainSubstring("llm"))
	})
})
