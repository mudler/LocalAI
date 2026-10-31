package studio

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/egress/audit"
)

// GetEgress returns the network of a chat's Sandbox commands.
// @Summary Read the network of a chat's Sandbox commands.
// @Description The preset of the chat (off, registries, all), the administrator's ceiling, the preset in force (the lower of the two), whether it applies (false when commands run on this machine, applies_reason host, or no Sandbox serves bash, none), the package registries the preset registries admits, the chat's allow and deny rules (above_limit when the ceiling no longer admits one) and the cards that asked. A sub-agent chat shows its parent's. Another user's chat is 404.
// @Tags chats
// @Param id path string true "conversation id"
// @Success 200 {object} schema.ChatsEgress "The network of the chat"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/egress [get]
func (h *Handlers) GetEgress(c echo.Context) error {
	v, err := h.svc.Egress(c.Request().Context(), userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, v.JSON())
}

// PutEgress sets the preset of a chat.
// @Summary Set the network preset of a chat's Sandbox commands.
// @Description off reaches nothing; registries the HTTPS hosts of the package registries; all every public host. A preset above the administrator's ceiling (chats_egress_ceiling) is 400 egress_above_ceiling. A sub-agent follows its parent: 409. It applies at the next connection of a command.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsEgressPreset true "the preset"
// @Success 200 {object} schema.ChatsEgress "The network of the chat"
// @Failure 400 {object} schema.ErrorResponse "Not a preset, or above the ceiling"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or it is another user's"
// @Router /api/chats/{id}/egress [put]
func (h *Handlers) PutEgress(c echo.Context) error {
	var in schema.ChatsEgressPreset
	if err := c.Bind(&in); err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, "the body must be {preset}")
	}
	ctx := c.Request().Context()
	if err := h.svc.SetEgressPreset(ctx, userID(c), c.Param("id"), egress.Preset(in.Preset)); err != nil {
		return failFor(c, err)
	}
	v, err := h.svc.Egress(ctx, userID(c), c.Param("id"))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, v.JSON())
}

// DeleteEgressRule removes an allow or deny rule of a chat.
// @Summary Remove a network rule of a chat.
// @Description Removes the rule that allows or denies one host and port for the chat's Sandbox commands. A host that was denied asks again; one that was allowed follows the preset again.
// @Tags chats
// @Param id path string true "conversation id"
// @Param host path string true "host name"
// @Param port path int true "port"
// @Success 204 "Removed"
// @Failure 404 {object} schema.ErrorResponse "No such conversation, or no such rule"
// @Router /api/chats/{id}/egress/rules/{host}/{port} [delete]
func (h *Handlers) DeleteEgressRule(c echo.Context) error {
	port, err := strconv.Atoi(c.Param("port"))
	if err != nil || port < 1 || port > 65535 {
		return fail(c, http.StatusBadRequest, typeInvalid, "the port must be 1 to 65535")
	}
	if err := h.svc.DeleteEgressRule(c.Request().Context(), userID(c), c.Param("id"), c.Param("host"), port); err != nil {
		return failFor(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// AdminEgress returns the administrator's view of the Sandbox network.
// @Summary Read the settings of the Sandbox network (administrators).
// @Description The chats_egress_* settings in force (ceiling, default, allow_private, ports, extra_registries, allowed_hosts, audit_days), the package registries with the extra hosts, the add-on that runs commands and how (isolated or host; the settings do not apply to commands on this machine), and the allowed and blocked requests of the last 24 hours. Change the settings with POST /api/settings.
// @Tags chats
// @Success 200 {object} schema.ChatsEgressAdmin "The settings in force"
// @Router /api/chats/admin/egress [get]
func (h *Handlers) AdminEgress(c echo.Context) error {
	v, err := h.svc.AdminEgress(c.Request().Context(), userID(c))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, v.JSON())
}

// AdminEgressEvents returns the audit of the Sandbox network.
// @Summary Read the audit of the Sandbox network requests (administrators).
// @Description Every request a command made through the Sandbox proxy, newest first: who, which chat, host, port, kind (connect or http), verdict (allow, block, or done when an allowed connection ended, with its address, bytes and duration) and reason.
// @Tags chats
// @Param limit query int false "How many lines (default 100, at most 500)"
// @Param user query string false "Only this user"
// @Param chat query string false "Only this chat"
// @Param verdict query string false "allow, block or done"
// @Param host query string false "Only this host"
// @Success 200 {object} schema.ChatsEgressEventList "The newest lines first"
// @Router /api/chats/admin/egress/events [get]
func (h *Handlers) AdminEgressEvents(c echo.Context) error {
	ctx := c.Request().Context()
	evs, err := h.svc.EgressEvents(ctx, audit.Query{Limit: intParam(c, "limit", 100, audit.MaxPage), User: c.QueryParam("user"),
		Chat: c.QueryParam("chat"), Verdict: c.QueryParam("verdict"), Host: c.QueryParam("host")})
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, h.svc.EgressEventsJSON(ctx, evs))
}

// EgressConsent answers a card that asks whether a command may reach a host.
// @Summary Answer a network card of a chat's Sandbox commands.
// @Description A command asked for a host the chat does not allow but the administrator's ceiling would: an egress_consent event carried the card id and host:port, and the card is in GET /api/chats/{id}/egress. once allows the host for the rest of that command (or the next call of the chat within ten minutes when the answer comes late), chat stores an allow rule for the chat, deny stores a deny rule so the host does not ask again. A host inside your network (private) needs an administrator to allow it; anyone who owns the chat may deny. A card that expired at the end of its turn may still be answered. The waiting connection goes on at once; without an answer it gets a 403 after 60 seconds.
// @Tags chats
// @Param id path string true "conversation id"
// @Param request body schema.ChatsEgressConsent true "the card id and the answer"
// @Success 204 "The answer was stored and sent to the waiting command"
// @Failure 400 {object} schema.ErrorResponse "Not an answer, or the ceiling does not admit the host (egress_above_ceiling; the card is refused, and every later answer gets the same reason)"
// @Failure 403 {object} schema.ErrorResponse "Only an administrator can allow a host inside your network"
// @Failure 404 {object} schema.ErrorResponse "No such conversation or card"
// @Failure 409 {object} schema.ErrorResponse "The card was answered already with another answer (already_answered); the same answer again is 204"
// @Router /api/chats/{id}/egress/consent [post]
func (h *Handlers) EgressConsent(c echo.Context) error {
	var in schema.ChatsEgressConsent
	if err := c.Bind(&in); err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, "the body must be {id, decision}")
	}
	if err := h.svc.EgressDecide(c.Request().Context(), userID(c), c.Param("id"), in.ID, in.Decision); err != nil {
		return failFor(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
