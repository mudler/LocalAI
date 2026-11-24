package routes

import (
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/http/endpoints/notifications"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/notify"
)

// RegisterNotificationRoutes registers the notification center: the inbox of a
// person and the browser push that tells them. The routes need the notifier
// and nothing else, so they work with the chats on or off. They carry no
// feature gate: every signed-in person has an inbox, and a row is only ever
// read by its owner.
//
// With chats set, the older paths under /api/chats answer the same handlers,
// so a client written for them keeps working (they stay gated by the chats
// feature in the route-feature registry). Without a notifier every one of the
// paths answers 404, so a client sees "off" and not a missing route.
func RegisterNotificationRoutes(e *echo.Echo, n *notify.Service, p *notify.Pusher, chats bool) {
	bases := []string{"/api/notifications"}
	if chats {
		bases = append(bases, "/api/chats/notifications")
	}
	if n == nil {
		off := func(c echo.Context) error {
			return c.JSON(http.StatusNotFound, schema.ErrorResponse{Error: &schema.APIError{
				Message: "the notification center is not available on this server", Code: http.StatusNotFound, Type: "notifications_off"}})
		}
		for _, b := range bases {
			e.Any(b, off)
			e.Any(b+"/*", off)
		}
		return
	}
	h := notifications.New(n, p)
	for _, b := range bases {
		e.GET(b, h.List)
		e.DELETE(b, h.Delete)
		e.GET(b+"/count", h.Count)
		e.POST(b+"/read", h.Read)
		e.GET(b+"/events", h.Events)
	}
	// The browser push of the same notifications.
	e.GET("/api/notifications/push/key", h.PushKey)
	e.POST("/api/notifications/push/subscribe", h.PushSubscribe)
	e.DELETE("/api/notifications/push/subscribe", h.PushUnsubscribe)
	if chats {
		e.GET("/api/chats/push/key", h.PushKey)
		e.POST("/api/chats/push/subscribe", h.PushSubscribe)
		e.DELETE("/api/chats/push/subscribe", h.PushUnsubscribe)
	}
}
