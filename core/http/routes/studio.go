package routes

import (
	"context"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	studioendpoint "github.com/mudler/LocalAI/core/http/endpoints/studio"
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

// RegisterStudioRoutes registers the chats API. It does nothing without
// a service. Authentication and the chats feature gate come from the global
// middleware and the route-feature registry, so no route carries its own.
func RegisterStudioRoutes(e *echo.Echo, svc *studiosvc.Service, models studioendpoint.ModelChecker) {
	if svc == nil {
		return
	}
	h := studioendpoint.New(svc, models)
	const base = "/api/chats"
	// The files routes come before the "/:id" ones; "files" is not a chat id.
	e.GET(base+"/files", h.ListFiles)
	e.DELETE(base+"/files", h.DeleteFile)
	e.GET(base+"/files/content", h.GetFileContent)
	e.PUT(base+"/files/content", h.PutFileContent)
	e.POST(base+"/files/folders", h.CreateFolder)
	e.POST(base+"/files/move", h.MoveFile)
	e.GET(base+"/files/trash", h.Trash)
	e.POST(base+"/files/undo", h.UndoFiles)
	e.GET(base+"/files/history", h.FileHistory)
	e.GET(base+"/skills", h.ListSkills)
	e.POST(base+"/skills", h.CreateSkill)
	e.GET(base+"/skills/:layer/:name", h.GetSkill)
	e.PUT(base+"/skills/:layer/:name", h.UpdateSkill)
	e.DELETE(base+"/skills/:layer/:name", h.DeleteSkill)
	e.PUT(base+"/:id/skills", h.SetChatSkills)
	e.GET(base+"/:id/addons", h.ListChatAddons)
	e.PUT(base+"/:id/addons", h.SetChatAddons)
	e.GET(base+"/files/search", h.SearchFiles)
	e.GET(base+"/conversion", h.Conversion)
	e.POST(base+"/import", h.ImportBrowserChats)
	e.GET(base+"/notifications", h.ListNotifications)
	e.DELETE(base+"/notifications", h.DeleteNotifications)
	e.GET(base+"/notifications/count", h.CountNotifications)
	e.POST(base+"/notifications/read", h.ReadNotifications)
	e.GET(base+"/notifications/events", h.NotificationEvents)
	e.GET(base+"/triggers", h.ListTriggers)
	e.POST(base+"/triggers", h.CreateTrigger)
	e.GET(base+"/triggers/preview", h.PreviewTrigger)
	e.GET(base+"/triggers/proposals", h.ListTriggerProposals)
	e.POST(base+"/triggers/proposals/:id/confirm", h.ConfirmTriggerProposal)
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
	e.GET(base+"/:id/mcp-apps/resource", h.GetMCPAppResource)
	e.POST(base+"/:id/mcp-apps/call", h.CallMCPAppTool)
	e.GET(base+"/agents", h.ListAgents)
	e.POST(base+"/agents", h.CreateAgent)
	e.GET(base+"/agents/:id", h.GetAgent)
	e.PUT(base+"/agents/:id", h.UpdateAgent)
	e.DELETE(base+"/agents/:id", h.DeleteAgent)
	e.POST(base, h.Create)
	e.GET(base, h.List)
	e.GET(base+"/:id", h.Get)
	e.DELETE(base+"/:id", h.Delete)
	e.GET(base+"/:id/messages", h.Messages)
	e.POST(base+"/:id/messages", h.Send)
	e.PATCH(base+"/:id/pending/:pid", h.EditPending)
	e.DELETE(base+"/:id/pending/:pid", h.CancelPending)
	e.POST(base+"/:id/send-now", h.SendNow)
	e.POST(base+"/:id/stop", h.Stop)
	e.GET(base+"/:id/events", h.Events)
}
