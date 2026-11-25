package auth

import "github.com/labstack/echo/v4"

// IsWebSession reports whether the request comes from the web UI: a session
// cookie, a legacy key the UI keeps in its token cookie, or any request when
// the server has no authentication at all (authOn false). A key sent in a
// header (Authorization, x-api-key, xi-api-key) is not, even a session token
// in a bearer header, and neither is an in-process request of the chat loop.
// Features that only the web UI may switch on, such as the incognito marker,
// use it so that an API client cannot.
func IsWebSession(c echo.Context, authOn bool) bool {
	if _, ok := internalUser(c.Request().Context()); ok {
		return false
	}
	if !authOn {
		return true
	}
	if _, ok := c.Get("_auth_session").(*Session); ok {
		return true
	}
	if GetSource(c) == UsageSourceLegacy {
		header, _ := c.Get(contextKeyHeaderAuthenticated).(bool)
		return !header
	}
	return false
}
