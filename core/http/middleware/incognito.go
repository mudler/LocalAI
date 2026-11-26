package middleware

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/privacy"
	"github.com/mudler/LocalAI/core/schema"
)

// Incognito sets the privacy level of a request from the marker header the
// web UI sends (privacy.Header). It must run after the auth middleware, which
// says how the request authenticated.
//
// The marker counts only for a web UI session (auth.IsWebSession); an API key
// that sends it gets normal behaviour. A web session that sends it while the
// policy is forbid, or with a level above incognito_max_level, gets 403, so
// the UI never shows an incognito state the server does not honour. The level
// that applies is echoed in the response header.
//
// An in-process request of the chat loop sends no marker: its context already
// carries the level of its chat, and this middleware leaves it as it is.
//
// authOn reports whether the server has accounts; the legacy keys are read
// from appConfig at each request because they change at run time.
func Incognito(appConfig *config.ApplicationConfig, authOn bool) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			marker := c.Request().Header.Get(privacy.Header)
			if marker == "" {
				return next(c)
			}
			if _, internal := auth.InternalUser(c.Request().Context()); internal {
				return next(c)
			}
			level, err := privacy.Decide(privacy.Request{
				Marker:     marker,
				WebSession: auth.IsWebSession(c, authOn || len(appConfig.ApiKeys) > 0),
				Policy:     appConfig.IncognitoPolicy(),
				MaxLevel:   appConfig.IncognitoMaxLevel(),
			})
			switch {
			case errors.Is(err, privacy.ErrInvalidLevel):
				return c.JSON(http.StatusBadRequest, schema.ErrorResponse{Error: &schema.APIError{
					Message: err.Error(), Code: http.StatusBadRequest, Type: "invalid_request_error",
				}})
			case err != nil:
				return c.JSON(http.StatusForbidden, schema.ErrorResponse{Error: &schema.APIError{
					Message: err.Error(), Code: http.StatusForbidden, Type: "incognito_forbidden",
				}})
			}
			if level.Incognito() {
				c.SetRequest(c.Request().WithContext(privacy.WithLevel(c.Request().Context(), level)))
				c.Response().Header().Set(privacy.Header, level.String())
			}
			return next(c)
		}
	}
}
