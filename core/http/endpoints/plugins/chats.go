package plugins

import (
	"errors"
	"net/http"
	"slices"

	"github.com/labstack/echo/v4"
)

// MaxChatPluginsOff bounds the switched-off list of a chat, as for skills.
const MaxChatPluginsOff = 200

// maxInstallIDLen bounds one install ID of the list.
const maxInstallIDLen = 128

// ErrChatNotFound is what a ChatSwitch returns for a chat the caller does
// not have; the route answers 404. The user of SetPluginsOff is the
// principal of the chats: the user ID, or the anonymous principal of a
// server without authentication.
var ErrChatNotFound = errors.New("chat not found")

// SetChatPlugins sets the plugins switched off in one chat.
// @Summary Switch plugins off in a chat
// @Description Sets the installs switched off in one chat, replacing the list. A chat keeps what is off, so a plugin turned on later is on in existing chats. At most 200 install IDs. Needs the chats and the plugins features.
// @Tags Plugins
// @Accept json
// @Produce json
// @Param id path string true "Chat ID"
// @Param request body ChatPluginsRequest true "Install IDs switched off"
// @Success 200 {object} ChatPluginsResponse "The list as stored"
// @Failure 400 {object} schema.ErrorResponse "Invalid list"
// @Failure 404 {object} schema.ErrorResponse "No such chat"
// @Failure 501 {object} schema.ErrorResponse "The chats of this server cannot keep plugin switches"
// @Router /api/chats/{id}/plugins [put]
func (h *Handlers) SetChatPlugins(c echo.Context) error {
	a, ok := h.actor(c)
	if !ok {
		return nil
	}
	var req ChatPluginsRequest
	if err := decode(c, &req); err != nil {
		return badBody(c, err)
	}
	off := make([]string, 0, len(req.Off))
	for _, id := range req.Off {
		if id == "" || len(id) > maxInstallIDLen {
			return apiError(c, http.StatusBadRequest, ErrTypeInvalid, "each entry must be an install ID")
		}
		if !slices.Contains(off, id) {
			off = append(off, id)
		}
	}
	if len(off) > MaxChatPluginsOff {
		return apiError(c, http.StatusBadRequest, ErrTypeInvalid, "at most 200 install IDs")
	}
	if h.d.Chats == nil {
		return apiError(c, http.StatusNotImplemented, ErrTypeChatsUnsupported, "The chats of this server cannot keep plugin switches.")
	}
	if err := h.d.Chats.SetPluginsOff(c.Request().Context(), a.ID, c.Param("id"), off); err != nil {
		if errors.Is(err, ErrChatNotFound) {
			return apiError(c, http.StatusNotFound, ErrTypeNotFound, "chat not found")
		}
		return fail(c, err)
	}
	return c.JSON(http.StatusOK, ChatPluginsResponse{Off: off})
}
