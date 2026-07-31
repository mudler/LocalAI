package gallery_test

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("gallery inference-default persistence", func() {
	It("persists inference defaults under parameters so the loader reads them back", func() {
		modelsPath, err := os.MkdirTemp("", "inference-defaults")
		Expect(err).ToNot(HaveOccurred())
		defer os.RemoveAll(modelsPath)

		systemState, err := system.GetSystemState(system.WithModelPath(modelsPath))
		Expect(err).ToNot(HaveOccurred())

		// A qwen3.5 name makes ApplyInferenceDefaults fill in the recommended
		// sampling parameters (repeat_penalty=1, presence_penalty=1.5, min_p=0).
		// Those belong under the parameters: key — ModelConfig embeds
		// schema.PredictionOptions with `yaml:"parameters"`, so the loader only
		// reads them from that submap (#11230). The install is fully offline:
		// the definition declares no files, so InstallModel just writes the YAML.
		definition := &ModelConfig{ConfigFile: `backend: transformers
parameters:
  model: owner/repo
`}

		_, err = InstallModel(context.TODO(), systemState, "qwen3.5-managed", definition, map[string]any{}, func(string, string, string, float64) {}, false)
		Expect(err).ToNot(HaveOccurred())

		data, err := os.ReadFile(filepath.Join(modelsPath, "qwen3.5-managed.yaml"))
		Expect(err).ToNot(HaveOccurred())

		// The defaults must survive a round-trip through the typed loader.
		var reloaded config.ModelConfig
		Expect(yaml.Unmarshal(data, &reloaded)).To(Succeed())
		Expect(reloaded.PresencePenalty).To(BeNumerically("==", 1.5))
		Expect(reloaded.RepeatPenalty).To(BeNumerically("==", 1))
		Expect(reloaded.MinP).NotTo(BeNil())
		Expect(reloaded.Temperature).NotTo(BeNil())

		// They must live under parameters:, never at the top level, or they are
		// silently dropped on reload.
		var raw map[string]any
		Expect(yaml.Unmarshal(data, &raw)).To(Succeed())
		Expect(raw).NotTo(HaveKey("presence_penalty"))
		Expect(raw).NotTo(HaveKey("repeat_penalty"))
		Expect(raw).NotTo(HaveKey("min_p"))
		parameters, ok := raw["parameters"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(parameters).To(HaveKey("presence_penalty"))
		Expect(parameters).To(HaveKey("repeat_penalty"))
		Expect(parameters).To(HaveKey("min_p"))
	})
})
