package localai

import (
	"net/url"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
)

// getUserID extracts the scoped user ID from the request context.
// Returns empty string when auth is not active (backward compat).
func getUserID(c echo.Context) string {
	user := auth.GetUser(c)
	if user == nil {
		return ""
	}
	return user.ID
}

// decodedParam returns the named path parameter, URL-decoding it.
//
// Echo routes a request via URL.RawPath whenever the path contains
// percent-encoded characters (e.g. %3A for ':'), and in that case stores the
// matched path-param value raw/escaped. Agent and collection names carry a
// "legacy-api-key:" prefix, so the ':' arrives as %3A and the raw param no
// longer matches the stored name. Callers must unescape before lookups.
// Falls back to the raw value if it isn't valid percent-encoding.
func decodedParam(c echo.Context, name string) string {
	raw := c.Param(name)
	if decoded, err := url.PathUnescape(raw); err == nil {
		return decoded
	}
	return raw
}

// isAdminUser returns true if the authenticated user has admin role.
func isAdminUser(c echo.Context) bool {
	user := auth.GetUser(c)
	return user != nil && user.Role == auth.RoleAdmin
}

// wantsAllUsers returns true if the request has ?all_users=true and the user is admin.
func wantsAllUsers(c echo.Context) bool {
	return c.QueryParam("all_users") == "true" && isAdminUser(c)
}

// effectiveUserID returns the user ID to scope operations to.
// SECURITY: Only admins may supply ?user_id=<id> to operate on another user's
// resources. Regular callers always get their own ID regardless of query params.
// The rule lives in the auth package so the project routes share it.
func effectiveUserID(c echo.Context) string { return auth.EffectiveUserID(c) }

// canImpersonateUser returns true if the caller is allowed to use ?user_id= to
// scope operations to another user. Admins only.
func canImpersonateUser(c echo.Context) bool { return auth.CanImpersonate(c) }
