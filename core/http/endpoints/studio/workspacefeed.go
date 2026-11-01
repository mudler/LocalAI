package studio

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/wsfeed"
)

// feedHeartbeat keeps idle proxies from closing the change feed.
const feedHeartbeat = 25 * time.Second

// WorkspaceEvents streams the changes to the user's files.
// @Summary Stream the changes to your files.
// @Description Server-sent events: a "change" event when files in your home folder change, whoever changed them (you in another tab, a chat, a trigger, a connector, an API client, on any frontend), with the paths inside your home and the revision as the event id. Changes that arrive close together come as one event. A "resync" event means changes were left out (the client fell behind, or the resume point is too far back): read again what you show. Send the last id back as the Last-Event-ID header or the after query parameter to resume; without it the stream starts from now. A comment line is sent every 25 seconds. At most 8 streams per user.
// @Tags workspace
// @Produce text/event-stream
// @Param after query int false "resume after this revision"
// @Param Last-Event-ID header int false "resume after this revision (sent by a browser when it reconnects)"
// @Success 200 {object} schema.WorkspaceChange "An event stream"
// @Failure 400 {object} schema.ErrorResponse "The resume point is not a number"
// @Failure 409 {object} schema.ErrorResponse "Files are off on this server"
// @Failure 429 {object} schema.ErrorResponse "Too many streams for this user"
// @Router /api/workspace/events [get]
func (h *Handlers) WorkspaceEvents(c echo.Context) error {
	if _, ok := h.files(c); !ok {
		return nil
	}
	after, resume, err := resumePoint(c)
	if err != nil {
		return fail(c, http.StatusBadRequest, typeInvalid, "the resume point must be a revision number")
	}
	ch, cancel, err := h.svc.SubscribeWorkspace(c.Request().Context(), userID(c), after, resume)
	if errors.Is(err, wsfeed.ErrTooManySubscribers) {
		return fail(c, http.StatusTooManyRequests, "rate_limit_error", "too many open change streams; close one first")
	}
	if err != nil {
		return failFile(c, err)
	}
	defer cancel()
	return streamSSEWithIDs(c, ch, feedHeartbeat, func(ev wsfeed.Event) (string, string, any) {
		id := strconv.FormatInt(ev.Revision, 10)
		if ev.Resync {
			return id, "resync", schema.WorkspaceChange{Revision: ev.Revision}
		}
		return id, "change", schema.WorkspaceChange{Revision: ev.Revision, Paths: ev.Paths, Kinds: ev.Kinds}
	})
}

// resumePoint reads the revision to resume after: the Last-Event-ID header a
// browser sends when it reconnects by itself, else the after parameter, which
// a client sets when it opens a new stream.
func resumePoint(c echo.Context) (int64, bool, error) {
	v := c.Request().Header.Get("Last-Event-ID")
	if v == "" {
		v = c.QueryParam("after")
	}
	if v == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, false, errors.New("bad revision")
	}
	return n, true, nil
}
