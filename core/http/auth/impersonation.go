package auth

import (
	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"
)

// UserIDQueryParam is the query parameter an administrator uses to act on the
// resources of another user.
const UserIDQueryParam = "user_id"

// CanImpersonate returns true if the caller is allowed to use ?user_id= to
// scope operations to another user. Admins only: the agent worker no longer
// calls the HTTP API, so the agent-worker provider carries no extra power (rows
// an earlier release created are treated as ordinary users).
func CanImpersonate(c echo.Context) bool {
	user := GetUser(c)
	if user == nil {
		return false
	}
	return user.Role == RoleAdmin
}

// LogImpersonation writes the audit line for a caller who acts for another
// user. Every route that honors ?user_id= writes it, so there is one trail.
func LogImpersonation(callerID, targetID, path string) {
	xlog.Info("User impersonation", "caller", callerID, "target", targetID, "path", path)
}

// EffectiveUserID returns the user ID to scope operations to.
// SECURITY: Only admins may supply ?user_id=<id> to operate on another user's
// resources. Regular callers always get their own ID regardless of query params.
func EffectiveUserID(c echo.Context) string {
	callerID := ""
	if user := GetUser(c); user != nil {
		callerID = user.ID
	}
	if targetUID := c.QueryParam(UserIDQueryParam); targetUID != "" && CanImpersonate(c) {
		if callerID != targetUID {
			LogImpersonation(callerID, targetUID, c.Path())
		}
		return targetUID
	}
	return callerID
}
