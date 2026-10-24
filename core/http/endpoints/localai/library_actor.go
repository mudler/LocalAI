// SPDX-License-Identifier: MIT
package localai

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/library"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
)

// LibraryActor is the middleware of the library routes on a frontend. It runs
// after the administrator middleware and gives the request the actor that the
// carrier signs for the owner, built and revalidated as the add-on routes do.
// A legacy API key has no named user and is refused, also in local-owner mode:
// the owner cannot tell who changed its sources.
func LibraryActor(app *application.Application) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if u := auth.GetUser(c); u != nil && u.ID == "legacy-api-key" {
				return librarySourceError(c, http.StatusUnauthorized, "a named account is required: the legacy API key cannot change the library on a frontend")
			}
			mode := app.AddonOwnershipMode()
			actor, err := auth.AddonActorForMode(c, mode)
			if err == nil {
				err = auth.AddonRevalidate(app.AuthDB(), mode)(c.Request().Context(), actor)
			}
			if err != nil {
				if errors.Is(err, auth.ErrAddonUnavailable) {
					gateway.WriteUnavailable(c.Response(), gateway.ReasonOwnerUnreached)
					return nil
				}
				return librarySourceError(c, http.StatusUnauthorized, "a named account is required")
			}
			c.SetRequest(c.Request().WithContext(library.WithActor(c.Request().Context(), actor)))
			return next(c)
		}
	}
}
