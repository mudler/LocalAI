package openai

import (
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

// Every LFM3-Audio gallery entry must be accepted by the realtime endpoint with
// the pipeline shape it advertises: any-to-any entries open one speech-to-speech
// session, the pipeline entries chain the model through its own slots. The
// overrides are read from the generated gallery and loaded as a model config the
// way an install writes them.
var _ = Describe("LFM3-Audio gallery entries in the realtime endpoint", func() {
	type entry struct {
		Name      string         `yaml:"name"`
		Overrides map[string]any `yaml:"overrides"`
	}
	load := func(name string) *config.ModelConfig {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "gallery", "index.yaml"))
		Expect(err).NotTo(HaveOccurred())
		var entries []entry
		Expect(yaml.Unmarshal(raw, &entries)).To(Succeed())
		for _, e := range entries {
			if e.Name != name {
				continue
			}
			body := map[string]any{"name": name}
			for k, v := range e.Overrides {
				body[k] = v
			}
			out, err := yaml.Marshal(body)
			Expect(err).NotTo(HaveOccurred())
			cfg := &config.ModelConfig{}
			Expect(yaml.Unmarshal(out, cfg)).To(Succeed())
			return cfg
		}
		Fail("gallery entry not found: " + name)
		return nil
	}

	DescribeTable("accepts the entry",
		func(name string, anyToAny, vad bool) {
			cfg := load(name)
			code, msg, ok := prepareRealtimeConfig(cfg)
			Expect(ok).To(BeTrue(), code+": "+msg)
			Expect(cfg.Pipeline.IsAnyToAny()).To(Equal(anyToAny))
			if vad {
				Expect(cfg.Pipeline.VAD).To(Equal("silero-vad-ggml"))
				Expect(defaultSessionTurnDetection(cfg)).NotTo(BeNil())
			} else {
				Expect(cfg.Pipeline.VAD).To(BeEmpty(), "no VAD slot is filled in")
				Expect(defaultSessionTurnDetection(cfg)).To(BeNil())
			}
			Expect(cfg.Pipeline.LLM).To(Equal(name))
			Expect(cfg.Pipeline.Transcription).To(Equal(name))
			if anyToAny {
				Expect(cfg.Pipeline.TTS).To(BeEmpty(), "speech comes from the same stream")
			} else {
				Expect(cfg.Pipeline.TTS).To(Equal(name))
				Expect(cfg.KnownUsecases).NotTo(BeNil())
				Expect(*cfg.KnownUsecases & config.FLAG_REALTIME_AUDIO).To(BeZero())
			}
		},
		Entry("any-to-any Q4", "lfm3-audio-2b", true, false),
		Entry("any-to-any Q8", "lfm3-audio-2b-q8", true, false),
		Entry("any-to-any Q4 with VAD", "lfm3-audio-2b-vad", true, true),
		Entry("any-to-any Q8 with VAD", "lfm3-audio-2b-q8-vad", true, true),
		Entry("pipeline Q4", "lfm3-audio-2b-pipeline", false, false),
		Entry("pipeline Q8", "lfm3-audio-2b-q8-pipeline", false, false),
		Entry("pipeline Q4 with VAD", "lfm3-audio-2b-pipeline-vad", false, true),
		Entry("pipeline Q8 with VAD", "lfm3-audio-2b-q8-pipeline-vad", false, true),
	)
})
