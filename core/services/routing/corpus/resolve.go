package corpus

import (
	"context"
	"errors"
	"fmt"

	"github.com/mudler/LocalAI/core/backend"
	"github.com/mudler/LocalAI/core/config"
)

// The REST corpus endpoints and the assistant MCP tools are thin
// transport adapters over the two helpers in this file, so the "what
// counts as a KNN router" rule and the seed-time validation cannot
// drift between the two surfaces. Sentinel errors let the HTTP layer
// map failure modes to status codes without string matching.
// The sentinel texts are sentence fragments so the wrapped messages
// read as the API's established error strings.
var (
	// ErrRouterNotFound: the named model doesn't exist (HTTP 404).
	ErrRouterNotFound = errors.New("not found")
	// ErrNotKNNRouter: the model exists but declares no router.knn
	// block (HTTP 400).
	ErrNotKNNRouter = errors.New("has no router.knn block (set classifier: knn and knn.embedding_model first)")
	// ErrUndeclaredLabel: a seed entry uses a label that is not a
	// declared router policy (HTTP 400).
	ErrUndeclaredLabel = errors.New("is not declared in router policies")
	// ErrEmbedderUnavailable: the router's knn.embedding_model can't
	// be loaded (HTTP 400 — a config problem, not a server fault).
	ErrEmbedderUnavailable = errors.New("not loadable")
)

// ResolveKNNRouter loads the named model config, requires it to declare
// a router.knn block, and resolves the corpus store name the classifier
// build uses — the single resolution path for every corpus surface.
//
// LoadModelConfigFileByName returns a synthetic stub (empty Name) when
// the model is unknown; that maps to ErrRouterNotFound so callers can
// distinguish "unknown model" from "known but not a KNN router".
func ResolveKNNRouter(loader *config.ModelConfigLoader, appConfig *config.ApplicationConfig, name string) (*config.ModelConfig, string, error) {
	cfg, err := loader.LoadModelConfigFileByNameDefaultOptions(name, appConfig)
	if err != nil {
		return nil, "", fmt.Errorf("failed to load model config: %w", err)
	}
	if cfg == nil || cfg.Name == "" {
		return nil, "", fmt.Errorf("model %q %w", name, ErrRouterNotFound)
	}
	if cfg.Router.KNN == nil || cfg.Router.KNN.EmbeddingModel == "" {
		return nil, "", fmt.Errorf("model %q %w", name, ErrNotKNNRouter)
	}
	return cfg, cfg.Router.KNN.ResolvedStoreName(cfg.Name), nil
}

// Seed validates entries against the router's declared policy labels
// (the same invariant candidate tables are validated against — a typo
// must not silently create an unroutable label), embeds and persists
// them via the Manager, and returns the post-seed stats.
func Seed(ctx context.Context, mgr *Manager, cfg *config.ModelConfig, storeName string,
	embedderFor func(string) backend.Embedder, storeFor func(string) backend.VectorStore,
	entries []Entry) (added, skipped int, stats Stats, err error) {

	declared := map[string]struct{}{}
	for _, p := range cfg.Router.Policies {
		declared[p.Label] = struct{}{}
	}
	for i, e := range entries {
		for _, l := range e.Labels {
			if _, ok := declared[l]; !ok {
				return 0, 0, Stats{}, fmt.Errorf("entry %d: label %q %w", i, l, ErrUndeclaredLabel)
			}
		}
	}

	embeddingModel := cfg.Router.KNN.EmbeddingModel
	var embedder backend.Embedder
	if embedderFor != nil {
		embedder = embedderFor(embeddingModel)
	}
	if embedder == nil {
		return 0, 0, Stats{}, fmt.Errorf("embedding_model %q %w", embeddingModel, ErrEmbedderUnavailable)
	}
	var store backend.VectorStore
	if storeFor != nil {
		store = storeFor(storeName)
	}

	added, skipped, err = mgr.Add(ctx, storeName, embeddingModel, embedder, store, entries)
	if err != nil {
		return added, skipped, Stats{}, err
	}
	stats, err = mgr.Stats(storeName)
	return added, skipped, stats, err
}
