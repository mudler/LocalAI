package studio

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/notify"
)

func (h *Handlers) pusher(c echo.Context) (*notify.Pusher, bool) {
	return feature(c, h.svc.Pusher(), func(c echo.Context) error {
		return fail(c, http.StatusConflict, "push_off", studiosvc.ErrPushOff.Error()+": the server needs a database or a data path to keep subscriptions")
	})
}

// PushKey returns the public key for browser push.
// @Summary Get the browser push key.
// @Description The VAPID public key a browser needs to subscribe to push for your notifications. 409 when the server cannot keep subscriptions (no database and no data path).
// @Tags chats
// @Success 200 {object} schema.ChatsPushKey "The key"
// @Router /api/chats/push/key [get]
func (h *Handlers) PushKey(c echo.Context) error {
	p, ok := h.pusher(c)
	if !ok {
		return nil
	}
	key, err := p.PublicKey(c.Request().Context())
	if err != nil {
		return failFor(c, err)
	}
	return c.JSON(http.StatusOK, schema.ChatsPushKey{PublicKey: key})
}

// PushSubscribe stores a browser's push subscription.
// @Summary Subscribe a browser to push.
// @Description Pass the endpoint and keys of a browser PushSubscription. A notification is then pushed to that browser with its title and link only. The endpoint must be an https address on the public internet. At most 10 browsers per user; the oldest go first.
// @Tags chats
// @Param request body schema.ChatsPushSubscription true "the browser's subscription"
// @Success 204 "Subscribed"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/push/subscribe [post]
func (h *Handlers) PushSubscribe(c echo.Context) error {
	p, ok := h.pusher(c)
	if !ok {
		return nil
	}
	req, ok := bind[schema.ChatsPushSubscription](c, msgNotJSON)
	if !ok {
		return nil
	}
	if err := p.Subscribe(c.Request().Context(), h.svc.Principal(userID(c)), req.Endpoint, req.Keys.P256dh, req.Keys.Auth); err != nil {
		if errors.Is(err, notify.ErrPushEndpoint) {
			return fail(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		}
		return failFor(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

// PushUnsubscribe removes a browser's push subscription.
// @Summary Unsubscribe a browser from push.
// @Tags chats
// @Param request body schema.ChatsPushUnsubscribe true "the endpoint"
// @Success 204 "Unsubscribed"
// @Failure 400 {object} schema.ErrorResponse "The request is not valid"
// @Router /api/chats/push/subscribe [delete]
func (h *Handlers) PushUnsubscribe(c echo.Context) error {
	p, ok := h.pusher(c)
	if !ok {
		return nil
	}
	var req schema.ChatsPushUnsubscribe
	if err := c.Bind(&req); err != nil || req.Endpoint == "" {
		return fail(c, http.StatusBadRequest, "invalid_request_error", "endpoint is required")
	}
	if err := p.Unsubscribe(c.Request().Context(), h.svc.Principal(userID(c)), req.Endpoint); err != nil {
		return failFor(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}
