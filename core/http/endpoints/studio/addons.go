package studio

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
)

func addonList(c echo.Context, h *Handlers) error {
	list, err := h.svc.Addons(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsAddonList{Addons: make([]schema.ChatsAddon, 0, len(list))}
	for _, a := range list {
		out.Addons = append(out.Addons, schema.ChatsAddon{ID: a.ID, Tools: a.Tools, Off: a.Off})
	}
	return c.JSON(http.StatusOK, out)
}

// ListChatAddons lists the add-ons a chat can use.
// @Summary List the add-ons a chat can use.
// @Description The add-ons whose tools the signed-in user may use now, each with the chat's switch. Without the add-on host the list is empty.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsAddonList "The add-ons and their switches"
// @Router /api/chats/{id}/addons [get]
func (h *Handlers) ListChatAddons(c echo.Context) error { return addonList(c, h) }

// SetChatAddons sets the add-ons a chat has switched off.
// @Summary Switch the tools of add-ons on and off for a chat.
// @Description The list replaces the earlier one. The next turn of the chat gets the tools of the add-ons that are on.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsAddonSwitches true "the IDs of the add-ons that are off"
// @Success 200 {object} schema.ChatsAddonList "The add-ons and their switches now in force"
// @Router /api/chats/{id}/addons [put]
func (h *Handlers) SetChatAddons(c echo.Context) error {
	var req schema.ChatsAddonSwitches
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "off is required")
	}
	if err := h.svc.SetAddonsOff(c.Request().Context(), userID(c), c.Param("id"), req.Off); err != nil {
		return failFor(c, err)
	}
	return addonList(c, h)
}
