package application

import (
	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
)

// adapterConfig resolves a model name to its runtime ModelConfig, or
// nil when the name is unknown. Shared by the router-facing factories
// below and by ModelConfigLookup.
func (a *Application) adapterConfig(modelName string) *config.ModelConfig {
	cfg, err := a.backendLoader.LoadModelConfigFileByNameDefaultOptions(modelName, a.applicationConfig)
	if err != nil || cfg == nil {
		return nil
	}
	return cfg
}

// ModelConfigLookup is the lookup function the router middleware's
// classifier validator uses to confirm classifier_model declares
// FLAG_SCORE before binding it.
func (a *Application) ModelConfigLookup() func(modelName string) *config.ModelConfig {
	return a.adapterConfig
}

// Scorer returns a backend.Scorer bound to the named model, or nil
// when the model is unknown. Used as a method value (app.Scorer) by
// router.ClassifierDeps — no factory-of-factory wrapper needed.
func (a *Application) Scorer(modelName string) backend.Scorer {
	cfg := a.adapterConfig(modelName)
	if cfg == nil {
		return nil
	}
	return backend.NewScorer(a.modelLoader, *cfg, a.applicationConfig)
}

// Reranker returns a backend.Reranker bound to the named model, or
// nil when unknown. The reranker model's `type:` (e.g. "colbert")
// selects the scoring head inside the rerankers backend.
func (a *Application) Reranker(modelName string) backend.Reranker {
	cfg := a.adapterConfig(modelName)
	if cfg == nil {
		return nil
	}
	return backend.NewReranker(a.modelLoader, *cfg, a.applicationConfig)
}

// Embedder returns a backend.Embedder bound to the named model, or
// nil when unknown. Used by the router's L2 embedding cache.
func (a *Application) Embedder(modelName string) backend.Embedder {
	cfg := a.adapterConfig(modelName)
	if cfg == nil {
		return nil
	}
	return backend.NewEmbedder(a.modelLoader, *cfg, a.applicationConfig)
}

// VectorStore returns a backend.VectorStore for the named collection,
// or nil when the name is empty. Each router model gets its own
// backend process via the model loader's cache keyed by storeName.
func (a *Application) VectorStore(storeName string) backend.VectorStore {
	return backend.NewVectorStore(a.modelLoader, a.applicationConfig, storeName)
}
