package localai

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/backend"
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
// The handlers are transport adapters over corpus.ResolveKNNRouter and
// corpus.Seed — the same helpers the assistant MCP tools use — so the
// two surfaces cannot drift on model resolution, store naming, or
// seed validation. This layer only binds requests and maps the corpus
// package's sentinel errors to HTTP statuses.

// resolveKNNRouter adapts corpus.ResolveKNNRouter to echo: name from
// the path param, sentinel errors to 404/400, the rest to 500.
func resolveKNNRouter(c echo.Context, loader *config.ModelConfigLoader, appConfig *config.ApplicationConfig) (*config.ModelConfig, string, error) {
	name := c.Param("name")
	if name == "" {
		return nil, "", echo.NewHTTPError(http.StatusBadRequest, "router name is required")
	}
	cfg, storeName, err := corpus.ResolveKNNRouter(loader, appConfig, name)
	switch {
	case err == nil:
		return cfg, storeName, nil
	case errors.Is(err, corpus.ErrRouterNotFound):
		return nil, "", echo.NewHTTPError(http.StatusNotFound, err.Error())
	case errors.Is(err, corpus.ErrNotKNNRouter):
		return nil, "", echo.NewHTTPError(http.StatusBadRequest, err.Error())
	default:
		return nil, "", echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
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
		entries := make([]corpus.Entry, 0, len(req.Entries))
		for _, e := range req.Entries {
			entries = append(entries, corpus.Entry{Text: e.Text, Labels: e.Labels})
		}

		added, skipped, stats, err := corpus.Seed(c.Request().Context(), mgr, cfg, storeName,
			deps.Embedder, deps.EmbedderFingerprint, deps.VectorStore, entries)
		if err != nil {
			// Undeclared labels and an unloadable embedding model are
			// request/config mistakes, not server faults.
			if errors.Is(err, corpus.ErrUndeclaredLabel) || errors.Is(err, corpus.ErrInvalidEntry) || errors.Is(err, corpus.ErrEmbedderUnavailable) {
				return echo.NewHTTPError(http.StatusBadRequest, err.Error())
			}
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
		var store backend.VectorStore
		if deps.VectorStore != nil {
			store = deps.VectorStore(storeName)
		}
		cleared, err := mgr.Clear(c.Request().Context(), storeName, store)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		return c.JSON(http.StatusOK, schema.RouterCorpusClearResponse{
			Router:  cfg.Name,
			Cleared: cleared,
		})
	}
}
