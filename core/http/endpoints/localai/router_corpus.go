package localai

import (
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/routing/corpus"
)

// The router corpus endpoints manage the labelled exemplar corpus
// behind the knn classifier. Corpus input is API-only by design:
// entries can contain example user content, so they are seeded and
// curated programmatically and never entered through or displayed in
// the UI — the inspection surface returns label counts, never texts.
//
// All three handlers resolve the router model by path param and
// require it to declare a `router.knn` block; the store name and
// embedding model come from that config so the API can't desync from
// what the classifier actually queries.

// resolveKNNRouter loads the named model config and returns its router
// KNN settings (store name defaulted the same way buildClassifier
// defaults it). Echo-shaped errors for the three failure modes.
func resolveKNNRouter(c echo.Context, loader *config.ModelConfigLoader, appConfig *config.ApplicationConfig) (*config.ModelConfig, string, error) {
	name := c.Param("name")
	if name == "" {
		return nil, "", echo.NewHTTPError(http.StatusBadRequest, "router name is required")
	}
	cfg, err := loader.LoadModelConfigFileByNameDefaultOptions(name, appConfig)
	if err != nil {
		return nil, "", echo.NewHTTPError(http.StatusInternalServerError, "failed to load model config: "+err.Error())
	}
	// A synthetic stub (no Name) means the model is unknown — see
	// RouterDecideEndpoint for the discrimination rationale.
	if cfg == nil || cfg.Name == "" {
		return nil, "", echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("model %q not found", name))
	}
	if cfg.Router.KNN == nil || cfg.Router.KNN.EmbeddingModel == "" {
		return nil, "", echo.NewHTTPError(http.StatusBadRequest,
			fmt.Sprintf("model %q has no router.knn block (set classifier: knn and knn.embedding_model first)", name))
	}
	storeName := cfg.Router.KNN.StoreName
	if storeName == "" {
		storeName = "router-corpus-" + cfg.Name
	}
	return cfg, storeName, nil
}

// RouterCorpusAddEndpoint bulk-seeds a router's KNN corpus. Entries
// are validated against the router's policy labels, embedded with the
// router's knn.embedding_model, persisted to the corpus file, and
// upserted into the live vector index — routing sees them immediately,
// no reload required.
//
// @Summary  Seed the KNN routing corpus with labelled example prompts
// @Tags     router
// @Accept   json
// @Produce  json
// @Param    name    path string true "router model name"
// @Param    request body schema.RouterCorpusAddRequest true "labelled exemplars"
// @Success  200 {object} schema.RouterCorpusAddResponse
// @Failure  400 {object} map[string]string
// @Failure  404 {object} map[string]string
// @Failure  500 {object} map[string]string
// @Router   /api/router/{name}/corpus [post]
func RouterCorpusAddEndpoint(loader *config.ModelConfigLoader, appConfig *config.ApplicationConfig, mgr *corpus.Manager, deps middleware.ClassifierDeps) echo.HandlerFunc {
	return func(c echo.Context) error {
		cfg, storeName, err := resolveKNNRouter(c, loader, appConfig)
		if err != nil {
			return err
		}
		var req schema.RouterCorpusAddRequest
		if err := c.Bind(&req); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid request body: "+err.Error())
		}
		if len(req.Entries) == 0 {
			return echo.NewHTTPError(http.StatusBadRequest, "entries is required")
		}

		// Labels must be declared policies — the same invariant
		// candidate tables are validated against. Catching it here
		// keeps a typo from silently creating an unroutable label.
		declared := map[string]struct{}{}
		for _, p := range cfg.Router.Policies {
			declared[p.Label] = struct{}{}
		}
		entries := make([]corpus.Entry, 0, len(req.Entries))
		for i, e := range req.Entries {
			for _, l := range e.Labels {
				if _, ok := declared[l]; !ok {
					return echo.NewHTTPError(http.StatusBadRequest,
						fmt.Sprintf("entry %d: label %q is not declared in router policies", i, l))
				}
			}
			entries = append(entries, corpus.Entry{Text: e.Text, Labels: e.Labels})
		}

		embedder := deps.Embedder(cfg.Router.KNN.EmbeddingModel)
		if embedder == nil {
			return echo.NewHTTPError(http.StatusBadRequest,
				fmt.Sprintf("embedding_model %q not loadable", cfg.Router.KNN.EmbeddingModel))
		}
		store := deps.VectorStore(storeName)

		added, skipped, err := mgr.Add(c.Request().Context(), storeName, cfg.Router.KNN.EmbeddingModel, embedder, store, entries)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		stats, err := mgr.Stats(storeName)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		return c.JSON(http.StatusOK, schema.RouterCorpusAddResponse{
			Router:      cfg.Name,
			Added:       added,
			Skipped:     skipped,
			Total:       stats.Total,
			LabelCounts: stats.LabelCounts,
		})
	}
}

// RouterCorpusStatsEndpoint reports corpus size and per-label counts.
// Deliberately count-only: corpus texts never leave the server.
//
// @Summary  Inspect a router's KNN corpus (label counts only, never texts)
// @Tags     router
// @Produce  json
// @Param    name path string true "router model name"
// @Success  200 {object} schema.RouterCorpusStatsResponse
// @Failure  400 {object} map[string]string
// @Failure  404 {object} map[string]string
// @Failure  500 {object} map[string]string
// @Router   /api/router/{name}/corpus/stats [get]
func RouterCorpusStatsEndpoint(loader *config.ModelConfigLoader, appConfig *config.ApplicationConfig, mgr *corpus.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		cfg, storeName, err := resolveKNNRouter(c, loader, appConfig)
		if err != nil {
			return err
		}
		stats, err := mgr.Stats(storeName)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		return c.JSON(http.StatusOK, schema.RouterCorpusStatsResponse{
			Router:          cfg.Name,
			StoreName:       storeName,
			EmbeddingModel:  cfg.Router.KNN.EmbeddingModel,
			Total:           stats.Total,
			LabelCounts:     stats.LabelCounts,
			EmbeddingModels: stats.EmbeddingModels,
		})
	}
}

// RouterCorpusClearEndpoint wipes a router's corpus — file and live
// index. Reseed with POST afterwards; there is intentionally no
// per-entry delete (exemplars are curated as a set; partial edits by
// vector identity are how mislabelled neighbourhoods linger).
//
// @Summary  Clear a router's KNN corpus
// @Tags     router
// @Produce  json
// @Param    name path string true "router model name"
// @Success  200 {object} schema.RouterCorpusClearResponse
// @Failure  400 {object} map[string]string
// @Failure  404 {object} map[string]string
// @Failure  500 {object} map[string]string
// @Router   /api/router/{name}/corpus [delete]
func RouterCorpusClearEndpoint(loader *config.ModelConfigLoader, appConfig *config.ApplicationConfig, mgr *corpus.Manager, deps middleware.ClassifierDeps) echo.HandlerFunc {
	return func(c echo.Context) error {
		cfg, storeName, err := resolveKNNRouter(c, loader, appConfig)
		if err != nil {
			return err
		}
		cleared, err := mgr.Clear(c.Request().Context(), storeName, deps.VectorStore(storeName))
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		return c.JSON(http.StatusOK, schema.RouterCorpusClearResponse{
			Router:  cfg.Name,
			Cleared: cleared,
		})
	}
}
