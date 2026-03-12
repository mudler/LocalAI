package routes

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
)

func RegisterAgentPoolRoutes(e *echo.Echo, app *application.Application) {
	if !app.ApplicationConfig().AgentPool.Enabled {
		return
	}

	// Group all agent routes behind a middleware that returns 503 while the
	// agent pool is still initializing (it starts after the HTTP server).
	g := e.Group("/api/agents", func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if app.AgentPoolService() == nil {
				return c.JSON(http.StatusServiceUnavailable, map[string]string{
					"error": "agent pool is starting, please retry shortly",
				})
			}
			return next(c)
		}
	})

	// Agent Management
	g.GET("", localai.ListAgentsEndpoint(app))
	g.POST("", localai.CreateAgentEndpoint(app))
	g.GET("/config/metadata", localai.GetAgentConfigMetaEndpoint(app))
	g.POST("/import", localai.ImportAgentEndpoint(app))
	g.GET("/:name", localai.GetAgentEndpoint(app))
	g.PUT("/:name", localai.UpdateAgentEndpoint(app))
	g.DELETE("/:name", localai.DeleteAgentEndpoint(app))
	g.GET("/:name/config", localai.GetAgentConfigEndpoint(app))
	g.PUT("/:name/pause", localai.PauseAgentEndpoint(app))
	g.PUT("/:name/resume", localai.ResumeAgentEndpoint(app))
	g.GET("/:name/status", localai.GetAgentStatusEndpoint(app))
	g.GET("/:name/observables", localai.GetAgentObservablesEndpoint(app))
	g.DELETE("/:name/observables", localai.ClearAgentObservablesEndpoint(app))
	g.POST("/:name/chat", localai.ChatWithAgentEndpoint(app))
	g.GET("/:name/sse", localai.AgentSSEEndpoint(app))
	g.GET("/:name/export", localai.ExportAgentEndpoint(app))
	g.GET("/:name/files", localai.AgentFileEndpoint(app))

	// Actions
	g.GET("/actions", localai.ListActionsEndpoint(app))
	g.POST("/actions/:name/definition", localai.GetActionDefinitionEndpoint(app))
	g.POST("/actions/:name/run", localai.ExecuteActionEndpoint(app))

	// Skills
	g.GET("/skills", localai.ListSkillsEndpoint(app))
	g.GET("/skills/config", localai.GetSkillsConfigEndpoint(app))
	g.GET("/skills/search", localai.SearchSkillsEndpoint(app))
	g.POST("/skills", localai.CreateSkillEndpoint(app))
	g.GET("/skills/export/*", localai.ExportSkillEndpoint(app))
	g.POST("/skills/import", localai.ImportSkillEndpoint(app))
	g.GET("/skills/:name", localai.GetSkillEndpoint(app))
	g.PUT("/skills/:name", localai.UpdateSkillEndpoint(app))
	g.DELETE("/skills/:name", localai.DeleteSkillEndpoint(app))
	g.GET("/skills/:name/resources", localai.ListSkillResourcesEndpoint(app))
	g.GET("/skills/:name/resources/*", localai.GetSkillResourceEndpoint(app))
	g.POST("/skills/:name/resources", localai.CreateSkillResourceEndpoint(app))
	g.PUT("/skills/:name/resources/*", localai.UpdateSkillResourceEndpoint(app))
	g.DELETE("/skills/:name/resources/*", localai.DeleteSkillResourceEndpoint(app))

	// Git Repos
	g.GET("/git-repos", localai.ListGitReposEndpoint(app))
	g.POST("/git-repos", localai.AddGitRepoEndpoint(app))
	g.PUT("/git-repos/:id", localai.UpdateGitRepoEndpoint(app))
	g.DELETE("/git-repos/:id", localai.DeleteGitRepoEndpoint(app))
	g.POST("/git-repos/:id/sync", localai.SyncGitRepoEndpoint(app))
	g.POST("/git-repos/:id/toggle", localai.ToggleGitRepoEndpoint(app))

	// Collections / Knowledge Base
	g.GET("/collections", localai.ListCollectionsEndpoint(app))
	g.POST("/collections", localai.CreateCollectionEndpoint(app))
	g.POST("/collections/:name/upload", localai.UploadToCollectionEndpoint(app))
	g.GET("/collections/:name/entries", localai.ListCollectionEntriesEndpoint(app))
	g.GET("/collections/:name/entries/*", localai.GetCollectionEntryContentEndpoint(app))
	g.POST("/collections/:name/search", localai.SearchCollectionEndpoint(app))
	g.POST("/collections/:name/reset", localai.ResetCollectionEndpoint(app))
	g.DELETE("/collections/:name/entry/delete", localai.DeleteCollectionEntryEndpoint(app))
	g.POST("/collections/:name/sources", localai.AddCollectionSourceEndpoint(app))
	g.DELETE("/collections/:name/sources", localai.RemoveCollectionSourceEndpoint(app))
	g.GET("/collections/:name/sources", localai.ListCollectionSourcesEndpoint(app))
}
