package routes

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	studioendpoint "github.com/mudler/LocalAI/core/http/endpoints/studio"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
)

// NewStudioModelChecker says whether a user may start a conversation with a
// model: it must be a model this instance has, and the user's model allowlist,
// when they have one, must include it. The chat route checks the allowlist again
// on every request, so this only gives an early, clear answer.
func NewStudioModelChecker(app *application.Application) studioendpoint.ModelChecker {
	return studioendpoint.ModelCheckerFunc(func(_ context.Context, user *auth.User, model string) error {
		if _, ok := app.ModelConfigLoader().GetModelConfig(model); !ok {
			return studioendpoint.ErrUnknownModel
		}
		if db := app.AuthDB(); db != nil && user != nil && !auth.IsModelAllowed(db, user, model) {
			return studioendpoint.ErrModelNotAllowed
		}
		return nil
	})
}

// RegisterStudioRoutes registers the chats API. Without a service (the chats
// are switched off or could not start) every /api/chats path answers 404 with
// the reason, so a client sees "off" and not a missing route. Authentication and
// the feature gates come from the global middleware and the route-feature
// registry, so no route carries its own; the administrator routes take adminMw.
func RegisterStudioRoutes(e *echo.Echo, svc *studiosvc.Service, models studioendpoint.ModelChecker, adminMw echo.MiddlewareFunc) {
	if svc == nil {
		off := func(c echo.Context) error {
			return c.JSON(http.StatusNotFound, schema.ErrorResponse{Error: &schema.APIError{
				Message: "the chats are turned off on this server", Code: http.StatusNotFound, Type: "chats_off"}})
		}
		e.Any("/api/chats", off)
		e.Any("/api/chats/*", off)
		e.Any("/api/workspace/*", off)
		return
	}
	h := studioendpoint.New(svc, models)
	const base = "/api/chats"
	// The files routes come before the "/:id" ones; "files" is not a chat id.
	// The administrator's view of every user's agents, triggers and connectors.
	// "admin" is not a chat id, and the static segment wins over "/:id".
	e.GET(base+"/admin/agents", h.AdminListAgents, adminMw)
	e.DELETE(base+"/admin/agents/:id", h.AdminDeleteAgent, adminMw)
	e.GET(base+"/admin/triggers", h.AdminListTriggers, adminMw)
	e.GET(base+"/admin/triggers/:id/runs", h.AdminTriggerRuns, adminMw)
	e.POST(base+"/admin/triggers/:id/disable", h.AdminDisableTrigger, adminMw)
	e.DELETE(base+"/admin/triggers/:id", h.AdminDeleteTrigger, adminMw)
	e.GET(base+"/admin/connectors", h.AdminListConnectors, adminMw)
	e.POST(base+"/admin/connectors/:id/disable", h.AdminDisableConnector, adminMw)
	e.DELETE(base+"/admin/connectors/:id", h.AdminDeleteConnector, adminMw)
	// The agent tasks and jobs keep the paths they had on master. They are
	// gated by the mcp_jobs feature in the route-feature registry.
	const agent = "/api/agent"
	e.POST(agent+"/tasks", h.CreateTask)
	e.GET(agent+"/tasks", h.ListTasks)
	e.GET(agent+"/tasks/:id", h.GetTask)
	e.PUT(agent+"/tasks/:id", h.UpdateTask)
	e.DELETE(agent+"/tasks/:id", h.DeleteTask)
	e.POST(agent+"/tasks/:name/execute", h.ExecuteTaskByName)
	e.POST(agent+"/jobs/execute", h.ExecuteJob)
	e.GET(agent+"/jobs", h.ListJobs)
	e.GET(agent+"/jobs/:id", h.GetJob)
	e.POST(agent+"/jobs/:id/cancel", h.CancelJob)
	e.POST(agent+"/jobs/:id/rerun", h.RerunJob)
	e.DELETE(agent+"/jobs/:id", h.DeleteJob)
	e.GET(base+"/files", h.ListFiles)
	e.DELETE(base+"/files", h.DeleteFile)
	e.GET(base+"/files/content", h.GetFileContent)
	e.PUT(base+"/files/content", h.PutFileContent)
	e.POST(base+"/files/folders", h.CreateFolder)
	e.POST(base+"/files/move", h.MoveFile)
	e.GET(base+"/files/trash", h.Trash)
	e.POST(base+"/files/undo", h.UndoFiles)
	e.GET(base+"/files/history", h.FileHistory)
	e.GET(base+"/wellknown/:name", h.GetWellKnown)
	e.PUT(base+"/wellknown/:name", h.PutWellKnown)
	e.POST(base+"/wellknown/:name/revert", h.RevertWellKnown)
	e.GET(base+"/wellknown/:name/schema", h.WellKnownSchema)
	e.GET(base+"/skills", h.ListSkills)
	e.POST(base+"/skills", h.CreateSkill)
	e.GET(base+"/skills/:layer/:name", h.GetSkill)
	e.PUT(base+"/skills/:layer/:name", h.UpdateSkill)
	e.DELETE(base+"/skills/:layer/:name", h.DeleteSkill)
	e.PUT(base+"/:id/skills", h.SetChatSkills)
	e.GET(base+"/:id/addons", h.ListChatAddons)
	e.PUT(base+"/:id/addons", h.SetChatAddons)
	e.GET(base+"/files/search", h.SearchFiles)
	// The pages of the workspace: the chats feature lets the request in, and
	// the handlers check the pages feature too.
	e.GET(base+"/pages", h.ListPages)
	e.GET(base+"/pages/:name", h.GetPage)
	e.GET(base+"/pages/:name/files", h.ListPageFiles)
	e.GET(base+"/pages/:name/file", h.ReadPageFile)
	e.PUT(base+"/pages/:name/file", h.WritePageFile)
	e.POST(base+"/pages/:name/chat", h.SendFromPage)
	// The pages of the caller's shared folders, by folder id and name, and the
	// caller's consent to the version that runs.
	e.GET(base+"/shared-pages", h.ListSharedPages)
	e.GET(base+"/shared-pages/:folder/:name", h.GetSharedPage)
	e.PUT(base+"/shared-pages/:folder/:name/consent", h.AllowSharedPage)
	e.DELETE(base+"/shared-pages/:folder/:name/consent", h.RevokeSharedPage)
	e.GET(base+"/shared-pages/:folder/:name/files", h.ListSharedPageFiles)
	e.GET(base+"/shared-pages/:folder/:name/file", h.ReadSharedPageFile)
	e.PUT(base+"/shared-pages/:folder/:name/file", h.WriteSharedPageFile)
	e.POST(base+"/shared-pages/:folder/:name/chat", h.SendFromSharedPage)
	e.GET(base+"/page-starters", h.ListPageStarters)
	e.POST(base+"/page-starters/:id/copy", h.CopyPageStarter)
	e.GET(base+"/conversion", h.Conversion)
	e.POST(base+"/import/browser", h.ImportBrowserChats)
	e.GET(base+"/notifications", h.ListNotifications)
	e.DELETE(base+"/notifications", h.DeleteNotifications)
	e.GET(base+"/notifications/count", h.CountNotifications)
	e.POST(base+"/notifications/read", h.ReadNotifications)
	e.GET(base+"/notifications/events", h.NotificationEvents)
	// The change feed of the user's files belongs to the chats feature: the
	// files are the chats' workspace.
	e.GET("/api/workspace/events", h.WorkspaceEvents)
	e.GET(base+"/triggers", h.ListTriggers)
	e.POST(base+"/triggers", h.CreateTrigger)
	e.GET(base+"/triggers/preview", h.PreviewTrigger)
	e.GET(base+"/triggers/proposals", h.ListTriggerProposals)
	e.POST(base+"/triggers/proposals/:id/confirm", h.ConfirmTriggerProposal)
	e.PUT(base+"/triggers/proposals/:id", h.EditTriggerProposal)
	e.DELETE(base+"/triggers/proposals/:id", h.DismissTriggerProposal)
	e.GET(base+"/triggers/:id", h.GetTrigger)
	e.PUT(base+"/triggers/:id", h.UpdateTrigger)
	e.DELETE(base+"/triggers/:id", h.DeleteTrigger)
	e.POST(base+"/triggers/:id/run", h.RunTrigger)
	e.POST(base+"/triggers/:id/enable", h.EnableTrigger)
	e.POST(base+"/triggers/:id/disable", h.DisableTrigger)
	e.GET(base+"/triggers/:id/runs", h.TriggerRuns)
	e.GET(base+"/push/key", h.PushKey)
	e.POST(base+"/push/subscribe", h.PushSubscribe)
	e.DELETE(base+"/push/subscribe", h.PushUnsubscribe)
	e.GET(base+"/connectors", h.ListConnectors)
	e.GET(base+"/connectors/kinds", h.ConnectorKinds)
	e.POST(base+"/connectors", h.CreateConnector)
	e.GET(base+"/connectors/:id", h.GetConnector)
	e.PUT(base+"/connectors/:id", h.UpdateConnector)
	e.DELETE(base+"/connectors/:id", h.DeleteConnector)
	e.POST(base+"/connectors/:id/enable", h.EnableConnector)
	e.POST(base+"/connectors/:id/disable", h.DisableConnector)
	e.POST(base+"/connectors/:id/test", h.TestConnector)
	e.GET(base+"/connectors/:id/senders", h.ConnectorSenders)
	e.POST(base+"/connectors/:id/senders/:sender/allow", h.AllowConnectorSender)
	e.GET(base+"/mcp-connectors", h.ListMCPConnectors)
	e.POST(base+"/mcp-connectors", h.CreateMCPConnector)
	e.GET(base+"/mcp-connectors/:id", h.GetMCPConnector)
	e.PUT(base+"/mcp-connectors/:id", h.UpdateMCPConnector)
	e.DELETE(base+"/mcp-connectors/:id", h.DeleteMCPConnector)
	e.POST(base+"/mcp-connectors/:id/enable", h.EnableMCPConnector)
	e.POST(base+"/mcp-connectors/:id/disable", h.DisableMCPConnector)
	e.POST(base+"/mcp-connectors/:id/test", h.TestMCPConnector)
	e.GET(base+"/:id/tools", h.ListChatTools)
	e.PUT(base+"/:id/mcp", h.SetChatMCP)
	e.GET(base+"/:id/mcp/prompts", h.MCPPrompts)
	e.POST(base+"/:id/mcp/prompts", h.MCPExpandPrompt)
	e.GET(base+"/:id/mcp/resources", h.MCPResources)
	e.POST(base+"/:id/mcp/resources/read", h.MCPReadResource)
	e.GET(base+"/:id/mcp-apps/resource", h.GetMCPAppResource)
	e.POST(base+"/:id/mcp-apps/call", h.CallMCPAppTool)
	e.GET(base+"/agents/templates", h.AgentTemplates)
	e.POST(base+"/agents/templates/:id/create", h.CreateAgentFromTemplate)
	e.POST(base+"/agents/import", h.ImportAgent)
	e.GET(base+"/agents/:id/export", h.ExportAgent)
	e.GET(base+"/agents/:id/runs", h.AgentRuns)
	e.GET(base+"/agents", h.ListAgents)
	e.POST(base+"/agents", h.CreateAgent)
	e.GET(base+"/agents/:id", h.GetAgent)
	e.PUT(base+"/agents/:id", h.UpdateAgent)
	e.DELETE(base+"/agents/:id", h.DeleteAgent)
	e.POST(base, h.Create)
	e.GET(base, h.List)
	e.GET(base+"/search", h.Search)
	e.POST(base+"/import", h.Import)
	e.GET(base+"/:id", h.Get)
	e.PATCH(base+"/:id", h.Update)
	e.POST(base+"/:id/regenerate", h.Regenerate)
	e.POST(base+"/:id/messages/:seq/edit", h.EditMessage)
	e.POST(base+"/:id/fork", h.Fork)
	e.GET(base+"/:id/export", h.Export)
	e.GET(base+"/:id/files", h.ChatFiles)
	e.GET(base+"/:id/agents", h.SubAgents)
	e.GET(base+"/:id/agents/:agent_id/messages", h.SubAgentMessages)
	e.POST(base+"/:id/agents/:agent_id/stop", h.StopSubAgent)
	e.DELETE(base+"/:id", h.Delete)
	e.GET(base+"/:id/messages", h.Messages)
	e.POST(base+"/:id/messages", h.Send)
	e.PATCH(base+"/:id/pending/:pid", h.EditPending)
	e.DELETE(base+"/:id/pending/:pid", h.CancelPending)
	e.POST(base+"/:id/send-now", h.SendNow)
	e.POST(base+"/:id/stop", h.Stop)
	e.GET(base+"/:id/events", h.Events)
}
