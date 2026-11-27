package middleware

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/priority"
)

// requestPriority returns the priority class of an API request.
//
// An in-process request of LocalAI's own callers (the chat loop, as the
// internal user) keeps the class its context carries: the caller knows whether
// a person waits. A network request cannot carry a Go context value, and the
// class of any other context is ignored, so the class of a network request
// comes from its service_tier only. A caller may lower its class (flex); only
// an administrator may raise it (priority), and so may anyone on a server with
// no authentication at all, where every caller already has an administrator's
// reach. On such a server the chat loop has no user to act as, so its context
// class is trusted there too: it grants nothing service_tier would not.
func requestPriority(c echo.Context, reqCtx context.Context, appConfig *config.ApplicationConfig, serviceTier string) (priority.Class, error) {
	mayRaise := mayRaisePriority(c, appConfig)
	_, internal := auth.InternalUser(reqCtx)
	if internal || (mayRaise && auth.GetUser(c) == nil) {
		if cls, ok := priority.FromContext(reqCtx); ok {
			return cls, nil
		}
	}
	cls, err := priority.Decide(serviceTier, mayRaise)
	if errors.Is(err, priority.ErrInvalidServiceTier) {
		return priority.Default, echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	return cls, err
}

// mayRaisePriority reports whether the caller may ask for a class above
// Default. It mirrors the admin middleware: an administrator may, and with no
// authentication configured (no user accounts, no API keys) every caller may.
func mayRaisePriority(c echo.Context, appConfig *config.ApplicationConfig) bool {
	if u := auth.GetUser(c); u != nil {
		return u.Role == auth.RoleAdmin
	}
	if appConfig == nil {
		return false
	}
	return !appConfig.Auth.Enabled && len(appConfig.ApiKeys) == 0
}
