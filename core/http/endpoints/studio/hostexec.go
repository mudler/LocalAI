package studio

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
)

// HostExecConsent answers the card that asks to run one command on this
// machine.
// @Summary Allow or refuse one command of the built-in bash on this machine.
// @Description When chats_host_exec is ask, each command of the chat's built-in bash waits for an answer: a host_exec_consent event carries the id and the command. once runs it (administrators only); deny refuses it (anyone who owns the chat). The command runs with no isolation, as the user LocalAI runs as, with the network of the server. Without an answer the call ends after 5 minutes.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsHostExecConsent true "the id of the event and the answer"
// @Success 204 "The answer was sent to the running turn"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Failure 403 {object} schema.ErrorResponse "Only an administrator can allow a command"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/host-exec/consent [post]
func (h *Handlers) HostExecConsent(c echo.Context) error {
	var in schema.ChatsHostExecConsent
	if err := c.Bind(&in); err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, "the body must be {id, decision}")
	}
	if err := h.svc.HostExecDecide(c.Request().Context(), userID(c), c.Param("id"), in.ID, in.Decision); err != nil {
		return failFor(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// AdminHostExecEvents returns the audit of the commands the built-in bash ran.
// @Summary Read the audit of the commands run on this machine (administrators).
// @Description Every command the chats' built-in bash ran directly on this machine: who, in which chat, the command (cut at 4 KiB), the exit code (-1 when it did not exit by itself), how long it took and whether it timed out. The commands had the network of the server, so this is also the record of what they could reach.
// @Tags chats
// @Param limit query int false "How many lines, newest first (default 100, at most 500)"
// @Success 200 {object} schema.ChatsHostExecEventList "The newest commands first"
// @Router /api/chats/admin/host-exec/events [get]
func (h *Handlers) AdminHostExecEvents(c echo.Context) error {
	ctx := c.Request().Context()
	evs, err := h.svc.HostExecEvents(ctx, intParam(c, "limit", 100, 500))
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsHostExecEventList{Events: make([]schema.ChatsHostExecEvent, 0, len(evs))}
	for _, e := range evs {
		out.Events = append(out.Events, schema.ChatsHostExecEvent{User: e.User, UserName: h.svc.UserName(ctx, e.User), Chat: e.Chat, Command: e.Command,
			Exit: e.Exit, DurationMS: e.DurationMS, TimedOut: e.TimedOut, At: e.At})
	}
	return c.JSON(http.StatusOK, out)
}

// AdminBuiltinTools lists the built-in chat tools for Settings > Tools.
// @Summary List the built-in chat tools (administrators).
// @Description The built-in tools of the chats (bash, web_search, web_fetch, as this server registers them): label, the per-chat switch group, the runtime setting that switches each off and the ones that configure it, whether it is on, whether it only reads and whether an add-on may replace it, who serves it now (builtin, an add-on's ID, or empty), the add-ons that replace it (oldest install first, with their install times in installed_at), the default one that serves with no pin (default: the add-on installed first) and the one pinned with chats_tool_providers, and for bash the mode of chats_host_exec and why this server cannot run it, if so. skills lists the built-in skills (xlsx, docx, pptx, pdf) that an installed add-on replaces: who serves each now, the add-ons that replace it (the same order, default and installed_at) and the one pinned in the same chats_tool_providers map. Change them with POST /api/settings.
// @Tags chats
// @Success 200 {object} schema.ChatsBuiltinToolList "The built-in tools, by ID"
// @Router /api/chats/admin/tools [get]
func (h *Handlers) AdminBuiltinTools(c echo.Context) error {
	list, err := h.svc.AdminBuiltinTools(c.Request().Context(), userID(c))
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsBuiltinToolList{Tools: make([]schema.ChatsBuiltinTool, 0, len(list))}
	for _, t := range list {
		cands := t.Candidates
		if cands == nil {
			cands = []string{}
		}
		opts := t.Options
		if opts == nil {
			opts = []string{}
		}
		out.Tools = append(out.Tools, schema.ChatsBuiltinTool{ID: t.ID, Label: t.Label, Description: t.Description, Group: t.Group, Setting: t.Setting,
			Options: opts, ReadOnly: t.ReadOnly, Replaceable: t.Replaceable, Enabled: t.Enabled, Provider: t.Provider, Candidates: cands, Default: t.Default, Pinned: t.Pinned, InstalledAt: t.InstalledAt, Mode: t.Mode, Unavailable: t.Unavailable})
	}
	out.Skills = []schema.ChatsBuiltinSkill{}
	for _, sk := range h.svc.AdminBuiltinSkills(c.Request().Context(), userID(c)) {
		out.Skills = append(out.Skills, schema.ChatsBuiltinSkill{ID: sk.ID, Provider: sk.Provider, Candidates: sk.Candidates, Default: sk.Default, Titles: sk.Titles, Pinned: sk.Pinned, InstalledAt: sk.InstalledAt, Note: sk.Note})
	}
	return c.JSON(http.StatusOK, out)
}
