package openai

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/endpoints/openai/types"
	"github.com/mudler/LocalAI/core/services/failover"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type rtSource map[string]config.ModelConfig

func (s rtSource) GetModelConfig(n string) (config.ModelConfig, bool) { c, ok := s[n]; return c, ok }
func (s rtSource) GetAllModelsConfigs() []config.ModelConfig {
	var out []config.ModelConfig
	for _, c := range s {
		out = append(out, c)
	}
	return out
}

var _ = Describe("realtime failover", func() {
	var fm *failover.Manager

	BeforeEach(func() {
		fm = failover.New(rtSource{
			"a":     {Name: "a", Backend: "cloud-proxy"},
			"b":     {Name: "b", Backend: "llama-cpp"},
			"chain": {Name: "chain", Failover: &config.FailoverConfig{Targets: []config.FailoverTarget{{Model: "a"}, {Model: "b"}}}},
		})
	})

	chainModel := func() *wrappedModel {
		return &wrappedModel{stageRouter: stageRouter{failover: fm, stageChains: map[string]string{config.PipelineStageTTS: "chain"},
			stageTargetConfig: func(name string) (*config.ModelConfig, error) { return &config.ModelConfig{Name: name}, nil }}}
	}

	It("routes a chain stage through the plan and retries before commit", func() {
		m := chainModel()
		var tried []string
		err := m.stageCall(context.Background(), config.PipelineStageTTS, nil, func(cfg *config.ModelConfig, _ func()) error {
			tried = append(tried, cfg.Name)
			if cfg.Name == "a" {
				return errors.New("dial tcp: refused")
			}
			return nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(tried).To(Equal([]string{"a", "b"}))
	})

	It("does not retry a chain stage once output was committed", func() {
		m := chainModel()
		var tried []string
		err := m.stageCall(context.Background(), config.PipelineStageTTS, nil, func(cfg *config.ModelConfig, commit func()) error {
			tried = append(tried, cfg.Name)
			commit()
			return errors.New("dial tcp: refused")
		})
		Expect(err).To(HaveOccurred())
		Expect(tried).To(Equal([]string{"a"}))
	})

	It("calls a plain stage once with its own config", func() {
		m := &wrappedModel{}
		base := &config.ModelConfig{Name: "plain"}
		calls := 0
		err := m.stageCall(context.Background(), config.PipelineStageTTS, base, func(cfg *config.ModelConfig, _ func()) error {
			calls++
			Expect(cfg).To(BeIdenticalTo(base))
			return nil
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(calls).To(Equal(1))
	})

	It("sends initial events, then switch events, and stops on cancel", func() {
		t := &fakeTransport{}
		failoverEvents := func() []types.ModelFailoverEvent {
			var out []types.ModelFailoverEvent
			for _, e := range t.events() {
				if fe, ok := e.(types.ModelFailoverEvent); ok {
					out = append(out, fe)
				}
			}
			return out
		}
		stop := startFailoverEvents(t, fm, map[string]string{config.PipelineStageLLM: "chain"})
		Eventually(failoverEvents).Should(ContainElement(And(
			HaveField("Reason", "initial"), HaveField("To", "a"), HaveField("Stage", "llm"))))
		fm.ReportFailure("a", errors.New("dial tcp: refused"))
		Eventually(failoverEvents).Should(ContainElement(And(
			HaveField("Reason", "trip"), HaveField("From", "a"), HaveField("To", "b"), HaveField("Chain", "chain"))))
		stop()
	})
})

var _ = Describe("realtime failover in transcription-only and sound-only sessions", func() {
	var (
		cl        *config.ModelConfigLoader
		ml        *model.ModelLoader
		appConfig *config.ApplicationConfig
		fm        *failover.Manager
	)

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		write := func(name, body string) {
			Expect(os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o600)).To(Succeed())
		}
		write("vad", "name: vad\nbackend: silero-vad\n")
		write("stt-a", "name: stt-a\nbackend: fake-stt-a\n")
		write("stt-b", "name: stt-b\nbackend: fake-stt-b\n")
		write("stt-chain", "name: stt-chain\nfailover:\n  targets:\n    - model: stt-a\n    - model: stt-b\n")
		write("sound-a", "name: sound-a\nbackend: fake-sound-a\n")
		write("sound-b", "name: sound-b\nbackend: fake-sound-b\n")
		write("sound-chain", "name: sound-chain\nfailover:\n  targets:\n    - model: sound-a\n    - model: sound-b\n")
		ss := &system.SystemState{Model: system.Model{ModelsPath: dir}}
		appConfig = config.NewApplicationConfig()
		appConfig.SystemState = ss
		cl = config.NewModelConfigLoader(dir)
		Expect(cl.LoadModelConfigsFromPath(dir)).To(Succeed())
		ml = model.NewModelLoader(ss)
		fm = failover.New(cl)
	})

	// failoverEvents collects the failover events a transport received.
	failoverEvents := func(t *fakeTransport) func() []types.ModelFailoverEvent {
		return func() []types.ModelFailoverEvent {
			var out []types.ModelFailoverEvent
			for _, e := range t.events() {
				if fe, ok := e.(types.ModelFailoverEvent); ok {
					out = append(out, fe)
				}
			}
			return out
		}
	}

	It("resolves a sound_detection chain and routes each call through it", func() {
		m, err := newSoundDetectionOnlyModel(&config.Pipeline{SoundDetection: "sound-chain"}, cl, ml, appConfig, fm)
		Expect(err).ToNot(HaveOccurred())
		tm := m.(*transcriptOnlyModel)
		Expect(tm.SoundDetectionConfig.Name).To(Equal("sound-a"))
		Expect(tm.stageChains).To(Equal(map[string]string{config.PipelineStageSoundDetection: "sound-chain"}))

		var tried []string
		tm.stageTargetConfig = func(name string) (*config.ModelConfig, error) {
			tried = append(tried, name)
			return nil, errors.New("dial tcp: refused")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err = tm.SoundDetection(ctx, "a.wav", 3, 0.1)
		Expect(err).To(HaveOccurred())
		Expect(tried).To(Equal([]string{"sound-a", "sound-b"}))

		t := &fakeTransport{}
		stop := startModelFailoverEvents(t, m)
		defer stop()
		Eventually(failoverEvents(t)).Should(ContainElement(And(
			HaveField("Stage", "sound_detection"), HaveField("Chain", "sound-chain"), HaveField("Reason", "initial"))))
	})

	It("resolves a transcription chain in a transcription-only session", func() {
		m, cfg, err := newTranscriptionOnlyModel(&config.Pipeline{VAD: "vad", Transcription: "stt-chain"}, cl, ml, appConfig, fm)
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.Name).To(Equal("stt-a"))
		tm := m.(*transcriptOnlyModel)
		Expect(tm.VADConfig.Name).To(Equal("vad"))
		Expect(tm.stageChains).To(Equal(map[string]string{config.PipelineStageTranscription: "stt-chain"}))

		var tried []string
		tm.stageTargetConfig = func(name string) (*config.ModelConfig, error) {
			tried = append(tried, name)
			return nil, errors.New("dial tcp: refused")
		}
		_, err = tm.Transcribe(context.Background(), "a.wav", "", false, false, "")
		Expect(err).To(HaveOccurred())
		Expect(tried).To(Equal([]string{"stt-a", "stt-b"}))
	})

	It("fails before touching a backend when failover is not running", func() {
		_, err := newSoundDetectionOnlyModel(&config.Pipeline{SoundDetection: "sound-chain"}, cl, ml, appConfig, nil)
		Expect(err).To(MatchError(ContainSubstring("failover is not running")))
		_, _, err = newTranscriptionOnlyModel(&config.Pipeline{VAD: "vad", Transcription: "stt-chain"}, cl, ml, appConfig, nil)
		Expect(err).To(MatchError(ContainSubstring("failover is not running")))
		Expect(ml.ListLoadedModels()).To(BeEmpty())
	})

	It("sends no failover events for a session without chains", func() {
		m, err := newSoundDetectionOnlyModel(&config.Pipeline{SoundDetection: "sound-a"}, cl, ml, appConfig, fm)
		Expect(err).ToNot(HaveOccurred())
		t := &fakeTransport{}
		startModelFailoverEvents(t, m)()
		Consistently(failoverEvents(t), 200*time.Millisecond).Should(BeEmpty())
	})
})
