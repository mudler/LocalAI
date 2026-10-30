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
	cmds, err := h.svc.CommandRunner(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsAddonList{Addons: make([]schema.ChatsAddon, 0, len(list)), Commands: string(cmds.State), CommandsReason: cmds.Reason,
		CommandsIsolation: cmds.Isolation, CommandsProvider: cmds.Provider, CommandsConsent: cmds.Consent,
		CommandsFallback: cmds.Fallback, CommandsFallbackReason: cmds.FallbackReason}
	for _, a := range list {
		out.Addons = append(out.Addons, schema.ChatsAddon{ID: a.ID, Tools: a.Tools, Off: a.Off})
	}
	return c.JSON(http.StatusOK, out)
}

// ListChatAddons lists the add-ons a chat can use.
// @Summary List the add-ons a chat can use.
// @Description The add-ons whose tools the signed-in user may use now, each with the chat's switch, and whether the chat can run commands at its next turn (commands: available, off, unavailable or none). When commands is available, commands_provider says who serves bash (builtin: directly on this machine with no isolation and with its network, or the add-on's ID) and commands_isolation says isolated or host; commands_consent is ask when each command waits for a yes in the chat, and commands_fallback names an add-on that would serve but does not (commands_fallback_reason: off, or why it cannot run). Without the add-on host the list is empty.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsAddonList "The add-ons and their switches"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/addons [get]
func (h *Handlers) ListChatAddons(c echo.Context) error { return addonList(c, h) }

// SetChatAddons sets the add-ons a chat has switched off.
// @Summary Switch the tools of add-ons on and off for a chat.
// @Description The list replaces the earlier one. The next turn of the chat gets the tools of the add-ons that are on.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsAddonSwitches true "the IDs of the add-ons that are off"
// @Success 200 {object} schema.ChatsAddonList "The add-ons and their switches now in force"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/addons [put]
func (h *Handlers) SetChatAddons(c echo.Context) error {
	req, ok := bind[schema.ChatsAddonSwitches](c, "off is required")
	if !ok {
		return nil
	}
	if err := h.svc.SetAddonsOff(c.Request().Context(), userID(c), c.Param("id"), req.Off); err != nil {
		return failFor(c, err)
	}
	return addonList(c, h)
}
