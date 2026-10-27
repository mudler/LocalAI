package routes

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
)

// RegisterCollectionsRoutes registers the knowledge-base collections API. It
// does nothing when the feature is off.
func RegisterCollectionsRoutes(e *echo.Echo, app *application.Application, collectionsMw echo.MiddlewareFunc) {
	if !app.ApplicationConfig().AgentPool.Enabled {
		return
	}

	// 503 while the service is still starting.
	readyMw := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if app.CollectionsService() == nil {
				return c.JSON(http.StatusServiceUnavailable, map[string]string{
					"error": "collections are starting, please retry shortly",
				})
			}
			return next(c)
		}
	}

	// Collections / Knowledge Base — require "collections" feature
	cg := e.Group("/api/agents/collections", readyMw, collectionsMw)
	cg.GET("", localai.ListCollectionsEndpoint(app))
	cg.POST("", localai.CreateCollectionEndpoint(app))
	cg.POST("/:name/upload", localai.UploadToCollectionEndpoint(app))
	cg.GET("/:name/entries", localai.ListCollectionEntriesEndpoint(app))
	cg.GET("/:name/entries/*", localai.GetCollectionEntryContentEndpoint(app))
	cg.GET("/:name/entries-raw/*", localai.GetCollectionEntryRawFileEndpoint(app))
	cg.POST("/:name/search", localai.SearchCollectionEndpoint(app))
	cg.POST("/:name/reset", localai.ResetCollectionEndpoint(app))
	cg.DELETE("/:name/entry/delete", localai.DeleteCollectionEntryEndpoint(app))
	cg.POST("/:name/sources", localai.AddCollectionSourceEndpoint(app))
	cg.DELETE("/:name/sources", localai.RemoveCollectionSourceEndpoint(app))
	cg.GET("/:name/sources", localai.ListCollectionSourcesEndpoint(app))
}
