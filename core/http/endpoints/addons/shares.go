// SPDX-License-Identifier: MIT
package addons

import (
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
)

// The sharing routes serve the resource kinds that are not add-ons with the
// same rules as the add-on grant routes: the owner role only, the person's
// own browser session only for anything but the listing, and never a tool.
// No MCP tool, model tool or proposal maps to them. They do not need the
// add-ons feature: the caller is checked as an account, and the owner's
// policy checks the feature the kind names, if any.

// sharingOpts makes the handlers check the account, not the add-on access.
func sharingOpts(opts []Option) []Option {
	return append(opts[:len(opts):len(opts)], func(s *settings) { s.account = true })
}

// ListShares lists the resources of a kind the caller owns or that were shared with the caller.
// @Summary List the shared resources of a kind
// @Description Returns the resources of one kind that the caller owns (owned) and those shared with the caller (shared), at most 100 of each (truncated says more exist). Each one has the caller's own role, which is owner or a role the kind grants; for a shared one the owner's display name, and everyone true when the role comes from a grant to every account; for an owned one the number of grants (sharedWith). The answer also carries the roles the kind grants and whether it may be shared with every account. Resources of other people that are not shared with the caller are never listed, whatever the caller's installation role. An unknown kind gets the same denial as a resource the caller may not see. Local-owner mode has no sharing and answers 409 sharing_disabled. Needs a named account, not the addons feature; a kind may name a feature of its own. Read-only. API keys may call it. No MCP tool maps to this route.
// @Tags Sharing
// @Produce json
// @Param kind path string true "Resource kind, for example example:note"
// @Success 200 {object} gateway.ShareIndex "Owned and shared resources of the kind"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "A feature the kind names is denied, or unknown kind"
// @Failure 409 {object} AddonErrorResponse "Type sharing_disabled"
// @Failure 503 {object} AddonErrorResponse "The owner is unavailable"
// @Router /api/shares/{kind} [get]
func ListShares(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return resourceCall(g, db, sharingOpts(opts), nil, func(c echo.Context, g *gateway.Gateway, a contract.Actor) (any, error) {
		return g.Shares(c.Request().Context(), a, c.Param("kind"))
	})
}

// ListShareGrants lists the grants of a resource of a kind.
// @Summary List who a resource is shared with (owner, UI only)
// @Description Returns the grants of the whole resource: the immutable account ID, the display name and the role of each grantee. A grant to every account has the user ID @everyone and no display name. Only the owner of the resource may read it; installation administrators and grantees get the same denial as for an unknown resource or an unknown kind. Local-owner mode answers 409 sharing_disabled. Accepted only from the owner's own browser session (same-origin fetch, X-LocalAI-Confirm equal to the resource ID, no API key, no tool, agent or MCP marker). No MCP tool maps to this route.
// @Tags Sharing
// @Produce json
// @Param kind path string true "Resource kind"
// @Param resourceId path string true "Resource ID"
// @Success 200 {object} gateway.ShareList "Grants of the resource"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human surface, a feature the kind names denied, not the owner, unknown kind or unknown resource"
// @Failure 409 {object} AddonErrorResponse "Type sharing_disabled"
// @Failure 503 {object} AddonErrorResponse "The owner is unavailable"
// @Router /api/shares/{kind}/{resourceId}/grants [get]
func ListShareGrants(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return resourceCall(g, db, sharingOpts(opts), byParam("resourceId"), func(c echo.Context, g *gateway.Gateway, a contract.Actor) (any, error) {
		return g.ShareGrants(c.Request().Context(), a, c.Param("kind"), c.Param("resourceId"))
	})
}

// SetShareGrant gives a user, or every account, a role on a resource of a kind.
// @Summary Share a resource with a user or with everyone (owner, UI only)
// @Description Gives the principal one of the roles the kind grants (for example use or manage) on the whole resource. The principal is the immutable account ID the user picker returned, an active named account that the kind must accept as a grantee (it does not need the addons feature), or @everyone on a kind that allows it: every active named account, accounts created later included. The owner role cannot be granted. A user's own grant and the grant to everyone add up and are changed and revoked apart. Lowering a role stops the grantee's admitted work that the role no longer allows before it is acknowledged. Local-owner mode answers 409 sharing_disabled. Accepted only from the owner's own browser session (same-origin fetch, X-LocalAI-Confirm equal to the resource ID, no API key, no tool, agent or MCP marker). Safe to repeat; after outcome_unknown repeat the same call. No MCP tool maps to this route: sharing is never a tool.
// @Tags Sharing
// @Accept json
// @Produce json
// @Param kind path string true "Resource kind"
// @Param resourceId path string true "Resource ID"
// @Param principal path string true "Account ID of the grantee, or @everyone"
// @Param request body gateway.SetShareRequest true "Role"
// @Success 200 {object} gateway.ShareList "Grants after the change"
// @Failure 400 {object} AddonErrorResponse "Malformed body, or type invalid_request for a role the kind does not grant"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human surface, a feature the kind names denied, not the owner, unknown kind or resource, or a principal that cannot be granted"
// @Failure 409 {object} AddonErrorResponse "Type sharing_disabled"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or type outcome_unknown (with operationId)"
// @Router /api/shares/{kind}/{resourceId}/grants/{principal} [put]
func SetShareGrant(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return grantChange(g, db, sharingOpts(opts), func(r gateway.SetShareRequest) bool { return r.Role != "" },
		func(c echo.Context, g *gateway.Gateway, a contract.Actor, r gateway.SetShareRequest) (any, error) {
			return g.SetShare(c.Request().Context(), a, r.OperationID, c.Param("kind"), c.Param("resourceId"), c.Param("principal"), r.Role)
		})
}

// RevokeShareGrant removes a grant on a resource of a kind.
// @Summary Stop sharing a resource with a user or with everyone (owner, UI only)
// @Description Removes the grant of the principal, an account ID or @everyone. The other grant a user may hold stays: revoking @everyone leaves the users' own grants, and revoking a user leaves what @everyone holds. It returns only after the admitted work that the removed grant alone allowed stopped; a drain that did not finish in the bound is a 503 and the revocation is already effective for new work. Local-owner mode answers 409 sharing_disabled. Accepted only from the owner's own browser session (same-origin fetch, X-LocalAI-Confirm equal to the resource ID, no API key, no tool, agent or MCP marker). Safe to repeat. No MCP tool maps to this route.
// @Tags Sharing
// @Produce json
// @Param kind path string true "Resource kind"
// @Param resourceId path string true "Resource ID"
// @Param principal path string true "Account ID of the grantee, or @everyone"
// @Param operationId query string false "Optional operation ID, returned with outcome_unknown"
// @Success 200 {object} gateway.ShareList "Grants after the change"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human surface, a feature the kind names denied, not the owner, unknown kind or unknown resource"
// @Failure 409 {object} AddonErrorResponse "Type sharing_disabled"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or type outcome_unknown (with operationId)"
// @Router /api/shares/{kind}/{resourceId}/grants/{principal} [delete]
func RevokeShareGrant(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return resourceCall(g, db, sharingOpts(opts), byParam("resourceId"), func(c echo.Context, g *gateway.Gateway, a contract.Actor) (any, error) {
		return g.RevokeShare(c.Request().Context(), a, c.QueryParam("operationId"), c.Param("kind"), c.Param("resourceId"), c.Param("principal"))
	})
}

// SearchShareUsers is the user picker of a resource of a kind.
// @Summary Search the people a resource can be shared with (owner, UI only)
// @Description Finds active named accounts by a prefix of the display name, 3 to 64 characters, at most 20 results, whether or not they may use add-ons, and keeps only those the kind accepts as grantees. A prefix of an address finds nobody; an account without a display name is found by its whole address only and comes back with an empty display name. Each result is an immutable account ID and a display name and nothing else. Each person may search 20 times at once and then once every 2 seconds; a search over that rate answers 429 rate_limited. Only the owner of the resource may search; the owner and the caller are left out. Local-owner mode answers 409 sharing_disabled. Accepted only from the owner's own browser session (same-origin fetch, X-LocalAI-Confirm equal to the resource ID, no API key, no tool, agent or MCP marker). No MCP tool maps to this route.
// @Tags Sharing
// @Produce json
// @Param kind path string true "Resource kind"
// @Param resourceId path string true "Resource ID"
// @Param q query string true "Display name prefix, or a whole address"
// @Param limit query int false "At most this many results, up to 20"
// @Success 200 {object} gateway.UserList "Matching people"
// @Failure 400 {object} AddonErrorResponse "Query too short or too long"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human surface, a feature the kind names denied, not the owner, unknown kind or unknown resource"
// @Failure 409 {object} AddonErrorResponse "Type sharing_disabled"
// @Failure 429 {object} AddonErrorResponse "Type rate_limited: too many searches"
// @Failure 503 {object} AddonErrorResponse "The owner is unavailable"
// @Router /api/shares/{kind}/{resourceId}/users [get]
func SearchShareUsers(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return resourceCall(g, db, sharingOpts(opts), byParam("resourceId"), func(c echo.Context, g *gateway.Gateway, a contract.Actor) (any, error) {
		limit, _ := strconv.Atoi(c.QueryParam("limit"))
		return g.SearchShareUsers(c.Request().Context(), a, c.Param("kind"), c.Param("resourceId"), c.QueryParam("q"), limit)
	})
}
