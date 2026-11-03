// SPDX-License-Identifier: MIT
package addons

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
)

// The host access routes are the UI-only half of the host operation policy:
// what each add-on may ask the host to do, the deny rules and the blanket
// approval. They are never tools. No MCP tool, model tool or proposal maps to
// them. Reads take an administrator's session or API key. Every write takes
// only the administrator's own browser session (same-origin fetch, the
// X-LocalAI-Confirm header equal to the operation ID); an API key, a bearer
// token, an MCP session or a tool call is refused, whatever its role. In a
// split deployment the frontend decides that and forwards the decision to the
// owner as a signed field; only the owner enforces and stores.

// hostGrantsAdmin runs the authentication and the administrator check, which
// come before the body is read.
func hostGrantsAdmin(c echo.Context, db *gorm.DB, s settings) (contract.Actor, error, bool) {
	privateHeaders(c)
	a, e := actor(c, db, s)
	if e != nil {
		return a, denied(c, e), false
	}
	if e = administrator(c, db, a, s); e != nil {
		if errors.Is(e, gateway.ErrUnavailable) {
			return a, failure(c, 503, "Addons unavailable"), false
		}
		return a, failure(c, 403, "Addon administration required"), false
	}
	return a, nil, true
}

// hostGrantsFailure writes the answer for an error of a host access call.
func hostGrantsFailure(c echo.Context, e error) error {
	var he *gateway.HostGrantsError
	if errors.As(e, &he) {
		return c.JSON(he.Status, AddonErrorResponse{Error: AddonError{Type: he.Type, Code: he.Status, Message: he.Message}})
	}
	var ue *gateway.UnavailableError
	if errors.As(e, &ue) {
		return failure(c, 503, ue.Reason)
	}
	return resultError(c, e)
}

// The value of the confirm header on each write. It names what the
// administrator acted on, so a form post or a link, which cannot set a custom
// header, cannot make the call: addonId@digest for an install, the add-on ID
// for an edit or a revoke, "blanket" and "deny" for the two switches.
const (
	hostGrantsConfirmBlanket = "blanket"
	hostGrantsConfirmDeny    = "deny"
)

// hostGrantsDo checks the human surface of a write (confirm is the expected
// header value; empty for a read), runs the call and writes the data of the
// reply.
func hostGrantsDo(c echo.Context, g *gateway.Gateway, s settings, a contract.Actor, call gateway.HostGrantsCall, confirm string) error {
	ctx := c.Request().Context()
	if confirm != "" || call.OperationID != "" {
		if !humanSurface(c, s, a, confirm) {
			// Hand the refused write to the owner without the human mark, so it
			// answers not_human_surface and writes the refusal to the audit file
			// with the kind of credential, for a caller that is not a person
			// (API key, MCP session, tool call). The owner refuses it; the
			// answer below is the same in either case.
			if g != nil {
				_, _ = g.HostGrants(ctx, a, call)
			}
			return c.JSON(403, AddonErrorResponse{Error: AddonError{Type: gateway.HostGrantsNotHumanSurface, Code: 403, Message: "This action requires your own browser session"}})
		}
		ctx = gateway.WithHumanSurface(ctx)
	}
	if g == nil {
		return failure(c, 503, "Addons unavailable")
	}
	raw, e := g.HostGrants(ctx, a, call)
	if e != nil {
		return hostGrantsFailure(c, e)
	}
	return c.Blob(200, echo.MIMEApplicationJSON, raw)
}

func validOperationID(id string) bool { return id != "" && len(id) <= 128 }

// newHostGrantsOperationID makes the operation ID of a write whose caller sent
// none. A caller that wants a retry to be the same change sends its own.
func newHostGrantsOperationID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "hg-" + hex.EncodeToString(raw[:]), nil
}

// hostGrantsBody is the shared front of a route with a JSON body:
// administrator check, strict decode, operation ID, call. confirm gives the
// expected confirm header of a write ("" for a read); op may be changed by
// prepare (the deny dry run is a read).
func hostGrantsBody[T any](g *gateway.Gateway, db *gorm.DB, opts []Option, op string,
	opOf func(*T) *string, addon func(echo.Context) string, confirm func(echo.Context, *T) string, prepare func(*T) string) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, err, ok := hostGrantsAdmin(c, db, s)
		if !ok {
			return err
		}
		var req T
		if decode(c, &req) != nil {
			return failure(c, 400, "Invalid host access request")
		}
		callOp := op
		if prepare != nil {
			callOp = prepare(&req)
		}
		write := callOp != gateway.HostGrantsPreview && callOp != gateway.HostGrantsDenyPreview
		id := opOf(&req)
		want := ""
		if write {
			if *id == "" {
				var e error
				if *id, e = newHostGrantsOperationID(); e != nil {
					return failure(c, 503, "Addons unavailable")
				}
			}
			if !validOperationID(*id) {
				return failure(c, 400, "Invalid operationId")
			}
			want = confirm(c, &req)
		} else {
			*id = ""
		}
		body, e := json.Marshal(req)
		if e != nil {
			return failure(c, 400, "Invalid host access request")
		}
		call := gateway.HostGrantsCall{Op: callOp, Body: body}
		if write {
			call.OperationID = *id
		}
		if addon != nil {
			call.AddonID = addon(c)
		}
		noteRequest(c, contract.Request{Action: "hostgrants-" + callOp, AddonID: call.AddonID})
		return hostGrantsDo(c, g, s, a, call, want)
	}
}

func addonParam(c echo.Context) string { return c.Param("addonId") }

// GetHostGrants reads the host access state.
// @Summary Read the host access policy and what each add-on holds (administrators)
// @Description Administrators only (an administrator's session or API key). Returns the revision of the grants document, whether the deployment locks the UI store, the blanket approval switch (mode off, official or any; setBy ui or deployment), the deployment floor (the policy file and flags, read-only), the deny rules by source (file and flags, which cannot be edited here, and ui), and for each installed add-on the operations its verified manifest requests (required and optional) with a plain title, text and risk, whether each is allowed, not_allowed, blocked or unsupported, where the grant comes from (deployment, install, upgrade, edit or blanket), when and by whom, the operations granted that the manifest no longer requests, an identity mismatch flag and a pending upgrade that waits for approval. Read-only. Nothing in the answer is a credential. No MCP tool maps to this route.
// @Tags Addons
// @Produce json
// @Success 200 {object} gateway.HostGrantsView "Host access state"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature or administration denied"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/host-grants [get]
func GetHostGrants(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, err, ok := hostGrantsAdmin(c, db, s)
		if !ok {
			return err
		}
		return hostGrantsDo(c, g, s, a, gateway.HostGrantsCall{Op: gateway.HostGrantsRead}, "")
	}
}

// PreviewHostGrants previews what an install would ask for.
// @Summary Preview the host access an install or upgrade would ask for (administrators)
// @Description Administrators only (an administrator's session or API key). The owner resolves and stages the release, reads the verified manifest of the staged package, computes the answer against the policy and discards the stage. Returns the digest of the staged package, the requested operations with plain text and risk, and for each one held (already allowed), grantable, blocked (a deny rule, which wins over every grant) or unsupported; canInstall false with blockedRequired when a required operation is blocked; whether the blanket approval would grant on install; the revision of the grants document; and a single-use nonce (five minutes, bound to the caller's credential, the add-on, the digest and the revision) that the install must present. Operations granted before an earlier uninstall carry hinted true so the dialog may tick them; a hint grants nothing. Changes no state.
// @Tags Addons
// @Accept json
// @Produce json
// @Param request body gateway.HostGrantsPreviewRequest true "Release to preview"
// @Success 200 {object} gateway.HostGrantsPreviewView "What the release asks for"
// @Failure 400 {object} AddonErrorResponse "Malformed or unknown request fields"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature or administration denied, or the release could not be verified"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/host-grants/preview [post]
func PreviewHostGrants(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return hostGrantsBody(g, db, opts, gateway.HostGrantsPreview, func(*gateway.HostGrantsPreviewRequest) *string { var none string; return &none }, nil, nil, nil)
}

// InstallWithHostGrants installs or upgrades with an approval.
// @Summary Install or upgrade an add-on with the host access the administrator approved (browser session)
// @Description Administrators only, from their own browser session (same-origin fetch, X-LocalAI-Confirm equal to addonId@digest, the add-on and the digest of the approved package). API keys, bearer tokens, MCP sessions and tool calls are refused with 403 not_human_surface. Carries the approval the dialog collected: the digest of the previewed package, the operations ticked, the revision of the grants document and the preview nonce. The owner stages the package again and grants only the operations that are approved AND declared by that verified manifest AND registered by the host AND not denied; a different package is 409 approval_stale, a changed document 409 revision_conflict, an expired or reused nonce 409 approval_stale. A required operation that is neither held nor approved is refused (approval_required) and one that a deny rule blocks is 403 blocked_by_policy; nothing is written then. For action upgrade, expectedDigest and candidateDigest pin the upgrade as in the plain upgrade. The plain dispatch install carries no approval. Repeating the operationId with the same request returns the recorded operation. After outcome_unknown keep the operationId and poll the operation status.
// @Tags Addons
// @Accept json
// @Produce json
// @Param request body gateway.HostGrantsInstallRequest true "Install with approval"
// @Success 200 {object} addons.OperationStatus "The lifecycle operation, with the host operations summary"
// @Failure 400 {object} AddonErrorResponse "Malformed request"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Not a human surface (not_human_surface), locked_by_deployment, blocked_by_policy, approval_required, or administration denied"
// @Failure 409 {object} AddonErrorResponse "revision_conflict or approval_stale"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or outcome_unknown (with operationId)"
// @Router /api/addons/host-grants/install [post]
func InstallWithHostGrants(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return hostGrantsBody(g, db, opts, gateway.HostGrantsInstall, func(r *gateway.HostGrantsInstallRequest) *string { return &r.OperationID }, nil,
		func(_ echo.Context, r *gateway.HostGrantsInstallRequest) string {
			return r.AddonID + "@" + r.Approve.Digest
		}, nil)
}

// EditHostGrants replaces the UI grants of an add-on.
// @Summary Set the host operations an add-on keeps through the UI (browser session)
// @Description Administrators only, from their own browser session (X-LocalAI-Confirm equal to the add-on ID). operationId is optional (the host makes one); send your own to make a retry the same change. ops is the full set the add-on keeps: operations left out are revoked, new ones must be declared by the installed manifest, registered by the host and not denied (403 blocked_by_policy). Grants made by the policy file or flags are not editable here. keep names held blanket grants to relabel as the administrator's own. revision is the revision the caller read; a stale one is 409 revision_conflict. Takes effect for the next call of the add-on with no restart.
// @Tags Addons
// @Accept json
// @Produce json
// @Param addonId path string true "Add-on ID"
// @Param request body gateway.HostGrantsEditRequest true "Edit"
// @Success 200 {object} gateway.HostGrantsAddonResult "The add-on entry and the new revision"
// @Failure 400 {object} AddonErrorResponse "Malformed request"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "not_human_surface, locked_by_deployment, blocked_by_policy or administration denied"
// @Failure 404 {object} AddonErrorResponse "The add-on is not installed"
// @Failure 409 {object} AddonErrorResponse "revision_conflict or operation_conflict"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or outcome_unknown (with operationId)"
// @Router /api/addons/host-grants/{addonId} [put]
func EditHostGrants(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return hostGrantsBody(g, db, opts, gateway.HostGrantsEdit, func(r *gateway.HostGrantsEditRequest) *string { return &r.OperationID }, addonParam,
		func(c echo.Context, _ *gateway.HostGrantsEditRequest) string { return c.Param("addonId") }, nil)
}

// RevokeHostGrants removes the UI grants of an add-on.
// @Summary Revoke every host operation granted to an add-on through the UI (browser session)
// @Description Administrators only, from their own browser session (X-LocalAI-Confirm equal to the add-on ID). Removes the UI and blanket grants of the add-on; grants of the policy file stay. Query: revision (the revision the caller read) and, optionally, operationId. Takes effect for the next call with no restart; a running call finishes. An add-on whose required operation is revoked becomes unavailable.
// @Tags Addons
// @Produce json
// @Param addonId path string true "Add-on ID"
// @Param revision query integer true "Revision of the grants document the caller read"
// @Param operationId query string false "Operation ID (optional)"
// @Success 200 {object} gateway.HostGrantsRevision "The new revision"
// @Failure 400 {object} AddonErrorResponse "Malformed request"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "not_human_surface, locked_by_deployment or administration denied"
// @Failure 404 {object} AddonErrorResponse "The add-on is not installed"
// @Failure 409 {object} AddonErrorResponse "revision_conflict or operation_conflict"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or outcome_unknown (with operationId)"
// @Router /api/addons/host-grants/{addonId} [delete]
func RevokeHostGrants(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, err, ok := hostGrantsAdmin(c, db, s)
		if !ok {
			return err
		}
		rev, perr := strconv.ParseUint(c.QueryParam("revision"), 10, 64)
		id := c.QueryParam("operationId")
		if id == "" {
			var e error
			if id, e = newHostGrantsOperationID(); e != nil {
				return failure(c, 503, "Addons unavailable")
			}
		}
		if perr != nil || !validOperationID(id) {
			return failure(c, 400, "revision is required")
		}
		body, _ := json.Marshal(gateway.HostGrantsRevokeRequest{OperationID: id, Revision: rev})
		call := gateway.HostGrantsCall{Op: gateway.HostGrantsRevoke, OperationID: id, AddonID: c.Param("addonId"), Body: body}
		noteRequest(c, contract.Request{Action: "hostgrants-revoke", AddonID: call.AddonID})
		return hostGrantsDo(c, g, s, a, call, c.Param("addonId"))
	}
}

// SetHostGrantsBlanket changes the blanket approval.
// @Summary Turn the blanket approval on, off or wider (browser session, typed confirmation)
// @Description Administrators only, from their own browser session (X-LocalAI-Confirm equal to "blanket"). Mode off, official or any. With it on, an install or upgrade grants the add-on the operations its verified manifest declares as required (also the optional ones with includeOptional) without the dialog step; it never grants an undeclared or unregistered operation and never overrides a deny rule. Mode official covers only the official library with its official signer. Mode any covers every library the administrator configured: ANY LIBRARY ENTRY THEN RECEIVES EXACTLY THE OPERATIONS ITS MANIFEST DECLARES, with no review, so a malicious or compromised entry gets what it asks for. Turning it on, or widening it, needs confirm equal to "grant official add-ons what they declare" (official) or "grant any library add-on what it declares" (any), else 400 confirmation_mismatch. applyToInstalled also grants the installed add-ons in the same revision. Turning it off keeps the grants it made, marked blanket; revokeOnOff revokes them all. When the deployment fixes the mode the answer is 403 locked_by_deployment.
// @Tags Addons
// @Accept json
// @Produce json
// @Param request body gateway.HostGrantsBlanketRequest true "Blanket change"
// @Success 200 {object} gateway.HostGrantsBlanketResult "The switch, the add-ons that were granted or skipped and the add-ons whose blanket grants were revoked"
// @Failure 400 {object} AddonErrorResponse "Malformed request, or confirmation_mismatch"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "not_human_surface, locked_by_deployment or administration denied"
// @Failure 409 {object} AddonErrorResponse "revision_conflict or operation_conflict"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or outcome_unknown (with operationId)"
// @Router /api/addons/host-grants/blanket [put]
func SetHostGrantsBlanket(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return hostGrantsBody(g, db, opts, gateway.HostGrantsBlanket, func(r *gateway.HostGrantsBlanketRequest) *string { return &r.OperationID }, nil,
		func(echo.Context, *gateway.HostGrantsBlanketRequest) string { return hostGrantsConfirmBlanket }, nil)
}

// SetHostGrantsDeny replaces the UI deny rules.
// @Summary Replace the deny rules set in the UI, or preview their effect (browser session)
// @Description Administrators only. A deny rule beats every grant. deny maps an add-on ID, or "*" for all, to prefixes ("egress."), exact operation names or the single token "*". The rules of the policy file and flags are not editable here and are unioned with these. With dryRun true the answer is the affected add-ons only (affects: the operations each would lose and whether it will stop because a required one is lost) and nothing changes; a dry run needs the administrator but not the browser session. Without dryRun the change needs the administrator's own browser session (X-LocalAI-Confirm equal to "deny"), the revision the caller read (409 revision_conflict otherwise), and applies to the next call with no restart.
// @Tags Addons
// @Accept json
// @Produce json
// @Param request body gateway.HostGrantsDenyRequest true "Deny rules"
// @Success 200 {object} gateway.HostGrantsDenyResult "The rules and what they take"
// @Failure 400 {object} AddonErrorResponse "Malformed request or invalid rule"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "not_human_surface, locked_by_deployment or administration denied"
// @Failure 409 {object} AddonErrorResponse "revision_conflict or operation_conflict"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or outcome_unknown (with operationId)"
// @Router /api/addons/host-grants/deny [put]
func SetHostGrantsDeny(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return hostGrantsBody(g, db, opts, gateway.HostGrantsDeny, func(r *gateway.HostGrantsDenyRequest) *string { return &r.OperationID }, nil,
		func(echo.Context, *gateway.HostGrantsDenyRequest) string { return hostGrantsConfirmDeny },
		func(r *gateway.HostGrantsDenyRequest) string {
			if r.DryRun {
				// A dry run is a read and has no operation of its own.
				return gateway.HostGrantsDenyPreview
			}
			return gateway.HostGrantsDeny
		})
}
