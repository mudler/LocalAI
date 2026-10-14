// SPDX-License-Identifier: MIT
package addons

import (
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
)

// The resource management routes are the UI-only half of the add-on data
// model: listing, creating, renaming and deleting resources, whole-resource
// sharing and the conversation selection. They are never tools. No MCP tool,
// model tool or proposal maps to them, and the mutations are accepted only
// from the person's own browser session, like a confirmation.

// resourceCall is the shared shape of a resource management handler.
func resourceCall(g *gateway.Gateway, db *gorm.DB, opts []Option, human func(echo.Context) string, call func(echo.Context, *gateway.Gateway, contract.Actor) (any, error)) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		if human != nil {
			id := human(c)
			if !humanSurface(c, s, a, id) {
				return failure(c, 403, "This action requires your own browser session")
			}
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		out, e := call(c, g, a)
		if e != nil {
			return resultError(c, e)
		}
		if out == nil {
			return c.NoContent(http.StatusNoContent)
		}
		return c.JSON(200, out)
	}
}

func byParam(name string) func(echo.Context) string {
	return func(c echo.Context) string { return c.Param(name) }
}

// ListResources lists the resources of an addon the caller may use.
// @Summary List the add-on resources the caller may use
// @Description Returns the resources of one add-on that the caller owns or that were shared with the caller, each with the caller's own role, the display name the add-on reported (empty when it could not answer in time), the owner's display name for a shared resource and, for the owner role only, how many people it is shared with and the revision to send back with a rename or a delete. Resources of other people that are not shared with the caller are never listed, whatever the caller's installation role: installation administration is not data access. The answer also carries the installed generation, whether sharing is possible (false in local-owner mode) and the management operations the add-on supports. At most 100 resources are listed (truncated says more exist). Read-only. API keys may call it. No MCP tool maps to this route.
// @Tags Addons
// @Produce json
// @Param addonId query string true "Add-on ID"
// @Success 200 {object} gateway.ResourceList "Resources the caller may use"
// @Failure 400 {object} AddonErrorResponse "Missing or malformed add-on ID"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature denied"
// @Failure 503 {object} AddonErrorResponse "Addons, the owner or the add-on are unavailable"
// @Router /api/addons/resources [get]
func ListResources(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return resourceCall(g, db, opts, nil, func(c echo.Context, g *gateway.Gateway, a contract.Actor) (any, error) {
		return g.ListResources(c.Request().Context(), a, c.QueryParam("addonId"))
	})
}

// CreateResource creates a resource.
// @Summary Create an add-on resource (UI only)
// @Description Creates a resource through the add-on's create tool as the person's own explicit action, for example a new database. It needs no confirmation card: the request is the intent, and the same admission, quota, receipt and crash reconciliation rules as every mutation apply. The caller becomes the owner and the resource is private. Repeating the same operationId returns the same resource and never creates a second one; after outcome_unknown repeat the same call. The arguments are validated against the add-on's own tool definition. Accepted only from the person's own browser session: a same-origin fetch whose X-LocalAI-Confirm header equals the operationId, with no API key and no tool, agent or MCP marker. No MCP tool maps to this route.
// @Tags Addons
// @Accept json
// @Produce json
// @Param request body gateway.CreateResourceRequest true "Create request"
// @Success 200 {object} gateway.ResourceInfo "The created resource"
// @Failure 400 {object} AddonErrorResponse "Malformed request or arguments the add-on's tool does not accept"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human surface, feature or creation authority denied, or type quota_reached"
// @Failure 409 {object} AddonErrorResponse "Type stale: the generation is not the installed one"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or type outcome_unknown (with operationId) after which the same call is repeated"
// @Router /api/addons/resources [post]
func CreateResource(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		var r gateway.CreateResourceRequest
		if decode(c, &r) != nil || r.OperationID == "" {
			return failure(c, 400, "Invalid resource creation")
		}
		if !humanSurface(c, s, a, r.OperationID) {
			return failure(c, 403, "Creating a resource requires your own browser session")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		out, e := g.CreateResource(c.Request().Context(), a, r)
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, out)
	}
}

// ListResourceGrants lists the grants of a resource.
// @Summary List who a resource is shared with (owner, UI only)
// @Description Returns the grants of the whole resource: the immutable account ID, the display name and the role of each grantee. Only the owner role of the resource may read it; installation administrators, editors and viewers get the same denial as for an unknown resource. Local-owner mode has no sharing and answers 409 sharing_disabled. Accepted only from the owner's own browser session (same-origin fetch, X-LocalAI-Confirm equal to the resource ID). No MCP tool maps to this route.
// @Tags Addons
// @Produce json
// @Param resourceId path string true "Resource ID"
// @Success 200 {object} gateway.GrantList "Grants of the resource"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human surface, feature denied, not the owner role or unknown resource"
// @Failure 409 {object} AddonErrorResponse "Type sharing_disabled"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/resources/{resourceId}/grants [get]
func ListResourceGrants(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return resourceCall(g, db, opts, byParam("resourceId"), func(c echo.Context, g *gateway.Gateway, a contract.Actor) (any, error) {
		return g.ResourceGrants(c.Request().Context(), a, c.Param("resourceId"))
	})
}

// SetResourceGrant gives a user a role on the whole resource.
// @Summary Share a whole resource with a named user (owner, UI only)
// @Description Gives the user the viewer or editor role on the whole resource; there are no per-table grants and the owner role cannot be granted. The user is named by the immutable account ID the user picker returned, and must be an active named account that may use add-ons. Lowering a role stops the user's admitted work and cancels the user's pending proposals before it is acknowledged. Local-owner mode answers 409 sharing_disabled. Accepted only from the owner's own browser session (same-origin fetch, X-LocalAI-Confirm equal to the resource ID). Safe to repeat; after outcome_unknown repeat the same call. No MCP tool maps to this route: sharing is never a tool.
// @Tags Addons
// @Accept json
// @Produce json
// @Param resourceId path string true "Resource ID"
// @Param userId path string true "Account ID of the grantee"
// @Param request body gateway.SetGrantRequest true "Role"
// @Success 200 {object} gateway.GrantList "Grants after the change"
// @Failure 400 {object} AddonErrorResponse "Malformed body or role"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human surface, feature denied, not the owner role, unknown resource or a user that cannot be granted"
// @Failure 409 {object} AddonErrorResponse "Type sharing_disabled"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or type outcome_unknown (with operationId)"
// @Router /api/addons/resources/{resourceId}/grants/{userId} [put]
func SetResourceGrant(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		if !humanSurface(c, s, a, c.Param("resourceId")) {
			return failure(c, 403, "Sharing requires the owner's own browser session")
		}
		var r gateway.SetGrantRequest
		if decode(c, &r) != nil || r.Role == "" {
			return failure(c, 400, "Invalid grant")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		out, e := g.SetResourceGrant(c.Request().Context(), a, r.OperationID, c.Param("resourceId"), c.Param("userId"), r.Role)
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, out)
	}
}

// RevokeResourceGrant removes a user's grant.
// @Summary Stop sharing a resource with a user (owner, UI only)
// @Description Removes the user's grant. It returns only after the user's admitted work stopped and their pending proposals were cancelled; a drain that did not finish in the bound is a 503 and the revocation is already effective for new work. Local-owner mode answers 409 sharing_disabled. Accepted only from the owner's own browser session (same-origin fetch, X-LocalAI-Confirm equal to the resource ID). Safe to repeat. No MCP tool maps to this route.
// @Tags Addons
// @Produce json
// @Param resourceId path string true "Resource ID"
// @Param userId path string true "Account ID of the grantee"
// @Param operationId query string false "Optional operation ID, returned with outcome_unknown"
// @Success 200 {object} gateway.GrantList "Grants after the change"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human surface, feature denied, not the owner role or unknown resource"
// @Failure 409 {object} AddonErrorResponse "Type sharing_disabled"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or type outcome_unknown (with operationId)"
// @Router /api/addons/resources/{resourceId}/grants/{userId} [delete]
func RevokeResourceGrant(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return resourceCall(g, db, opts, byParam("resourceId"), func(c echo.Context, g *gateway.Gateway, a contract.Actor) (any, error) {
		return g.RevokeResourceGrant(c.Request().Context(), a, c.QueryParam("operationId"), c.Param("resourceId"), c.Param("userId"))
	})
}

// SearchResourceUsers is the user picker.
// @Summary Search the people a resource can be shared with (owner, UI only)
// @Description Finds active named accounts that may use add-ons by a prefix of the display name or of the address, 2 to 64 characters, at most 20 results. Each result is an immutable account ID and a display name and nothing else. Only the owner role of the resource may search; the owner and the caller are left out. Local-owner mode answers 409 sharing_disabled. Accepted only from the owner's own browser session (same-origin fetch, X-LocalAI-Confirm equal to the resource ID). No MCP tool maps to this route.
// @Tags Addons
// @Produce json
// @Param resourceId path string true "Resource ID"
// @Param q query string true "Name or address prefix"
// @Param limit query int false "At most this many results, up to 20"
// @Success 200 {object} gateway.UserList "Matching people"
// @Failure 400 {object} AddonErrorResponse "Query too short or too long"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human surface, feature denied, not the owner role or unknown resource"
// @Failure 409 {object} AddonErrorResponse "Type sharing_disabled"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/resources/{resourceId}/users [get]
func SearchResourceUsers(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return resourceCall(g, db, opts, byParam("resourceId"), func(c echo.Context, g *gateway.Gateway, a contract.Actor) (any, error) {
		limit, _ := strconv.Atoi(c.QueryParam("limit"))
		return g.SearchResourceUsers(c.Request().Context(), a, c.Param("resourceId"), c.QueryParam("q"), limit)
	})
}

// RenameResource renames a resource.
// @Summary Rename a resource (owner, UI only)
// @Description Renames the resource through the add-on as the owner's own explicit action, with no confirmation card. It still runs under the resource's admission, waits behind an unresolved outcome (403 outcome_blocked for the owner role), is checked against the generation and the revision the owner listed (409 stale, nothing changed) and leaves the add-on's receipt for the operation. Repeating the same operationId never renames twice; after outcome_unknown repeat the same call or look it up with the operation status route. Accepted only from the owner's own browser session (same-origin fetch, X-LocalAI-Confirm equal to the resource ID). No MCP tool maps to this route.
// @Tags Addons
// @Accept json
// @Produce json
// @Param resourceId path string true "Resource ID"
// @Param request body gateway.RenameResourceRequest true "Rename request"
// @Success 200 {object} gateway.ResourceInfo "The renamed resource"
// @Failure 400 {object} AddonErrorResponse "Malformed request or a name the add-on refuses"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human surface, feature denied, not the owner role or unknown resource, or type outcome_blocked"
// @Failure 409 {object} AddonErrorResponse "Type stale"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or type outcome_unknown (with operationId)"
// @Router /api/addons/resources/{resourceId}/rename [post]
func RenameResource(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		if !humanSurface(c, s, a, c.Param("resourceId")) {
			return failure(c, 403, "Renaming requires the owner's own browser session")
		}
		var r gateway.RenameResourceRequest
		if decode(c, &r) != nil || r.OperationID == "" {
			return failure(c, 400, "Invalid rename")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		out, e := g.RenameResource(c.Request().Context(), a, c.Param("resourceId"), r)
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, out)
	}
}

// DeleteResource deletes a resource and its data.
// @Summary Delete a resource and its data (owner, UI only)
// @Description Permanently deletes the resource, its data and every grant, as the owner's own explicit action. It is never a chat operation and never an uninstall. The request must carry the generation and revision the owner listed and confirm, which must equal the resource's display name exactly. The owner marks the resource as deleting (inaccessible to everyone, no new admission), stops and waits for admitted work, checks the revision, cancels pending proposals, asks the add-on to delete and only then removes the record and the grants. A crash leaves a deleting record that the owner finishes at startup; an unconfirmed one returns to use. A 503 with type outcome_unknown means the deletion is confirmed and being finished: repeat the same call, the resource is inaccessible meanwhile. Accepted only from the owner's own browser session (same-origin fetch, X-LocalAI-Confirm equal to the resource ID). No MCP tool maps to this route and the add-on's delete tool is not offered to any model.
// @Tags Addons
// @Accept json
// @Produce json
// @Param resourceId path string true "Resource ID"
// @Param request body gateway.DeleteResourceRequest true "Delete request with the typed confirmation"
// @Success 200 {object} gateway.DeleteResult "Deleted"
// @Failure 400 {object} AddonErrorResponse "Malformed request, wrong confirmation or unsupported operation"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human surface, feature denied, not the owner role or unknown resource, or type outcome_blocked"
// @Failure 409 {object} AddonErrorResponse "Type stale"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or type outcome_unknown (with operationId)"
// @Router /api/addons/resources/{resourceId}/delete [post]
func DeleteResource(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		if !humanSurface(c, s, a, c.Param("resourceId")) {
			return failure(c, 403, "Deleting requires the owner's own browser session")
		}
		var r gateway.DeleteResourceRequest
		if decode(c, &r) != nil || r.OperationID == "" || r.Confirm == "" {
			return failure(c, 400, "Invalid deletion")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		out, e := g.DeleteResource(c.Request().Context(), a, c.Param("resourceId"), r)
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, out)
	}
}

// ApplyResource applies a data or schema change of a resource.
// @Summary Change a resource's data or schema by the person's own action (UI only)
// @Description Applies one change through a write or schema tool the add-on declares, for example inserting rows or adding a column, as the person's own explicit action in the person's own browser tab. It needs no confirmation card: the request is the intent. It is not a proposal and nothing is stored for later. The same rules as every change apply: the role of the action (the owner and editors write rows, only the owner changes the schema, viewers and installation administrators without a grant get the answer of an unknown resource), the declared tool definition, the resource's admission, the generation and the revision the caller read (409 stale, nothing changed), the add-on's receipt, which makes repeating the same operationId with the same request one change, and a wait behind an unresolved outcome. The tool's arguments must not name the resource, the tool, an actor or any field the tool definition does not declare. Reads, creations, the add-on's rename and delete, administrator tools and tools nobody declared are refused with 400. Accepted only from the person's own browser session: a same-origin fetch whose X-LocalAI-Confirm header equals the resource ID, with no API key and no tool, agent or MCP marker. After outcome_unknown, look the receipt up with kind=apply and the original operationId; never submit a new operation. No MCP tool maps to this route.
// @Tags Addons
// @Accept json
// @Produce json
// @Param resourceId path string true "Resource ID"
// @Param request body gateway.ApplyResourceRequest true "Change request"
// @Success 200 {object} gateway.ApplyResult "The committed change and its receipt"
// @Failure 400 {object} AddonErrorResponse "Malformed request, a tool that is not a change, or arguments the tool does not accept"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human surface, feature denied, role too low or unknown resource, or type outcome_blocked"
// @Failure 409 {object} AddonErrorResponse "Type stale: the generation or the revision moved"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or type outcome_unknown (with operationId)"
// @Router /api/addons/resources/{resourceId}/apply [post]
func ApplyResource(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		if !humanSurface(c, s, a, c.Param("resourceId")) {
			return failure(c, 403, "Changing a resource requires your own browser session")
		}
		var r gateway.ApplyResourceRequest
		if decode(c, &r) != nil || r.OperationID == "" || r.Tool == "" || len(r.Arguments) == 0 {
			return failure(c, 400, "Invalid change")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		out, e := g.ApplyResource(c.Request().Context(), a, c.Param("resourceId"), r)
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, out)
	}
}

// GetResourceOperation looks a UI mutation's receipt up.
// @Summary Look up the receipt of a rename or a deletion (owner, UI only)
// @Description Read-only receipt lookup after an outcome_unknown answer. kind is rename (operationId is the one the rename carried), delete or apply (the operationId of a change; any role that may write looks up its own). The state is pending, committed, rejected or unknown; it never applies anything. The owner role may look up a rename or a deletion and the roles that may write look up their own changes, from the owner's own browser session (same-origin fetch, X-LocalAI-Confirm equal to the resource ID). After a deletion finished the resource is gone and the lookup is denied like any unknown resource. No MCP tool maps to this route.
// @Tags Addons
// @Produce json
// @Param resourceId path string true "Resource ID"
// @Param operationId path string true "Operation ID of the rename"
// @Param kind query string true "Operation kind" Enums(rename,delete,apply)
// @Param requestHash query string false "Apply only: the requestHash of the request the caller means (64 lowercase hex). A lookup whose hash is not the one the operation ID was used for is a 409 operation_conflict, never the other request's receipt"
// @Success 200 {object} gateway.OperationStatus "Receipt state"
// @Failure 400 {object} AddonErrorResponse "Unsupported kind or malformed requestHash"
// @Failure 409 {object} AddonErrorResponse "Type operation_conflict: the operation ID belongs to another request"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human surface, feature denied, not the owner role or unknown resource"
// @Failure 503 {object} AddonErrorResponse "Addons, the owner or the add-on are unavailable"
// @Router /api/addons/resources/{resourceId}/operations/{operationId} [get]
func GetResourceOperation(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return resourceCall(g, db, opts, byParam("resourceId"), func(c echo.Context, g *gateway.Gateway, a contract.Actor) (any, error) {
		return g.ResourceOperation(c.Request().Context(), a, c.Param("resourceId"), c.QueryParam("kind"), c.Param("operationId"), c.QueryParam("requestHash"))
	})
}

// GetConversationSelection reads a conversation's selected resource.
// @Summary Read the resource selected for a conversation
// @Description Returns the resource the authenticated user selected for the conversation and add-on, with the user's current role and display name. The stored reference is keyed by the authenticated user, the conversation and the add-on, and is never a credential: the owner authorizes it again on every use. A reference that was revoked, deleted or never set is one answer: 404 not_selected. Another user's selection of the same conversation ID is never visible. Read-only. No MCP tool maps to this route.
// @Tags Addons
// @Produce json
// @Param conversationId path string true "Conversation ID"
// @Param addonId path string true "Add-on ID"
// @Success 200 {object} gateway.Selection "The selection"
// @Failure 400 {object} AddonErrorResponse "Malformed conversation or add-on ID"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature denied"
// @Failure 404 {object} AddonErrorResponse "Type not_selected"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/conversations/{conversationId}/selection/{addonId} [get]
func GetConversationSelection(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return resourceCall(g, db, opts, nil, func(c echo.Context, g *gateway.Gateway, a contract.Actor) (any, error) {
		return g.ConversationSelection(c.Request().Context(), a, c.Param("conversationId"), c.Param("addonId"))
	})
}

// PutConversationSelection selects a resource for a conversation.
// @Summary Select the resource a conversation works on
// @Description Stores which resource the authenticated user chose for the conversation and add-on, so that chat tool calls that name no resource use it. The caller must hold at least the viewer role on the resource now; the stored reference is never a bearer and is authorized again at the owner on every use. It is not process-global, not shared between users or conversations, and not part of the chat history. The store is bounded per user and expires unused entries. No MCP tool maps to this route.
// @Tags Addons
// @Accept json
// @Produce json
// @Param conversationId path string true "Conversation ID"
// @Param addonId path string true "Add-on ID"
// @Param request body gateway.SelectionRequest true "The resource"
// @Success 200 {object} gateway.Selection "The selection"
// @Failure 400 {object} AddonErrorResponse "Malformed IDs or body"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature denied, or the resource is unknown or not readable by the caller"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/conversations/{conversationId}/selection/{addonId} [put]
func PutConversationSelection(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		var r gateway.SelectionRequest
		if decode(c, &r) != nil || r.ResourceID == "" {
			return failure(c, 400, "Invalid selection")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		out, e := g.SelectResource(c.Request().Context(), a, c.Param("conversationId"), c.Param("addonId"), r.ResourceID)
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, out)
	}
}

// DeleteConversationSelection clears a conversation's selection.
// @Summary Clear the resource selected for a conversation
// @Description Removes the authenticated user's selection for the conversation and add-on. Clearing a selection that does not exist succeeds. No MCP tool maps to this route.
// @Tags Addons
// @Param conversationId path string true "Conversation ID"
// @Param addonId path string true "Add-on ID"
// @Success 204 "Cleared"
// @Failure 400 {object} AddonErrorResponse "Malformed conversation or add-on ID"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature denied"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/conversations/{conversationId}/selection/{addonId} [delete]
func DeleteConversationSelection(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		if e = g.ClearConversationSelection(c.Request().Context(), a, c.Param("conversationId"), c.Param("addonId")); e != nil {
			return resultError(c, e)
		}
		return c.NoContent(http.StatusNoContent)
	}
}
