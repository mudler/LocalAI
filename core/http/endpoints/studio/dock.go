package studio

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

// dockJSON is a dock as the API gives it.
func dockJSON(d *session.Dock) *schema.ChatsDock {
	if d == nil {
		return nil
	}
	out := &schema.ChatsDock{Open: d.Open, Active: d.Active, Width: d.Width, Tabs: make([]schema.ChatsDockTab, 0, len(d.Tabs))}
	for _, t := range d.Tabs {
		out.Tabs = append(out.Tabs, schema.ChatsDockTab{Page: t.Page, Folder: t.Folder})
	}
	return out
}

// GetDock returns the dock of a chat.
// @Summary Get a chat's dock.
// @Description The dock is the panel of pages that the interface shows beside the chat. It is stored with the chat, so every browser and every frontend shows the same one. The assistant opens a page of your home in it with its open_page tool.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsDockResponse "The dock, or null"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/dock [get]
func (h *Handlers) GetDock(c echo.Context) error {
	d, err := h.svc.Dock(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsDockResponse{Dock: dockJSON(d)})
}

// PutDock replaces the dock of a chat.
// @Summary Set a chat's dock.
// @Description Replaces the dock: its tabs (at most 6 pages, each once; folder is the id of a shared folder, absent for a page of your home), the index of the tab in front, whether the panel is open and its width in CSS pixels (0 for the default, else 320 to 4096). A dock with no tab clears it. The page names are not checked here: a page that is gone shows as missing in the interface, and a shared page still needs your consent before it runs. Watchers of the chat get a dock event. A sub-agent run has no dock (409).
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsDock true "the dock"
// @Success 200 {object} schema.ChatsDockResponse "The dock as stored, or null when it was cleared"
// @Failure 400 {object} schema.ErrorResponse "The dock is out of its bounds"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Failure 409 {object} schema.ErrorResponse "A sub-agent run has no dock"
// @Router /api/chats/{id}/dock [put]
func (h *Handlers) PutDock(c echo.Context) error {
	var req schema.ChatsDock
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, "the body must be JSON {open, tabs, active, width}")
	}
	in := session.Dock{Open: req.Open, Active: req.Active, Width: req.Width, Tabs: make([]session.DockTab, 0, len(req.Tabs))}
	for _, t := range req.Tabs {
		in.Tabs = append(in.Tabs, session.DockTab{Page: t.Page, Folder: t.Folder})
	}
	d, err := h.svc.SetDock(c.Request().Context(), userID(c), c.Param("id"), in)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsDockResponse{Dock: dockJSON(d)})
}
