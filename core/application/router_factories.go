package application

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/routing/corpus"
)

// adapterConfig resolves a model name to its runtime ModelConfig, or nil when
// unknown. LoadModelConfigFileByNameDefaultOptions never returns nil — for an
// unknown name it returns a defaults-filled stub with an empty Name (the YAML
// `name:` field is required by Validate), which is how we tell the two apart.
func (a *Application) adapterConfig(modelName string) *config.ModelConfig {
	cfg, err := a.backendLoader.LoadModelConfigFileByNameDefaultOptions(modelName, a.applicationConfig)
	if err != nil || cfg == nil || cfg.Name == "" {
		return nil
	}
	return cfg
}

// ModelConfigLookup is the lookup the router middleware's classifier validator
// uses to confirm classifier_model declares FLAG_SCORE before binding it.
func (a *Application) ModelConfigLookup() func(modelName string) *config.ModelConfig {
	return a.adapterConfig
}

// The router-facing factories below (Scorer, Embedder, Reranker, TokenCounter)
// bind a model NAME at construction and re-resolve the CONFIG on every call.
// Capturing the config at construction would bake in whatever state
// adapterConfig saw first — including a stub returned before the YAML reached
// bcl.configs (e.g. /import-model or gallery install racing startup). The
// classifier registry caches factories by router-config fingerprint, so a
// once-stale capture stays stale until the router config is edited.

func (a *Application) Scorer(modelName string) backend.Scorer {
	if a.adapterConfig(modelName) == nil {
		return nil
	}
	return &lazyScorer{app: a, modelName: modelName}
}

type lazyScorer struct {
	app       *Application
	modelName string
}

func (l *lazyScorer) Score(ctx context.Context, prompt string, candidates []string) ([]backend.CandidateScore, error) {
	cfg := l.app.adapterConfig(l.modelName)
	if cfg == nil {
		return nil, fmt.Errorf("scorer: model %q no longer available", l.modelName)
	}
	return backend.NewScorer(l.app.modelLoader, *cfg, l.app.applicationConfig).Score(ctx, prompt, candidates)
}

// TokenCounter returns a func so the middleware's literal field type accepts
// it as a method value without importing core/http/middleware from here.
func (a *Application) TokenCounter(modelName string) func(string) (int, error) {
	if a.adapterConfig(modelName) == nil {
		return nil
	}
	return func(text string) (int, error) {
		cfg := a.adapterConfig(modelName)
		if cfg == nil {
			return 0, fmt.Errorf("token counter: model %q no longer available", modelName)
		}
		resp, err := backend.ModelTokenize(text, a.modelLoader, *cfg, a.applicationConfig)
		if err != nil {
			return 0, err
		}
		return len(resp.Tokens), nil
	}
}

func (a *Application) Reranker(modelName string) backend.Reranker {
	if a.adapterConfig(modelName) == nil {
		return nil
	}
	return &lazyReranker{app: a, modelName: modelName}
}

type lazyReranker struct {
	app       *Application
	modelName string
}

func (l *lazyReranker) Rerank(ctx context.Context, query string, documents []string) ([]backend.RerankResult, error) {
	cfg := l.app.adapterConfig(l.modelName)
	if cfg == nil {
		return nil, fmt.Errorf("reranker: model %q no longer available", l.modelName)
	}
	return backend.NewReranker(l.app.modelLoader, *cfg, l.app.applicationConfig).Rerank(ctx, query, documents)
}

func (a *Application) Embedder(modelName string) backend.Embedder {
	if a.adapterConfig(modelName) == nil {
		return nil
	}
	return &lazyEmbedder{app: a, modelName: modelName}
}

type lazyEmbedder struct {
	app       *Application
	modelName string
}

func (l *lazyEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	cfg := l.app.adapterConfig(l.modelName)
	if cfg == nil {
		return nil, fmt.Errorf("embedder: model %q no longer available", l.modelName)
	}
	return backend.NewEmbedder(l.app.modelLoader, *cfg, l.app.applicationConfig).Embed(ctx, text)
}

// VectorStore takes a store name, not a model name — no adapterConfig, no
// staleness to avoid.
func (a *Application) VectorStore(storeName string) backend.VectorStore {
	return backend.NewVectorStore(a.modelLoader, a.applicationConfig, storeName)
}

// RouterCorpus returns the process-wide KNN corpus manager, built in
// newApplication.
func (a *Application) RouterCorpus() *corpus.Manager {
	return a.routerCorpus
}
