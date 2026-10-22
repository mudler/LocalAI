// SPDX-License-Identifier: MIT
package addons

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	_ "github.com/mudler/LocalAI/core/services/addons/updates" // the status documented by swagger
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
)

// The update routes. The status is a read for every caller with the addons
// feature; the owner decides what each one sees. The check and the upgrade are
// the administrator's own action from their own browser session. They are
// never tools: no MCP tool, model tool or typed dispatch of a model maps to
// them, and the owner decides every one of them again.

// UpdateCheckRequest asks the owner to read the library again.
type UpdateCheckRequest struct {
	// OperationID identifies the request and is the confirmation header.
	OperationID string `json:"operationId" example:"check-notes-1"`
}

// UpgradeRequest asks for the explicit upgrade of an add-on to the candidate
// the caller checked.
type UpgradeRequest struct {
	// OperationID is the identity of the upgrade: repeat it to replay safely
	// and, after an outcome_unknown answer, poll operation-status with it. The
	// same transition under another operation ID is refused.
	OperationID string `json:"operationId" example:"upgrade-notes-1"`
	// ExpectedDigest is the digest of the installed package the caller saw.
	ExpectedDigest string `json:"expectedDigest" example:"sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"`
	// CandidateDigest is the digest of the candidate the check resolved.
	CandidateDigest string `json:"candidateDigest" example:"sha256:fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"`
	// Generation is the installed generation the caller saw, when it knows it.
	Generation uint64 `json:"generation,omitempty" example:"1"`
}

// updateCall runs one update action on the owner and writes its answer. The
// owner's typed refusal is part of its result, so every frontend gives the
// same status and type.
func updateCall(c echo.Context, g *gateway.Gateway, a contract.Actor, id, opID, action string, generation uint64, args any, wrap bool) error {
	raw, e := json.Marshal(args)
	if e != nil {
		return failure(c, 400, "Invalid update request")
	}
	out, e := g.Dispatch(c.Request().Context(), a, contract.Request{AddonID: id, OperationID: opID, Generation: generation, Action: action, Arguments: raw})
	if e != nil {
		return resultError(c, e)
	}
	var env gateway.UpdateEnvelope
	if json.Unmarshal(out.Payload, &env) != nil {
		return failure(c, 503, "Addon owner unavailable")
	}
	if env.Error != nil {
		code := env.Error.Status
		if code < 400 || code > 599 {
			code = http.StatusServiceUnavailable
		}
		return c.JSON(code, AddonErrorResponse{Error: AddonError{Type: env.Error.Type, Message: env.Error.Message, Code: code}})
	}
	if wrap {
		// The answer of a lifecycle action is the operation status in a
		// result, as every other lifecycle route gives it.
		return c.JSON(200, contract.Result{Payload: env.Data})
	}
	return c.Blob(200, echo.MIMEApplicationJSON, env.Data)
}

// GetUpdate reads the update status of an add-on.
// @Summary Read the update status of an addon
// @Description Returns the owner's update check for an installed add-on, for every authenticated caller who holds the addons feature and may see the add-on (an administrator any installed add-on, others only an enabled one; anything else is the same 403). The status is computed by the owner from the library the add-on was installed from and never from the browser: state is unknown, up-to-date, update-available, incompatible (with reasons: platform, host-version, api-version, protocol-version, sdk-version or data-format), check-failed (with a redacted error class and the time the library was last read) or integrity-error. A failed check is never up-to-date. A moving channel (development, a branch, latest) compares resolved digests, so a changed digest with the same version label is an update; an immutable version selects only a greater compatible version of the same major. The library and channel never change by themselves. The route never uses the network and never applies anything. Callers who are not administrators get the state, versions and times only, without library, channel or digests. No MCP tool maps to this route.
// @Tags Addons
// @Produce json
// @Param id path string true "Add-on ID"
// @Success 200 {object} updates.Status "Update status"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature denied, or the add-on is unknown or hidden"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/{id}/update [get]
func GetUpdate(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		privateHeaders(c)
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		id := c.Param("id")
		if !appID.MatchString(id) {
			return failure(c, 403, "Addon request denied")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		var raw [16]byte
		if _, e = rand.Read(raw[:]); e != nil {
			return failure(c, 503, "Addons unavailable")
		}
		return updateCall(c, g, a, id, "u"+hex.EncodeToString(raw[:]), gateway.ActionUpdateStatus, 0, struct{}{}, false)
	}
}

// updateMutation is the shared front of the check and the upgrade:
// authentication, administration (before the body is read) and the human
// surface bound to the operation ID.
func updateMutation(g *gateway.Gateway, db *gorm.DB, opts []Option, action string, wrap bool, body func() any, opOf func(any) string, argsOf func(any) (uint64, any)) echo.HandlerFunc {
	s := resolve(opts)
	return func(c echo.Context) error {
		privateHeaders(c)
		a, e := actor(c, db, s)
		if e != nil {
			return denied(c, e)
		}
		if e = administrator(c, db, a, s); e != nil {
			if errors.Is(e, gateway.ErrUnavailable) {
				return failure(c, 503, "Addons unavailable")
			}
			return failure(c, 403, "Addon administration required")
		}
		id := c.Param("id")
		if !appID.MatchString(id) {
			return failure(c, 403, "Addon request denied")
		}
		req := body()
		if decode(c, req) != nil {
			return failure(c, 400, "Invalid update request")
		}
		opID := opOf(req)
		if opID == "" || len(opID) > 256 {
			return failure(c, 400, "An operationId is required")
		}
		if !humanSurface(c, s, a, opID) {
			return failure(c, 403, "This action requires your own browser session")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		generation, args := argsOf(req)
		return updateCall(c, g, a, id, opID, action, generation, args, wrap)
	}
}

// CheckUpdate reads the library again.
// @Summary Check an addon for updates now (administrators, browser session)
// @Description Administrators only, from their own browser session (same-origin fetch, X-LocalAI-Confirm equal to the operationId). The owner fetches the library the add-on was installed from again, through the credential reference the library is configured with, and answers with the new update status. It is rate limited per add-on and per library: within the interval the last status is returned with throttled true. It applies nothing; an upgrade is a separate explicit request. No MCP tool maps to this route.
// @Tags Addons
// @Accept json
// @Produce json
// @Param id path string true "Add-on ID"
// @Param request body UpdateCheckRequest true "Operation"
// @Success 200 {object} updates.Status "Update status"
// @Failure 400 {object} AddonErrorResponse "Malformed request"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature, administration or browser session denied"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/{id}/update/check [post]
func CheckUpdate(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return updateMutation(g, db, opts, gateway.ActionUpdateCheck, false,
		func() any { return &UpdateCheckRequest{} },
		func(v any) string { return v.(*UpdateCheckRequest).OperationID },
		func(any) (uint64, any) { return 0, struct{}{} })
}

// UpgradeAddon upgrades to the checked candidate.
// @Summary Upgrade an addon to the candidate that was checked (administrators, browser session)
// @Description Administrators only, from their own browser session (same-origin fetch, X-LocalAI-Confirm equal to the operationId). The only action that applies an update. It carries the digest of the installed package the caller saw (expectedDigest) and the digest of the candidate the check resolved (candidateDigest). The owner re-resolves the library the add-on came from and refuses, before it records anything, an installed package that is not the expected one (409 stale_installed), a candidate that is not the one it resolves now (409 candidate_changed), an incompatible candidate (409 incompatible), a rewritten immutable version (409 integrity_error) and the same transition under another operation ID (409 upgrade_conflict). It never changes the library or the channel. The upgrade then follows the lifecycle of an add-on: the candidate is staged and verified, the data is backed up and migrated when the add-on has any, and the new generation is published only when it starts. A failure or cancellation keeps the last good generation or leaves an explicit recovery state; the previous installation is never deleted. Repeating the operationId returns the recorded result and never runs again; after outcome_unknown read operation-status with the same operationId. No MCP tool maps to this route.
// @Tags Addons
// @Accept json
// @Produce json
// @Param id path string true "Add-on ID"
// @Param request body UpgradeRequest true "Operation and the pinned digests"
// @Success 200 {object} contract.Result "Operation accepted; the payload is the operation status"
// @Failure 400 {object} AddonErrorResponse "Malformed request"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature, administration or browser session denied"
// @Failure 409 {object} AddonErrorResponse "Type stale_installed, candidate_changed, incompatible, integrity_error, upgrade_conflict, upgrade_failed or operation_conflict"
// @Failure 503 {object} AddonErrorResponse "Unavailable, check_failed, or outcome_unknown (with operationId)"
// @Router /api/addons/{id}/upgrade [post]
func UpgradeAddon(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return updateMutation(g, db, opts, gateway.ActionUpdateUpgrade, true,
		func() any { return &UpgradeRequest{} },
		func(v any) string { return v.(*UpgradeRequest).OperationID },
		func(v any) (uint64, any) {
			r := v.(*UpgradeRequest)
			return r.Generation, struct {
				ExpectedDigest  string `json:"expectedDigest"`
				CandidateDigest string `json:"candidateDigest"`
			}{r.ExpectedDigest, r.CandidateDigest}
		})
}
