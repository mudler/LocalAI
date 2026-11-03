package studio

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
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
	return writeAddonList(c, list, cmds)
}

func writeAddonList(c echo.Context, list []studiosvc.AddonInfo, cmds studiosvc.Commands) error {
	out := schema.ChatsAddonList{Addons: make([]schema.ChatsAddon, 0, len(list)), Commands: string(cmds.State), CommandsReason: cmds.Reason,
		CommandsIsolation: cmds.Isolation, CommandsProvider: cmds.Provider, CommandsConsent: cmds.Consent,
		CommandsFallback: cmds.Fallback, CommandsFallbackReason: cmds.FallbackReason}
	if v := cmds.ExecutionStatus; v != nil {
		out.ExecutionStatus = &schema.ChatsExecutionStatus{Readiness: v.Readiness, Isolation: v.Isolation, Explanation: v.Explanation, UnknownReason: v.UnknownReason, Generation: v.Generation, Incarnation: v.Incarnation}
	}
	for _, a := range list {
		out.Addons = append(out.Addons, addonOut(a))
	}
	return c.JSON(http.StatusOK, out)
}

// addonOut is one add-on row with its switch and, for an add-on that runs
// with a credential, the connections and the ones this chat may use.
func addonOut(a studiosvc.AddonInfo) schema.ChatsAddon {
	row := schema.ChatsAddon{ID: a.ID, Tools: a.Tools, Off: a.Off, Title: a.Title, Description: a.Description, Credentialed: a.Credentialed,
		Enabled: a.Enabled.IDs(), ConnectionBy: a.Enabled.By, NeedsConnection: a.Credentialed && len(a.Enabled.Items) == 0}
	if len(row.Enabled) == 0 {
		row.Enabled = nil
	}
	for _, x := range a.Connections {
		row.Connections = append(row.Connections, schema.ChatsConnection{ID: x.ID, Name: x.Name, Handle: x.Handle})
	}
	return row
}

// ListChatAddons lists the add-ons a chat can use.
// @Summary List the add-ons a chat can use.
// @Description The add-ons whose tools the signed-in user may use now, each with the chat's switch, and whether the chat can run commands at its next turn (commands: available, off, unavailable, none, or switched_off when the chat switched the built-in group shell off). When commands is available, commands_provider says who serves bash (builtin: directly on this machine with no isolation and with its network, or the add-on's ID) and commands_isolation says isolated or host; commands_consent is ask when each command waits for a yes in the chat, and commands_fallback names an add-on that would serve but does not (commands_fallback_reason: off, or why it cannot run). Without the add-on host the list is empty.
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

// SetChatConnection sets the connections a chat may use for an add-on.
// @Summary Choose the connections a chat may use for an add-on.
// @Description For an add-on whose tools run with the token of one of your connections, the host never picks connections for you. With exactly one it is enabled; with several, choose the set here, and until then the tools of the add-on are not offered to the model. With two or more enabled, the model names the connection in every tool call by a generic connection argument; it can only name an enabled one. An empty list clears the choice. Choosing connections switches the add-on on for the chat. If the saved agent of the chat names the set, the answer is 400: the agent decides. A request with the older single connection field is read as a set of one.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsConnectionSwitch true "the add-on and the connections"
// @Success 200 {object} schema.ChatsAddonList "The add-ons and their switches now in force"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid, or the agent names the connection"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/connections [put]
func (h *Handlers) SetChatConnection(c echo.Context) error {
	req, ok := bind[schema.ChatsConnectionSwitch](c, "addon is required")
	if !ok {
		return nil
	}
	ids := req.Connections
	if len(ids) == 0 && req.Connection != "" {
		ids = []string{req.Connection}
	}
	if err := h.svc.SetConnections(c.Request().Context(), userID(c), c.Param("id"), req.Addon, ids); err != nil {
		return failFor(c, err)
	}
	return addonList(c, h)
}
