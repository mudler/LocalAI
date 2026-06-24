package routes

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
)

func RegisterAgentPoolRoutes(e *echo.Echo, app *application.Application,
	agentsMw, skillsMw, collectionsMw echo.MiddlewareFunc) {
	if !app.ApplicationConfig().AgentPool.Enabled {
		return
	}

	// Middleware that returns 503 while the agent pool is still initializing.
	poolReadyMw := func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if app.AgentPoolService() == nil {
				return c.JSON(http.StatusServiceUnavailable, map[string]string{
					"error": "agent pool is starting, please retry shortly",
				})
			}
			return next(c)
		}
	}

	// Agent management routes — require "agents" feature
	ag := e.Group("/api/agents", poolReadyMw, agentsMw)
	ag.GET("", localai.ListAgentsEndpoint(app))
	ag.POST("", localai.CreateAgentEndpoint(app))
	ag.GET("/config/metadata", localai.GetAgentConfigMetaEndpoint(app))
	ag.POST("/import", localai.ImportAgentEndpoint(app))
	ag.GET("/:name", localai.GetAgentEndpoint(app))
	ag.PUT("/:name", localai.UpdateAgentEndpoint(app))
	ag.DELETE("/:name", localai.DeleteAgentEndpoint(app))
	ag.GET("/:name/config", localai.GetAgentConfigEndpoint(app))
	ag.PUT("/:name/pause", localai.PauseAgentEndpoint(app))
	ag.PUT("/:name/resume", localai.ResumeAgentEndpoint(app))
	ag.GET("/:name/status", localai.GetAgentStatusEndpoint(app))
	ag.GET("/:name/observables", localai.GetAgentObservablesEndpoint(app))
	ag.DELETE("/:name/observables", localai.ClearAgentObservablesEndpoint(app))
	ag.POST("/:name/chat", localai.ChatWithAgentEndpoint(app))
	ag.GET("/:name/sse", localai.AgentSSEEndpoint(app))
	ag.GET("/:name/export", localai.ExportAgentEndpoint(app))
	ag.GET("/:name/files", localai.AgentFileEndpoint(app))

	// Actions (part of agents feature)
	ag.GET("/actions", localai.ListActionsEndpoint(app))
	ag.POST("/actions/:name/definition", localai.GetActionDefinitionEndpoint(app))
	ag.POST("/actions/:name/run", localai.ExecuteActionEndpoint(app))

	// Skills routes — require "skills" feature
	sg := e.Group("/api/agents/skills", poolReadyMw, skillsMw)
	sg.GET("", localai.ListSkillsEndpoint(app))
	sg.GET("/config", localai.GetSkillsConfigEndpoint(app))
	sg.GET("/search", localai.SearchSkillsEndpoint(app))
	sg.POST("", localai.CreateSkillEndpoint(app))
	sg.GET("/export/*", localai.ExportSkillEndpoint(app))
	sg.POST("/import", localai.ImportSkillEndpoint(app))
	sg.GET("/:name", localai.GetSkillEndpoint(app))
	sg.PUT("/:name", localai.UpdateSkillEndpoint(app))
	sg.DELETE("/:name", localai.DeleteSkillEndpoint(app))
	sg.GET("/:name/resources", localai.ListSkillResourcesEndpoint(app))
	sg.GET("/:name/resources/*", localai.GetSkillResourceEndpoint(app))
	sg.POST("/:name/resources", localai.CreateSkillResourceEndpoint(app))
	sg.PUT("/:name/resources/*", localai.UpdateSkillResourceEndpoint(app))
	sg.DELETE("/:name/resources/*", localai.DeleteSkillResourceEndpoint(app))

	// Git Repos — guarded by skills feature (at original /api/agents/git-repos path)
	gg := e.Group("/api/agents/git-repos", poolReadyMw, skillsMw)
	gg.GET("", localai.ListGitReposEndpoint(app))
	gg.POST("", localai.AddGitRepoEndpoint(app))
	gg.PUT("/:id", localai.UpdateGitRepoEndpoint(app))
	gg.DELETE("/:id", localai.DeleteGitRepoEndpoint(app))
	gg.POST("/:id/sync", localai.SyncGitRepoEndpoint(app))
	gg.POST("/:id/toggle", localai.ToggleGitRepoEndpoint(app))

	// Collections / Knowledge Base — require "collections" feature
	cg := e.Group("/api/agents/collections", poolReadyMw, collectionsMw)
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
