package backend

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/xlog"
)

// PreloadModelByName loads the named model into memory so the first request
// that uses it pays no cold-start load cost — the inverse of shutting a model
// down. If the model is a realtime pipeline (its config declares a `pipeline:`
// block), each configured sub-model (VAD, transcription, LLM, TTS,
// sound_detection, voice_recognition) is loaded concurrently instead of the
// pipeline stub, which has no backend of its own. It returns the model names
// actually loaded and a joined error naming each sub-model that failed (nil on
// full success); a partial pipeline load reports both the loaded names and the
// failures so the caller can surface exactly what is and isn't resident.
// Compaction's summary_model is deliberately left cold: it is only invoked off
// the response path, so it can stay lazy.
func PreloadModelByName(ctx context.Context, cl *config.ModelConfigLoader, ml *model.ModelLoader, appConfig *config.ApplicationConfig, name string) ([]string, error) {
	cfg, err := cl.LoadModelConfigFileByNameDefaultOptions(name, appConfig)
	if err != nil {
		return nil, err
	}

	stages, err := pipelineStages(cl, &cfg.Pipeline, ml.ModelPath)
	if err != nil {
		return nil, err
	}
	if len(stages) == 0 {
		// Not a pipeline: load the model's own backend directly.
		if err := PreloadModel(ctx, ml, *cfg, appConfig); err != nil {
			return nil, err
		}
		return []string{cfg.Name}, nil
	}
	return PreloadStages(ctx, ml, appConfig, stages)
}

// PreloadStage names one pipeline sub-model to preload and the resolved config
// to load it from (nil = stage absent, skipped). Role labels the pipeline slot
// in errors and logs.
type PreloadStage struct {
	Role string
	Cfg  *config.ModelConfig
}

// loadStage is PreloadModel behind a seam so PreloadStages can be unit-tested
// without spawning real backends.
var loadStage = PreloadModel

// pipelineStages resolves each populated pipeline stage to its concrete model
// config, following a single alias hop — the same resolution the realtime
// pipeline itself uses. A stage that fails to resolve is a misconfiguration,
// so it fails fast rather than being deferred to load. A pipeline with no
// stages set returns nil, which callers treat as "not a pipeline".
func pipelineStages(cl *config.ModelConfigLoader, p *config.Pipeline, modelPath string) ([]PreloadStage, error) {
	voiceRec := ""
	if p.VoiceRecognition != nil {
		voiceRec = p.VoiceRecognition.Model
	}
	var stages []PreloadStage
	for _, s := range []struct{ role, name string }{
		{"vad", p.VAD},
		{"transcription", p.Transcription},
		{"llm", p.LLM},
		{"tts", p.TTS},
		{"sound_detection", p.SoundDetection},
		{"voice_recognition", voiceRec},
	} {
		if s.name == "" {
			continue
		}
		cfg, err := cl.LoadResolvedModelConfig(s.name, modelPath)
		if err != nil {
			return nil, fmt.Errorf("%s (%s): %w", s.role, s.name, err)
		}
		stages = append(stages, PreloadStage{Role: s.role, Cfg: cfg})
	}
	return stages, nil
}

// PreloadStages loads every present stage at once and waits for all of them, so
// a pipeline warms in the time of its slowest stage rather than the sum. Absent
// (nil-config) stages are skipped. A failed stage does not cancel the others —
// they all run to completion so the joined error names every broken stage at
// once, alongside the names that did load.
func PreloadStages(ctx context.Context, ml *model.ModelLoader, appConfig *config.ApplicationConfig, stages []PreloadStage) ([]string, error) {
	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		loaded []string
		errs   []error
	)
	for _, s := range stages {
		if s.Cfg == nil {
			continue
		}
		wg.Add(1)
		go func(s PreloadStage) {
			defer wg.Done()
			if err := loadStage(ctx, ml, *s.Cfg, appConfig); err != nil {
				xlog.Warn("preload: failed to load pipeline sub-model", "stage", s.Role, "model", s.Cfg.Name, "error", err)
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s (%s): %w", s.Role, s.Cfg.Name, err))
				mu.Unlock()
				return
			}
			xlog.Debug("preload: loaded pipeline sub-model", "stage", s.Role, "model", s.Cfg.Name)
			mu.Lock()
			loaded = append(loaded, s.Cfg.Name)
			mu.Unlock()
		}(s)
	}
	wg.Wait()
	return loaded, errors.Join(errs...)
}
