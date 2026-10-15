// SPDX-License-Identifier: MIT
package addons

import (
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
)

// ConfirmHeader must carry the proposal identifier on a confirmation. The host
// confirmation surface sets it from the proposal the human is looking at.
const ConfirmHeader = "X-LocalAI-Confirm"

// toolContextHeaders mark a request made by a tool, an agent run or an MCP
// session. A confirmation that carries any of them is refused, whatever else
// it carries. They are a second line: the credential rules below already
// exclude every caller that has no browser session.
var toolContextHeaders = []string{"Mcp-Session-Id", "X-LocalAI-Tool-Call", "X-LocalAI-Agent", "X-LocalAI-Execution-Context", "X-Mcp-Session-Id"}

// ConfirmRequest names what the human was shown.
type ConfirmRequest struct {
	// Digest is the digest of the proposal as it was displayed.
	Digest string `json:"digest"`
}

// humanSurface reports whether the request comes from the host's own
// confirmation surface: a logged-in browser tab of the person who proposed.
// Every rule must hold; none of them is satisfied by model output, tool
// arguments or an API key.
//
//  1. The credential is a browser session cookie. A named API key, a bearer
//     token or a legacy key is how unattended agents and MCP clients
//     authenticate, so none of them can confirm, whatever their role.
//  2. The request is a same-origin fetch from a page of this host
//     (Sec-Fetch-Site same-origin, destination empty, mode cors or
//     same-origin), which a cross-site page or a navigation cannot produce.
//  3. It names the proposal in the confirmation header, which a form post or
//     a link cannot set.
//  4. It carries no tool, agent or MCP execution marker.
//
// In local-owner mode there is no session; rules 2 to 4 still apply and a
// request that carries any API credential is refused.
func humanSurface(c echo.Context, s settings, a contract.Actor, id string) bool {
	h := c.Request().Header
	for _, name := range []string{"Authorization", "X-Api-Key", "Xi-Api-Key"} {
		if h.Get(name) != "" {
			return false
		}
	}
	if cookie, err := c.Cookie("token"); err == nil && cookie.Value != "" {
		return false
	}
	if auth.GetAPIKey(c) != nil {
		return false
	}
	for _, name := range toolContextHeaders {
		if h.Get(name) != "" {
			return false
		}
	}
	if h.Get("Sec-Fetch-Site") != "same-origin" || h.Get("Sec-Fetch-Dest") != "empty" {
		return false
	}
	if mode := h.Get("Sec-Fetch-Mode"); mode != "cors" && mode != "same-origin" {
		return false
	}
	if id == "" || h.Get(ConfirmHeader) != id {
		return false
	}
	if s.mode == auth.AddonModeLocalOwner {
		return a.CredentialID == auth.AddonLocalOwnerID
	}
	return strings.HasPrefix(a.CredentialID, "session:") && len(a.CredentialID) > len("session:")
}

// Propose stores a data or schema mutation as a pending proposal.
// @Summary Propose an addon data or schema mutation, or a resource creation, for confirmation
// @Description A request with action create and no resourceId proposes the creation of a resource: the owner reserves a pending resource for the caller that nobody can reach, shows the requested name, and creates it only on confirmation by the proposer's browser session. Previews the mutation on the addon owner and stores it as a pending proposal with its exact effect count, a bounded sample, the revisions it saw and an expiry. Nothing is applied. The operationId becomes the proposal ID; repeating the same request returns the same proposal. Sharing and database deletion are never proposals. A named session or API key with the addons feature may propose; the proposal can only be applied by Confirm, which requires the proposer's own browser session. No MCP tool confirms a proposal.
// @Tags Addons
// @Accept json
// @Produce json
// @Param request body contract.Request true "Typed write, schema or create request"
// @Success 200 {object} gateway.Proposal "Stored proposal"
// @Failure 400 {object} AddonErrorResponse "Malformed or unknown request fields, or type invalid_request when the add-on refused the request as invalid: nothing was applied and the same call is refused again"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature, resource policy or request binding denied the proposal. Type outcome_blocked (with blockedBy) when the caller is the owner role of the resource and another operation on it has an unresolved outcome; other callers get the plain denial"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable. Type outcome_unknown (with operationId): the proposal had been handed to the addon and its outcome is unknown, reconcile it. Type proposal_unavailable (with operationId): the owner could not serve this call and this call sent nothing to the addon; read the proposal for its state, which may already be settled, and repeat the same call with the same ID"
// @Router /api/addons/proposals [post]
func Propose(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		var r contract.Request
		if decode(c, &r) != nil {
			return failure(c, 400, "Invalid addon proposal")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		p, e := g.Propose(c.Request().Context(), a, r)
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, p)
	}
}

// ListPendingProposals lists the caller's own pending proposals.
// @Summary List the caller's own pending addon proposals
// @Description Returns the caller's proposals that still wait for a human decision, newest first, at most 32, each with its state, exact effect count, bounded sample, digest and expiry. This is where a change proposed by a chat tool, an agent or a scheduled job waits: only the proposer's own browser session can confirm it. A proposal of another user is never listed, and an expired one is left out. Read-only: it applies nothing. API keys may call it so an unattended agent can see what is waiting; they still cannot confirm.
// @Tags Addons
// @Produce json
// @Param state query string true "Only pending is supported" Enums(pending)
// @Success 200 {object} gateway.PendingList "The caller's pending proposals"
// @Failure 400 {object} AddonErrorResponse "Missing or unsupported state filter"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature denied"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/proposals [get]
func ListPendingProposals(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		if c.QueryParam("state") != "pending" {
			return failure(c, 400, "Only state=pending is supported")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		list, e := g.PendingProposals(c.Request().Context(), a)
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, list)
	}
}

// GetProposal returns the caller's proposal.
// @Summary Read a stored addon proposal
// @Description Returns the caller's own proposal with its state, effects, receipt and, for a pending proposal that is blocked, the operation to resolve first. Expiry is recorded when a pending proposal is read after its expiry.
// @Tags Addons
// @Produce json
// @Param id path string true "Proposal ID (the operation ID)"
// @Success 200 {object} gateway.Proposal "Stored proposal"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature denied, or the proposal is not the caller's"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/proposals/{id} [get]
func GetProposal(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return proposalCall(g, db, opts, func(c echo.Context, g *gateway.Gateway, a contract.Actor) (gateway.Proposal, error) {
		return g.GetProposal(c.Request().Context(), a, c.Param("id"))
	})
}

// CancelProposal withdraws a pending proposal.
// @Summary Cancel a pending addon proposal
// @Description Withdraws the caller's pending proposal. A cancelled, expired or settled proposal can never run. Cancelling needs only a current identity.
// @Tags Addons
// @Produce json
// @Param id path string true "Proposal ID (the operation ID)"
// @Success 200 {object} gateway.Proposal "Cancelled proposal"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature denied, the proposal is not the caller's, or it is not pending"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable. Type outcome_unknown (with operationId): the proposal had been handed to the addon and its outcome is unknown, reconcile it. Type proposal_unavailable (with operationId): the owner could not serve this call and this call sent nothing to the addon; read the proposal for its state, which may already be settled, and repeat the same call with the same ID"
// @Router /api/addons/proposals/{id}/cancel [post]
func CancelProposal(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return proposalCall(g, db, opts, func(c echo.Context, g *gateway.Gateway, a contract.Actor) (gateway.Proposal, error) {
		return g.CancelProposal(c.Request().Context(), a, c.Param("id"))
	})
}

// ReconcileProposal resolves an uncertain outcome by receipt lookup.
// @Summary Resolve the outcome of an addon proposal by receipt lookup
// @Description Looks the operation's receipt up on the owner and records a committed or rejected result. It never applies anything and can be repeated. Use it after an outcome_unknown response. While an outcome is unresolved, other writes on the same resource are denied.
// @Tags Addons
// @Produce json
// @Param id path string true "Proposal ID (the operation ID)"
// @Success 200 {object} gateway.Proposal "Proposal after the lookup"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature denied or the proposal is not the caller's"
// @Failure 503 {object} AddonErrorResponse "Addons, the owner or the addon runtime are unavailable"
// @Router /api/addons/proposals/{id}/reconcile [post]
func ReconcileProposal(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return proposalCall(g, db, opts, func(c echo.Context, g *gateway.Gateway, a contract.Actor) (gateway.Proposal, error) {
		return g.ReconcileProposal(c.Request().Context(), a, c.Param("id"))
	})
}

func proposalCall(g *gateway.Gateway, db *gorm.DB, opts []Option, call func(echo.Context, *gateway.Gateway, contract.Actor) (gateway.Proposal, error)) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		p, e := call(c, g, a)
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, p)
	}
}

// ConfirmProposal applies a pending proposal on explicit human confirmation.
// @Summary Confirm and apply a pending addon proposal
// @Description Applies exactly the stored operation once. This is the only way a stored mutation reaches an addon. It is accepted only from the proposer's own browser session: a same-origin fetch from the host UI that carries the X-LocalAI-Confirm header equal to the proposal ID and the digest of what was shown. API keys, bearer tokens, MCP sessions and requests that carry tool or agent execution markers are refused, and no MCP tool maps to this route. A repeated confirmation returns the stored receipt. If the reply is lost the outcome is outcome_unknown with the original operationId; resolve it with reconcile and never submit the change again.
// @Tags Addons
// @Accept json
// @Produce json
// @Param id path string true "Proposal ID (the operation ID)"
// @Param request body ConfirmRequest true "Digest of the displayed proposal"
// @Success 200 {object} gateway.Proposal "Proposal after the apply, with its receipt"
// @Failure 400 {object} AddonErrorResponse "Malformed or unknown request fields, or type invalid_request when the add-on refused the request as invalid: nothing was applied and the same call is refused again"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human confirmation surface, feature denied, or the proposal is not pending, is expired, stale, changed or blocked"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable. Type outcome_unknown (with operationId): the proposal had been handed to the addon and its outcome is unknown, reconcile it. Type proposal_unavailable (with operationId): the owner could not serve this call and this call sent nothing to the addon; read the proposal for its state, which may already be settled, and repeat the same call with the same ID"
// @Router /api/addons/proposals/{id}/confirm [post]
func ConfirmProposal(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		id := c.Param("id")
		if !humanSurface(c, s, a, id) {
			return failure(c, 403, "Confirmation requires the proposer's own browser session")
		}
		var r ConfirmRequest
		if decode(c, &r) != nil || r.Digest == "" {
			return failure(c, 400, "Invalid addon confirmation")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		p, e := g.ConfirmProposal(c.Request().Context(), a, id, r.Digest)
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, p)
	}
}

// ResolveRequest names what the resource owner was shown and why the owner
// stops waiting.
type ResolveRequest struct {
	// Digest is the digest of the proposal as it was displayed.
	Digest string `json:"digest"`
	// Reason is why the owner stops waiting. Required, at most 512 characters.
	Reason string `json:"reason"`
}

// ResolveProposal records that the resource owner stops waiting for an
// unresolved outcome.
// @Summary Abandon an unresolved addon proposal outcome as the resource owner
// @Description Explicit recovery for an operation whose receipt cannot be found. After outcome_unknown, repeated reconcile calls and the automatic lookups that did not settle it, the owner role of the resource may record that the outcome is not worth waiting for. The proposal becomes abandoned-unknown with the acting owner, the time and the stated reason, and stops blocking writes on the resource. The operation is never submitted again, and a later receipt lookup that says it ran is recorded as applied. The owner is asserting that waiting is not worth it: a change that did run stays in the data, and the revision check of later changes protects against applying on a stale view. Accepted only from a host confirmation surface with the same rules as confirm: the owner's own browser session, a same-origin fetch, the X-LocalAI-Confirm header equal to the proposal ID, and no tool, agent or MCP marker. Installation administrators and API keys have no access through this route, and no MCP tool maps to it.
// @Tags Addons
// @Accept json
// @Produce json
// @Param id path string true "Proposal ID (the operation ID)"
// @Param request body ResolveRequest true "Digest of the displayed proposal and the reason"
// @Success 200 {object} gateway.Proposal "Proposal after the acknowledgement, or settled by a final receipt"
// @Failure 400 {object} AddonErrorResponse "Malformed or unknown request fields, or type invalid_request when the add-on refused the request as invalid: nothing was applied and the same call is refused again"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human confirmation surface, not the resource owner, feature denied, digest changed or the proposal is not unresolved"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable. Always type proposal_unavailable (with operationId), whoever proposed the operation, because resolving is not an apply: the owner could not serve this call and it sent nothing to the addon; read the proposal for its state and repeat the same call with the same ID"
// @Router /api/addons/proposals/{id}/resolve [post]
func ResolveProposal(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		id := c.Param("id")
		if !humanSurface(c, s, a, id) {
			return failure(c, 403, "Resolution requires the resource owner's own browser session")
		}
		var r ResolveRequest
		if decode(c, &r) != nil || r.Digest == "" || r.Reason == "" {
			return failure(c, 400, "Invalid addon resolution")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		p, e := g.ResolveProposal(c.Request().Context(), a, id, r.Digest, r.Reason)
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, p)
	}
}

// ListUnresolvedProposals lists the unresolved proposals of a resource for its
// owner role.
// @Summary List the unresolved addon proposals of a resource as its owner
// @Description Returns the proposals on the resource whose outcome is unresolved (executing, outcome-unknown) or abandoned-unknown, whoever proposed them, so the resource owner can learn the operation ID and digest that resolve needs. Each entry has the proposal ID, state, the proposing user, the action, the exact effect count and the bounded effect sample the proposer saw, the expiry, the digest and the age in seconds. It never returns the proposed arguments or any credential. Read-only. Only the owner role of the resource may call it; installation administrators, editors and viewers are denied. It is accepted only from a host confirmation surface with the rules of resolve: the owner's own browser session, a same-origin fetch, the X-LocalAI-Confirm header equal to the resource ID, and no tool, agent or MCP marker. API keys are refused and no MCP tool maps to this route.
// @Tags Addons
// @Produce json
// @Param resourceId path string true "Resource ID"
// @Param state query string true "Only unresolved is supported" Enums(unresolved)
// @Success 200 {object} gateway.ProposalList "Unresolved proposals, oldest first"
// @Failure 400 {object} AddonErrorResponse "Missing or unsupported state filter"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human confirmation surface, not the owner role of the resource, feature denied or unknown resource"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/resources/{resourceId}/proposals [get]
func ListUnresolvedProposals(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		resourceID := c.Param("resourceId")
		if !humanSurface(c, s, a, resourceID) {
			return failure(c, 403, "Listing requires the resource owner's own browser session")
		}
		if c.QueryParam("state") != "unresolved" {
			return failure(c, 400, "Only state=unresolved is supported")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		list, e := g.UnresolvedProposals(c.Request().Context(), a, resourceID)
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, list)
	}
}
