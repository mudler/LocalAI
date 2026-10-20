// SPDX-License-Identifier: MIT
package addons

import (
	"encoding/json"
	"errors"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
	"io"
	"net/http"
)

// AddonError is the error object of every failure response of the addon endpoints.
type AddonError struct {
	// Type is addon_error, outcome_unknown when a mutation may have run,
	// proposal_unavailable when a proposal call could not be served and the
	// same call can be repeated by the same proposal ID after reading it, or
	// outcome_blocked (403) when another operation on the resource has an
	// unresolved outcome and the caller is the resource's owner role. The
	// resource management routes add sharing_disabled (409, local-owner mode
	// has no named users to share with), stale (409, the generation or the
	// revision the caller saw is out of date), operation_conflict (409, the
	// operation ID was used for another request and nothing was applied),
	// invalid_request (400, for
	// example a missing deletion confirmation or an unsupported operation),
	// quota_reached (403) and not_selected (404, no resource is selected for
	// the conversation).
	Type string `json:"type" example:"outcome_unknown"`
	// Message describes the failure and never carries owner details.
	Message string `json:"message"`
	// Code repeats the HTTP status. It is absent from outcome_unknown responses.
	Code int `json:"code,omitempty"`
	// OperationID is the original operation, which is the proposal ID for a
	// proposal call. It is present only with type outcome_unknown or
	// proposal_unavailable.
	OperationID string `json:"operationId,omitempty"`
	// BlockedBy is the unresolved operation that blocks the write. It is
	// present only with type outcome_blocked, and only for the owner role of
	// the resource.
	BlockedBy string `json:"blockedBy,omitempty"`
}

// AddonErrorResponse is the body of every failure response of the addon endpoints.
type AddonErrorResponse struct {
	Error AddonError `json:"error"`
}

func failure(c echo.Context, code int, message string) error {
	return c.JSON(code, AddonErrorResponse{Error: AddonError{Code: code, Message: message, Type: "addon_error"}})
}

// Option configures the deployment-level behaviour of a handler.
type Option func(*settings)

type settings struct{ mode string }

// WithOwnership selects the ownership mode the deployment configured. The
// default is named-user; local-owner is never inferred from a request.
func WithOwnership(mode string) Option {
	return func(s *settings) { s.mode = mode }
}

func resolve(opts []Option) settings {
	s := settings{mode: auth.AddonModeNamedUser}
	for _, o := range opts {
		o(&s)
	}
	return s
}

func actor(c echo.Context, db *gorm.DB, s settings) (contract.Actor, error) {
	a, e := auth.AddonActorForMode(c, s.mode)
	if e != nil {
		return a, e
	}
	e = auth.AddonRevalidate(db, s.mode)(c.Request().Context(), a)
	return a, e
}

// administrator checks the lifecycle mutation right against current storage.
// In local-owner mode every caller shares the owner's authority, so without an
// account database there is no role to check; with one, the authenticated
// caller must still be an administrator.
func administrator(c echo.Context, db *gorm.DB, a contract.Actor, s settings) error {
	if s.mode == auth.AddonModeLocalOwner {
		if db == nil {
			return nil
		}
		named, e := auth.AddonActor(c)
		if e != nil {
			return e
		}
		a = named
	}
	return auth.ValidateAddonActor(c.Request().Context(), db, a, true)
}

// denied reports a failed identity step: a store outage is unavailable, any
// other failure is an authentication problem.
func denied(c echo.Context, e error) error {
	if errors.Is(e, gateway.ErrUnavailable) {
		return failure(c, 503, "Addons unavailable")
	}
	return failure(c, 401, "Named authentication required")
}
func decode(c echo.Context, v any) error {
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, 2<<20)
	d := json.NewDecoder(c.Request().Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return gateway.ErrForbidden
	}
	return nil
}
func resultError(c echo.Context, e error) error {
	// The add-on's own refusal of a request is a typed 400 from every path,
	// never an outage banner.
	e = gateway.Refused(e)
	var unknown *gateway.OutcomeUnknown
	if errors.As(e, &unknown) {
		return c.JSON(503, AddonErrorResponse{Error: AddonError{Type: "outcome_unknown", Message: "Check operation status; do not submit a new mutation", OperationID: unknown.OperationID}})
	}
	var blocked *gateway.OutcomeBlocked
	if errors.As(e, &blocked) && blocked.BlockedBy != "" && gateway.StatusFor(e) == http.StatusForbidden {
		return c.JSON(403, AddonErrorResponse{Error: AddonError{Type: "outcome_blocked", Code: 403, Message: "Another operation on the resource has an unresolved outcome; resolve it first", BlockedBy: blocked.BlockedBy}})
	}
	var unavailable *gateway.ProposalUnavailable
	if errors.As(e, &unavailable) {
		return c.JSON(503, AddonErrorResponse{Error: AddonError{Type: "proposal_unavailable", Message: "Proposal call unavailable; read the proposal and repeat the same call with the same proposal ID", OperationID: unavailable.ProposalID}})
	}
	if gateway.StatusFor(e) == http.StatusServiceUnavailable {
		return failure(c, 503, "Addon owner unavailable")
	}
	// An add-on built for another SDK range is refused at install. The
	// administrator gets that text and no other internal error text.
	var refusal *contract.SDKRefusal
	if errors.As(e, &refusal) {
		return c.JSON(403, AddonErrorResponse{Error: AddonError{Type: "sdk_incompatible", Code: 403, Message: refusal.Error()}})
	}
	for _, t := range []struct {
		err     error
		status  int
		kind    string
		message string
	}{
		{gateway.ErrSharingDisabled, 409, "sharing_disabled", "Sharing is disabled: this installation has no named users to share with"},
		{gateway.ErrStale, 409, "stale", "The view is out of date; reload it and repeat the action"},
		{gateway.ErrOperationConflict, 409, "operation_conflict", "The operation ID was used for another request; nothing was applied. Repeat the change under a new operation ID"},
		{gateway.ErrInvalidRequest, 400, "invalid_request", "The request cannot be carried out as given"},
		{gateway.ErrQuotaReached, 403, "quota_reached", "The resource limit is reached"},
		{gateway.ErrNotSelected, 404, "not_selected", "No resource is selected for the conversation"},
	} {
		if errors.Is(e, t.err) {
			return c.JSON(t.status, AddonErrorResponse{Error: AddonError{Type: t.kind, Code: t.status, Message: t.message}})
		}
	}
	return failure(c, 403, "Addon request denied")
}

// Dispatch forwards typed operations to the explicitly configured owner.
// @Summary Submit a typed addon lifecycle operation or read an addon resource
// @Description Authenticates the named caller, checks the addons feature, requires administration for every action except read, and forwards the typed request to the designated addon owner. A mutation is never retried: after an outcome_unknown response keep the operationId and poll operation-status instead of submitting a new mutation. Lifecycle actions are install, upgrade, enable, disable and uninstall (arguments sourceId and version; enable and disable take the generation that was listed). The management reads, all administrator-only and never mutations, are catalog-search (arguments text, sourceId, offset, limit up to 100), catalog-details (addonId plus arguments sourceId and version), catalog-sources and addons-installed. The library changes are catalog-source-save (arguments are one source: id, name, url, enabled, policy, authReference; removed true deletes it and never touches installed addons) and catalog-source-refresh (argument sourceId). A library names a credential only by a reference the owner configured, never a secret. Unknown argument fields are denied. The update actions are update-status and update-check (no arguments; the check fetches the library again and applies nothing) and update-upgrade (arguments expectedDigest and candidateDigest, the pinned upgrade); each answers with a payload {error: {type, message, status}} or {data}, the same from every frontend. The browser pages use the dedicated routes under /api/addons/{id}/update and /api/addons/{id}/upgrade, which accept only an administrator's own browser session.
// @Tags Addons
// @Accept json
// @Produce json
// @Param request body contract.Request true "Typed addon request"
// @Success 200 {object} contract.Result "Operation accepted or read result"
// @Failure 400 {object} AddonErrorResponse "Malformed or unknown request fields, or type invalid_request when the add-on refused the request as invalid: nothing was applied and the same call is refused again"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature, administration or owner policy denied the request, or type sdk_incompatible when an install is refused because the add-on needs another SDK version: the message names the range the add-on needs and the SDK version the host provides"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable, or the outcome of a mutation is unknown (type outcome_unknown, with operationId)"
// @Router /api/addons/dispatch [post]
func Dispatch(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		var r contract.Request
		if decode(c, &r) != nil {
			return failure(c, 400, "Invalid addon request")
		}
		// Recovery and update mutations have their own routes, which accept
		// only the administrator's own browser session. The generic dispatch
		// also takes API keys, so it must not carry them.
		if (gateway.RecoveryAction(r.Action) && r.Action != gateway.ActionRecoveryStatus) ||
			(gateway.UpdateAction(r.Action) && r.Action != gateway.ActionUpdateStatus) {
			return failure(c, 403, "Addon request denied")
		}
		if r.Action != "read" {
			if e = administrator(c, db, a, s); e != nil {
				if errors.Is(e, gateway.ErrUnavailable) {
					return failure(c, 503, "Addons unavailable")
				}
				return failure(c, 403, "Addon administration required")
			}
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		out, e := g.Dispatch(c.Request().Context(), a, r)
		if e != nil {
			return resultError(c, e)
		}
		return c.JSON(200, out)
	}
}

// Relay keeps all streams on the receiving frontend.
// @Summary Relay addon status, lifecycle progress, JSON, attachments or immutable UI assets
// @Description Streams the response of the designated addon owner through the receiving frontend, bounded per kind by the configured byte and time limits. The kind selects the response: status (JSON), progress (text/event-stream), json, file (a private file result as an attachment: the handle is the artifact ID that a read returned, never an addon handle) or asset (text/javascript or text/css). An owner decision made before the first byte is returned with the same status from every frontend; a failure after the first byte aborts the stream.
// @Tags Addons
// @Accept json
// @Produce json
// @Produce text/event-stream
// @Produce application/octet-stream
// @Produce text/javascript
// @Produce text/css
// @Param request body gateway.RelayRequest true "Relay request"
// @Success 200 {string} string "Relayed response body"
// @Failure 400 {object} AddonErrorResponse "Malformed or unknown request fields, or type invalid_request when the add-on refused the request as invalid: nothing was applied and the same call is refused again"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature or owner policy denied the request"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/relay [post]
func Relay(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		var r gateway.RelayRequest
		if decode(c, &r) != nil {
			return failure(c, 400, "Invalid addon relay")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		e = g.Relay(c.Request().Context(), a, r, c.Response())
		if e != nil {
			if c.Response().Committed {
				panic(http.ErrAbortHandler)
			}
			return resultError(c, e)
		}
		return nil
	}
}

// BrowserProtection is stricter than the legacy CSRF compatibility skipper:
// cookie mutations without fetch metadata are rejected, not assumed safe.
func BrowserProtection() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			h := c.Request().Header
			_, session := c.Cookie("session")
			// The legacy token cookie carries an API key. It is a browser
			// credential too, unless a header credential is sent with it.
			_, token := c.Cookie("token")
			headerCredential := h.Get("Authorization") != "" || h.Get("X-Api-Key") != "" || h.Get("Xi-Api-Key") != ""
			if (session == nil || (token == nil && !headerCredential)) && h.Get("Sec-Fetch-Site") != "same-origin" {
				// The global CSRF check lets same-site requests through, so
				// a sibling origin could drive an administrator's cookie.
				return failure(c, 403, "Same-origin browser request required")
			}
			return next(c)
		}
	}
}
