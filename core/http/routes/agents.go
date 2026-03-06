package routes

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
)

func RegisterAgentPoolRoutes(e *echo.Echo, app *application.Application) {
	if app.AgentPoolService() == nil {
		return
	}

	// Agent Management
	e.GET("/api/agents", localai.ListAgentsEndpoint(app))
	e.POST("/api/agents", localai.CreateAgentEndpoint(app))
	e.GET("/api/agents/config/metadata", localai.GetAgentConfigMetaEndpoint(app))
	e.POST("/api/agents/import", localai.ImportAgentEndpoint(app))
	e.GET("/api/agents/:name", localai.GetAgentEndpoint(app))
	e.PUT("/api/agents/:name", localai.UpdateAgentEndpoint(app))
	e.DELETE("/api/agents/:name", localai.DeleteAgentEndpoint(app))
	e.GET("/api/agents/:name/config", localai.GetAgentConfigEndpoint(app))
	e.PUT("/api/agents/:name/pause", localai.PauseAgentEndpoint(app))
	e.PUT("/api/agents/:name/resume", localai.ResumeAgentEndpoint(app))
	e.GET("/api/agents/:name/status", localai.GetAgentStatusEndpoint(app))
	e.GET("/api/agents/:name/observables", localai.GetAgentObservablesEndpoint(app))
	e.DELETE("/api/agents/:name/observables", localai.ClearAgentObservablesEndpoint(app))
	e.POST("/api/agents/:name/chat", localai.ChatWithAgentEndpoint(app))
	e.GET("/api/agents/:name/sse", localai.AgentSSEEndpoint(app))
	e.GET("/api/agents/:name/export", localai.ExportAgentEndpoint(app))

	// Actions
	e.GET("/api/agents/actions", localai.ListActionsEndpoint(app))
	e.POST("/api/agents/actions/:name/definition", localai.GetActionDefinitionEndpoint(app))
	e.POST("/api/agents/actions/:name/run", localai.ExecuteActionEndpoint(app))

	// Skills
	e.GET("/api/agents/skills", localai.ListSkillsEndpoint(app))
	e.GET("/api/agents/skills/config", localai.GetSkillsConfigEndpoint(app))
	e.GET("/api/agents/skills/search", localai.SearchSkillsEndpoint(app))
	e.POST("/api/agents/skills", localai.CreateSkillEndpoint(app))
	e.GET("/api/agents/skills/export/*", localai.ExportSkillEndpoint(app))
	e.POST("/api/agents/skills/import", localai.ImportSkillEndpoint(app))
	e.GET("/api/agents/skills/:name", localai.GetSkillEndpoint(app))
	e.PUT("/api/agents/skills/:name", localai.UpdateSkillEndpoint(app))
	e.DELETE("/api/agents/skills/:name", localai.DeleteSkillEndpoint(app))
	e.GET("/api/agents/skills/:name/resources", localai.ListSkillResourcesEndpoint(app))
	e.GET("/api/agents/skills/:name/resources/*", localai.GetSkillResourceEndpoint(app))
	e.POST("/api/agents/skills/:name/resources", localai.CreateSkillResourceEndpoint(app))
	e.PUT("/api/agents/skills/:name/resources/*", localai.UpdateSkillResourceEndpoint(app))
	e.DELETE("/api/agents/skills/:name/resources/*", localai.DeleteSkillResourceEndpoint(app))

	// Git Repos
	e.GET("/api/agents/git-repos", localai.ListGitReposEndpoint(app))
	e.POST("/api/agents/git-repos", localai.AddGitRepoEndpoint(app))
	e.PUT("/api/agents/git-repos/:id", localai.UpdateGitRepoEndpoint(app))
	e.DELETE("/api/agents/git-repos/:id", localai.DeleteGitRepoEndpoint(app))
	e.POST("/api/agents/git-repos/:id/sync", localai.SyncGitRepoEndpoint(app))
	e.POST("/api/agents/git-repos/:id/toggle", localai.ToggleGitRepoEndpoint(app))

	// Collections / Knowledge Base
	e.GET("/api/agents/collections", localai.ListCollectionsEndpoint(app))
	e.POST("/api/agents/collections", localai.CreateCollectionEndpoint(app))
	e.POST("/api/agents/collections/:name/upload", localai.UploadToCollectionEndpoint(app))
	e.GET("/api/agents/collections/:name/entries", localai.ListCollectionEntriesEndpoint(app))
	e.GET("/api/agents/collections/:name/entries/*", localai.GetCollectionEntryContentEndpoint(app))
	e.POST("/api/agents/collections/:name/search", localai.SearchCollectionEndpoint(app))
	e.POST("/api/agents/collections/:name/reset", localai.ResetCollectionEndpoint(app))
	e.DELETE("/api/agents/collections/:name/entry/delete", localai.DeleteCollectionEntryEndpoint(app))
	e.POST("/api/agents/collections/:name/sources", localai.AddCollectionSourceEndpoint(app))
	e.DELETE("/api/agents/collections/:name/sources", localai.RemoveCollectionSourceEndpoint(app))
	e.GET("/api/agents/collections/:name/sources", localai.ListCollectionSourcesEndpoint(app))
}
