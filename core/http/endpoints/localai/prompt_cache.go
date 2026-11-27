package localai

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/promptcachesvc"
	"github.com/mudler/xlog"
)

// PromptCacheClearRequest is the body of POST /api/prompt-cache/clear.
type PromptCacheClearRequest struct {
	// Model is the model whose files go; empty clears every model.
	Model string `json:"model,omitempty"`
}

// PromptCacheClearResponse says what a clear deleted.
type PromptCacheClearResponse struct {
	Files int   `json:"files"`
	Bytes int64 `json:"bytes"`
}

func promptCacheError(c echo.Context, code int, msg string) error {
	return c.JSON(code, schema.ErrorResponse{Error: &schema.APIError{Message: msg, Code: code, Type: "invalid_request_error"}})
}

// PromptCacheStatusEndpoint reports the disk prompt cache.
// @Summary Show the disk prompt cache: size, free disk, effective cap and state of each node, and the size of each model.
// @Tags prompt-cache
// @Produce json
// @Success 200 {object} promptcachesvc.StatusReport "Response"
// @Router /api/prompt-cache [get]
func PromptCacheStatusEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		resp, err := promptcachesvc.Report(c.Request().Context(), app.PromptCacheNodes(),
			app.PromptCacheEnabled(), app.ApplicationConfig().PromptCache.Shared,
			promptcachesvc.ModelNames(app.ModelConfigLoader()))
		if err != nil {
			xlog.Warn("prompt cache usage failed", "error", err)
			return promptCacheError(c, http.StatusInternalServerError, "could not read the prompt cache")
		}
		return c.JSON(http.StatusOK, resp)
	}
}

// PromptCacheClearEndpoint deletes the disk prompt cache of every model, or
// of one model.
// @Summary Clear the disk prompt cache, of every model or of one model.
// @Tags prompt-cache
// @Accept json
// @Produce json
// @Param request body PromptCacheClearRequest false "model to clear, empty for all"
// @Success 200 {object} PromptCacheClearResponse "Response"
// @Router /api/prompt-cache/clear [post]
func PromptCacheClearEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req PromptCacheClearRequest
		if c.Request().ContentLength != 0 {
			if err := c.Bind(&req); err != nil {
				return promptCacheError(c, http.StatusBadRequest, "invalid request body")
			}
		}
		scope := promptcachesvc.Scope{All: true}
		if req.Model != "" {
			cfg, ok := app.ModelConfigLoader().GetModelConfig(req.Model)
			if !ok || cfg.Model == "" {
				return promptCacheError(c, http.StatusNotFound, "model not found")
			}
			scope = promptcachesvc.ModelScope(cfg.Model)
		}
		res, err := app.PromptCacheNodes().Purge(c.Request().Context(), scope)
		if err != nil {
			xlog.Warn("prompt cache clear failed", "model", req.Model, "error", err)
			return promptCacheError(c, http.StatusInternalServerError, "could not clear the prompt cache")
		}
		return c.JSON(http.StatusOK, PromptCacheClearResponse{Files: res.Files, Bytes: res.Bytes})
	}
}

// PromptCacheClearMineEndpoint deletes the cached prompts of the caller: its
// own namespace and the ones of its client keys. Entries of the shared
// namespace belong to no user and stay.
// @Summary Clear my cached prompts from the disk prompt cache.
// @Tags prompt-cache
// @Produce json
// @Success 200 {object} PromptCacheClearResponse "Response"
// @Router /api/prompt-cache/mine [delete]
func PromptCacheClearMineEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		namespaces := []string{middleware.PromptCacheNamespace(c)}
		if u := auth.GetUser(c); u != nil && u.ID != "" {
			namespaces = auth.UserPromptCacheNamespaces(app.AuthDB(), u.ID)
		}
		res, err := app.PromptCacheNodes().Purge(c.Request().Context(), promptcachesvc.NamespaceScope(namespaces...))
		if err != nil && !errors.Is(err, promptcachesvc.ErrNoScope) {
			xlog.Warn("prompt cache clear of a user failed", "error", err)
			return promptCacheError(c, http.StatusInternalServerError, "could not clear your cached prompts")
		}
		return c.JSON(http.StatusOK, PromptCacheClearResponse{Files: res.Files, Bytes: res.Bytes})
	}
}
