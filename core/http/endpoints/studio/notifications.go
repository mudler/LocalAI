package studio

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studio/notify"
)

const (
	defaultNotifications = 50
	maxNotifications     = notify.MaxPerUser
	maxNotificationIDs   = 500
)

func notificationOut(n notify.Notification) schema.ChatsNotification {
	return schema.ChatsNotification{ID: n.ID, Kind: n.Kind, Status: n.Status, Title: n.Title, Body: n.Body, Link: n.Link, Read: n.Read, Created: n.Created}
}

// ListNotifications lists the inbox.
// @Summary List your notifications.
// @Description The inbox: one item for each run of a trigger that finished, failed or needs you. Newest first. It also returns the number unread and the total kept.
// @Tags chats
// @Param unread query bool false "only the unread ones"
// @Param limit query int false "most items to return (default 50, at most 500)"
// @Success 200 {object} schema.ChatsNotificationList "The notifications"
// @Router /api/chats/notifications [get]
func (h *Handlers) ListNotifications(c echo.Context) error {
	user := userID(c)
	list, err := h.svc.Notifications(c.Request().Context(), user, c.QueryParam("unread") == "1" || c.QueryParam("unread") == "true",
		intParam(c, "limit", defaultNotifications, maxNotifications))
	if err != nil {
		return failFor(c, err)
	}
	unread, err := h.svc.UnreadNotifications(c.Request().Context(), user)
	if err != nil {
		return failFor(c, err)
	}
	total, err := h.svc.TotalNotifications(c.Request().Context(), user)
	if err != nil {
		return failFor(c, err)
	}
	out := schema.ChatsNotificationList{Notifications: make([]schema.ChatsNotification, 0, len(list)), Unread: unread, Total: total}
	for _, n := range list {
		out.Notifications = append(out.Notifications, notificationOut(n))
	}
	return c.JSON(http.StatusOK, out)
}

// CountNotifications returns the unread count.
// @Summary Count unread notifications.
// @Tags chats
// @Success 200 {object} schema.ChatsNotificationCount "The number unread"
// @Router /api/chats/notifications/count [get]
func (h *Handlers) CountNotifications(c echo.Context) error {
	n, err := h.svc.UnreadNotifications(c.Request().Context(), userID(c))
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsNotificationCount{Unread: n})
}

// ReadNotifications marks notifications read.
// @Summary Mark notifications read.
// @Description Name the ids, or pass all. Opening a chat marks its notifications read by itself.
// @Tags chats
// @Param request body schema.ChatsNotificationsRead true "ids or all"
// @Success 200 {object} schema.ChatsNotificationCount "The number still unread"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/notifications/read [post]
func (h *Handlers) ReadNotifications(c echo.Context) error {
	var req schema.ChatsNotificationsRead
	if err := c.Bind(&req); err != nil || (!req.All && len(req.IDs) == 0) || len(req.IDs) > maxNotificationIDs {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "name up to 500 ids, or pass all")
	}
	user := userID(c)
	if err := h.svc.MarkNotificationsRead(c.Request().Context(), user, req.IDs, req.All); err != nil {
		return failFor(c, err)
	}
	return h.CountNotifications(c)
}

// DeleteNotifications removes notifications.
// @Summary Delete notifications.
// @Description Name the ids, or pass all to empty your inbox; with all, read_only keeps the unread ones. Only your own notifications are touched.
// @Tags chats
// @Param request body schema.ChatsNotificationsDelete true "ids, or all"
// @Success 200 {object} schema.ChatsNotificationCount "The number still unread and how many were deleted"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/notifications [delete]
func (h *Handlers) DeleteNotifications(c echo.Context) error {
	var req schema.ChatsNotificationsDelete
	if err := c.Bind(&req); err != nil || len(req.IDs) > maxNotificationIDs || (req.All == (len(req.IDs) > 0)) || (req.ReadOnly && !req.All) {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "name 1 to 500 ids, or pass all")
	}
	ctx, user := c.Request().Context(), userID(c)
	deleted := len(req.IDs)
	if req.All {
		n, err := h.svc.ClearNotifications(ctx, user, req.ReadOnly)
		if err != nil {
			return failFor(c, err)
		}
		deleted = n
	} else if err := h.svc.DeleteNotifications(ctx, user, req.IDs); err != nil {
		return failFor(c, err)
	}
	unread, err := h.svc.UnreadNotifications(ctx, user)
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsNotificationCount{Unread: unread, Deleted: deleted})
}

// NotificationEvents streams new notifications.
// @Summary Stream your notifications.
// @Description Server-sent events: one notification event for each new inbox item, with the same fields as the list, and a notifications_changed event when notifications were read or deleted, so a client reads the list again. Live only; list the inbox for what came before.
// @Tags chats
// @Produce text/event-stream
// @Success 200 {object} schema.ChatsNotification "An event stream"
// @Router /api/chats/notifications/events [get]
func (h *Handlers) NotificationEvents(c echo.Context) error {
	ch, cancel, err := h.svc.SubscribeNotifications(userID(c))
	if err != nil {
		return failFor(c, err)
	}
	defer cancel()

	return streamSSE(c, ch, func(n notify.Notification) (string, any) {
		if n.Op == notify.OpChanged {
			return "notifications_changed", struct{}{}
		}
		return "notification", notificationOut(n)
	})
}
