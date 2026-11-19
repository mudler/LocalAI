// Package notifications serves the notification center: the inbox of a person
// (backend updates, finished operations, runs of triggers and connectors) and
// the browser push that tells them. It needs the notifier only, so it works
// with the chats on or off.
package notifications

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/sse"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/notify"
)

const (
	defaultNotifications = 50
	maxNotifications     = notify.MaxPerUser
	maxNotificationIDs   = 500
	heartbeat            = 15 * time.Second
)

// Handlers serves the routes of the notification center.
type Handlers struct {
	notifier *notify.Service
	pusher   *notify.Pusher
}

// New returns the handlers of a notifier. The pusher is nil when the server has
// nowhere to keep push subscriptions.
func New(n *notify.Service, p *notify.Pusher) *Handlers {
	return &Handlers{notifier: n, pusher: p}
}

// principal is the owner of the caller's rows: the user id, or the anonymous
// owner when authentication is off.
func principal(c echo.Context) string {
	if u := auth.GetUser(c); u != nil && u.ID != "" {
		return u.ID
	}
	return notify.AnonymousPrincipal
}

func fail(c echo.Context, status int, typ, msg string) error {
	return c.JSON(status, schema.ErrorResponse{Error: &schema.APIError{Message: msg, Code: status, Type: typ}})
}

func failStore(c echo.Context, err error) error {
	xlog.Error("notifications: request failed", "path", c.Request().URL.Path, "error", err)
	return fail(c, http.StatusInternalServerError, "server_error", "the request failed")
}

func intParam(c echo.Context, name string, def, max int) int {
	n, err := strconv.Atoi(c.QueryParam(name))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, max)
}

func notificationOut(n notify.Notification) schema.ChatsNotification {
	return schema.ChatsNotification{ID: n.ID, Kind: n.Kind, Status: n.Status, Source: n.Source, Title: n.Title, Body: n.Body, Link: n.Link, Read: n.Read, Created: n.Created, Code: n.Code, Params: n.Params}
}

// List lists the inbox.
// @Summary List your notifications.
// @Description The inbox: backend updates, finished operations, and one item for each run of a trigger that finished, failed or needs you. Newest first. It also returns the number unread and the total kept. Works with the chats on or off; the same routes also answer under /api/chats/notifications while the chats are on.
// @Tags notifications
// @Param unread query bool false "only the unread ones"
// @Param limit query int false "most items to return (default 50, at most 500)"
// @Success 200 {object} schema.ChatsNotificationList "The notifications"
// @Router /api/notifications [get]
// @Router /api/chats/notifications [get]
func (h *Handlers) List(c echo.Context) error {
	ctx, user := c.Request().Context(), principal(c)
	unreadOnly := c.QueryParam("unread") == "1" || c.QueryParam("unread") == "true"
	list, err := h.notifier.List(ctx, user, unreadOnly, intParam(c, "limit", defaultNotifications, maxNotifications))
	if err != nil {
		return failStore(c, err)
	}
	unread, err := h.notifier.Count(ctx, user)
	if err != nil {
		return failStore(c, err)
	}
	all, err := h.notifier.List(ctx, user, false, 0)
	if err != nil {
		return failStore(c, err)
	}
	out := schema.ChatsNotificationList{Notifications: make([]schema.ChatsNotification, 0, len(list)), Unread: unread, Total: len(all)}
	for _, n := range list {
		out.Notifications = append(out.Notifications, notificationOut(n))
	}
	return c.JSON(http.StatusOK, out)
}

// Count returns the unread count.
// @Summary Count unread notifications.
// @Tags notifications
// @Success 200 {object} schema.ChatsNotificationCount "The number unread"
// @Router /api/notifications/count [get]
// @Router /api/chats/notifications/count [get]
func (h *Handlers) Count(c echo.Context) error {
	n, err := h.notifier.Count(c.Request().Context(), principal(c))
	if err != nil {
		return failStore(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsNotificationCount{Unread: n})
}

// Read marks notifications read.
// @Summary Mark notifications read.
// @Description Name the ids, or pass all. Opening a chat marks its notifications read by itself.
// @Tags notifications
// @Param request body schema.ChatsNotificationsRead true "ids or all"
// @Success 200 {object} schema.ChatsNotificationCount "The number still unread"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/notifications/read [post]
// @Router /api/chats/notifications/read [post]
func (h *Handlers) Read(c echo.Context) error {
	var req schema.ChatsNotificationsRead
	if err := c.Bind(&req); err != nil || (!req.All && len(req.IDs) == 0) || len(req.IDs) > maxNotificationIDs {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "name up to 500 ids, or pass all")
	}
	ctx, user := c.Request().Context(), principal(c)
	var err error
	if req.All {
		err = h.notifier.MarkAllRead(ctx, user)
	} else {
		err = h.notifier.MarkRead(ctx, user, req.IDs)
	}
	if err != nil {
		return failStore(c, err)
	}
	return h.Count(c)
}

// Delete removes notifications.
// @Summary Delete notifications.
// @Description Name the ids, or pass all to empty your inbox; with all, read_only keeps the unread ones. Only your own notifications are touched.
// @Tags notifications
// @Param request body schema.ChatsNotificationsDelete true "ids, or all"
// @Success 200 {object} schema.ChatsNotificationCount "The number still unread and how many were deleted"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/notifications [delete]
// @Router /api/chats/notifications [delete]
func (h *Handlers) Delete(c echo.Context) error {
	var req schema.ChatsNotificationsDelete
	if err := c.Bind(&req); err != nil || len(req.IDs) > maxNotificationIDs || (req.All == (len(req.IDs) > 0)) || (req.ReadOnly && !req.All) {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "name 1 to 500 ids, or pass all")
	}
	ctx, user := c.Request().Context(), principal(c)
	deleted := len(req.IDs)
	if req.All {
		n, err := h.notifier.DeleteAll(ctx, user, req.ReadOnly)
		if err != nil {
			return failStore(c, err)
		}
		deleted = n
	} else if err := h.notifier.Delete(ctx, user, req.IDs); err != nil {
		return failStore(c, err)
	}
	unread, err := h.notifier.Count(ctx, user)
	if err != nil {
		return failStore(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsNotificationCount{Unread: unread, Deleted: deleted})
}

// Events streams new notifications.
// @Summary Stream your notifications.
// @Description Server-sent events: one notification event for each new inbox item, with the same fields as the list, and a notifications_changed event when notifications were read or deleted, so a client reads the list again. Live only; list the inbox for what came before. In a cluster the events of every frontend reach every stream.
// @Tags notifications
// @Produce text/event-stream
// @Success 200 {object} schema.ChatsNotification "An event stream"
// @Router /api/notifications/events [get]
// @Router /api/chats/notifications/events [get]
func (h *Handlers) Events(c echo.Context) error {
	ch, cancel := h.notifier.Subscribe(principal(c))
	defer cancel()
	return sse.Events(c, ch, heartbeat, func(n notify.Notification) (string, any) {
		if n.Op == notify.OpChanged {
			return "notifications_changed", struct{}{}
		}
		return "notification", notificationOut(n)
	})
}

var errPushOff = errors.New("browser push is not available: the server needs a database or a data path to keep subscriptions")

func (h *Handlers) pushOn(c echo.Context) bool {
	if h.pusher == nil {
		_ = fail(c, http.StatusConflict, "push_off", errPushOff.Error())
		return false
	}
	return true
}

// PushKey returns the public key for browser push.
// @Summary Get the browser push key.
// @Description The VAPID public key a browser needs to subscribe to push for your notifications. 409 when the server cannot keep subscriptions (no database and no data path).
// @Tags notifications
// @Success 200 {object} schema.ChatsPushKey "The key"
// @Router /api/notifications/push/key [get]
// @Router /api/chats/push/key [get]
func (h *Handlers) PushKey(c echo.Context) error {
	if !h.pushOn(c) {
		return nil
	}
	key, err := h.pusher.PublicKey(c.Request().Context())
	if err != nil {
		return failStore(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsPushKey{PublicKey: key})
}

// PushSubscribe stores a browser's push subscription.
// @Summary Subscribe a browser to push.
// @Description Pass the endpoint and keys of a browser PushSubscription. A notification is then pushed to that browser with its title and link only. The endpoint must be an https address on the public internet. At most 10 browsers per user; the oldest go first.
// @Tags notifications
// @Param request body schema.ChatsPushSubscription true "the browser's subscription"
// @Success 204 "Subscribed"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/notifications/push/subscribe [post]
// @Router /api/chats/push/subscribe [post]
func (h *Handlers) PushSubscribe(c echo.Context) error {
	if !h.pushOn(c) {
		return nil
	}
	var req schema.ChatsPushSubscription
	if err := c.Bind(&req); err != nil {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "the body is not JSON")
	}
	if err := h.pusher.Subscribe(c.Request().Context(), principal(c), req.Endpoint, req.Keys.P256dh, req.Keys.Auth); err != nil {
		if errors.Is(err, notify.ErrPushEndpoint) {
			return fail(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		}
		return failStore(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// PushUnsubscribe removes a browser's push subscription.
// @Summary Unsubscribe a browser from push.
// @Tags notifications
// @Param request body schema.ChatsPushUnsubscribe true "the endpoint"
// @Success 204 "Unsubscribed"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/notifications/push/subscribe [delete]
// @Router /api/chats/push/subscribe [delete]
func (h *Handlers) PushUnsubscribe(c echo.Context) error {
	if !h.pushOn(c) {
		return nil
	}
	var req schema.ChatsPushUnsubscribe
	if err := c.Bind(&req); err != nil || req.Endpoint == "" {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "endpoint is required")
	}
	if err := h.pusher.Unsubscribe(c.Request().Context(), principal(c), req.Endpoint); err != nil {
		return failStore(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
