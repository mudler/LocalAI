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
	e.GET(base+"/notifications", h.ListNotifications)
	e.DELETE(base+"/notifications", h.DeleteNotifications)
	e.GET(base+"/notifications/count", h.CountNotifications)
	e.POST(base+"/notifications/read", h.ReadNotifications)
	e.GET(base+"/notifications/events", h.NotificationEvents)
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
