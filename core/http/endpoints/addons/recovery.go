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
	"github.com/mudler/LocalAI/core/services/addons/recovery"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"gorm.io/gorm"
)

// The data recovery routes are the UI-only administrator half of an add-on's
// lifecycle: the backups of its data, an explicit restore and the purge of
// retained data. They are never tools: no MCP tool, model tool, proposal or
// typed dispatch of a model maps to them. Every mutation is accepted only
// from an administrator's own browser session, with the operation ID in the
// confirmation header, and is decided by the owner.

// RecoveryBackupRequest asks for a backup now.
type RecoveryBackupRequest struct {
	// OperationID identifies the request. Repeating it returns the backup it
	// made and makes no second one.
	OperationID string `json:"operationId"`
}

// RecoveryVerifyRequest asks the owner to read a backup completely.
type RecoveryVerifyRequest struct {
	OperationID string `json:"operationId"`
	BackupID    string `json:"backupId"`
}

// RecoveryRestoreRequest restores one backup.
type RecoveryRestoreRequest struct {
	OperationID string `json:"operationId"`
	BackupID    string `json:"backupId"`
	// Mapping names the current account that takes the data of a backup made
	// in another ownership mode, as {"local-owner": "<account id>"}. It is
	// explicit: there is no wildcard and no default.
	Mapping map[string]string `json:"mapping,omitempty"`
	// Confirm is the add-on's ID, typed.
	Confirm string `json:"confirm"`
}

// RecoveryDeleteBackupRequest removes one backup.
type RecoveryDeleteBackupRequest struct {
	OperationID string `json:"operationId"`
	BackupID    string `json:"backupId"`
}

// RecoveryPruneRequest applies the retention policy now.
type RecoveryPruneRequest struct {
	OperationID string `json:"operationId"`
}

// RecoveryPurgeRequest removes retained data.
type RecoveryPurgeRequest struct {
	OperationID string              `json:"operationId"`
	Scope       recovery.PurgeScope `json:"scope"`
	// Confirm is the add-on's ID, typed.
	Confirm string `json:"confirm"`
}

// RecoveryDeleted is the answer of a backup deletion.
type RecoveryDeleted struct {
	Deleted string `json:"deleted"`
}

// RecoveryPruned is the answer of a retention run.
type RecoveryPruned struct {
	Removed []string `json:"removed"`
}

// recoveryCall runs one recovery action on the owner and writes its answer.
// The owner's typed refusal is part of its result, so every frontend gives
// the same status and type.
func recoveryCall(c echo.Context, g *gateway.Gateway, a contract.Actor, id, opID, action string, args any) error {
	raw, e := json.Marshal(args)
	if e != nil {
		return failure(c, 400, "Invalid recovery request")
	}
	out, e := g.Dispatch(c.Request().Context(), a, contract.Request{AddonID: id, OperationID: opID, Action: action, Arguments: raw})
	if e != nil {
		return resultError(c, e)
	}
	var env gateway.RecoveryEnvelope
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
	return c.Blob(200, echo.MIMEApplicationJSON, env.Data)
}

// recoveryRoute is the shared front of the recovery routes: authentication,
// administration (before the body is read), and for a mutation the human
// surface bound to the operation ID.
func recoveryRoute(g *gateway.Gateway, db *gorm.DB, opts []Option, action string, mutates bool, body func() any, opOf func(any) string, argsOf func(any) any) echo.HandlerFunc {
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
		var req any
		opID := ""
		if body != nil {
			req = body()
			if decode(c, req) != nil {
				return failure(c, 400, "Invalid recovery request")
			}
			opID = opOf(req)
			if opID == "" || len(opID) > 256 {
				return failure(c, 400, "An operationId is required")
			}
		}
		if mutates && !humanSurface(c, s, a, opID) {
			return failure(c, 403, "This action requires your own browser session")
		}
		if g == nil {
			return failure(c, 503, "Addons unavailable")
		}
		if opID == "" {
			// A read has no operation of its own; the forwarding frame
			// needs one to bind the call to.
			var raw [16]byte
			if _, e = rand.Read(raw[:]); e != nil {
				return failure(c, 503, "Addons unavailable")
			}
			opID = "status-" + hex.EncodeToString(raw[:])
		}
		var args any = struct{}{}
		if argsOf != nil {
			args = argsOf(req)
		}
		return recoveryCall(c, g, a, id, opID, action, args)
	}
}

// GetRecovery reads the recovery state of an add-on.
// @Summary Read the data recovery state and the backups of an add-on (administrators)
// @Description Administrators only. Returns the recovery state of the add-on's data (ready, upgrading, restoring, needs-recovery or purging) with its reason, the backups newest first with their size, creation time, reason (manual, upgrade or restore), the ownership mode and package version they hold and the last recorded verification result (verified, failed or unverified), the backup that is the recovery copy, the receipt and warnings of the last restore, the retention policy, the number of data copies a restore set aside and the operation IDs whose result is recorded. Read-only. No MCP tool maps to this route.
// @Tags Addons
// @Produce json
// @Param id path string true "Add-on ID"
// @Success 200 {object} recovery.Status "Recovery state"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature or administration denied"
// @Failure 503 {object} AddonErrorResponse "Addons or the owner are unavailable"
// @Router /api/addons/{id}/recovery [get]
func GetRecovery(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return recoveryRoute(g, db, opts, gateway.ActionRecoveryStatus, false, nil, nil, nil)
}

// CreateBackup makes a backup.
// @Summary Back up the data of an add-on (administrators, browser session)
// @Description Administrators only, from their own browser session (same-origin fetch, X-LocalAI-Confirm equal to the operationId). Blocks new admission for the add-on's resources, invalidates its pending proposals, drains admitted work, stops the add-on, makes a consistent copy of its data (every SQLite file with VACUUM INTO, so the write-ahead log is part of it) together with the owner's access state, verifies the copy, publishes it atomically with restricted permissions and starts the add-on again if it was meant to run. A backup that cannot be completed leaves nothing behind. Repeating the operationId returns the same backup. 503 insufficient_storage means the free space did not fit the backup and nothing was changed. No MCP tool maps to this route.
// @Tags Addons
// @Accept json
// @Produce json
// @Param id path string true "Add-on ID"
// @Param request body RecoveryBackupRequest true "Operation"
// @Success 200 {object} recovery.BackupReceipt "The verified backup"
// @Failure 400 {object} AddonErrorResponse "Malformed request"
// @Failure 401 {object} AddonErrorResponse "Named authentication required"
// @Failure 403 {object} AddonErrorResponse "Feature, administration or browser session denied"
// @Failure 404 {object} AddonErrorResponse "Type not_installed"
// @Failure 409 {object} AddonErrorResponse "Type busy"
// @Failure 503 {object} AddonErrorResponse "Unavailable, insufficient_storage, or outcome_unknown (with operationId)"
// @Router /api/addons/{id}/recovery/backup [post]
func CreateBackup(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return recoveryRoute(g, db, opts, gateway.ActionRecoveryBackup, true,
		func() any { return &RecoveryBackupRequest{} },
		func(v any) string { return v.(*RecoveryBackupRequest).OperationID }, nil)
}

// VerifyBackup reads a backup completely.
// @Summary Verify a backup of an add-on (administrators, browser session)
// @Description Administrators only, from their own browser session. Reads every file of the backup against its manifest digests and runs the SQLite integrity check, and records the result shown in the list. A failed backup stays in place; removing it is a separate explicit action. No MCP tool maps to this route.
// @Tags Addons
// @Accept json
// @Produce json
// @Param id path string true "Add-on ID"
// @Param request body RecoveryVerifyRequest true "Operation and backup"
// @Success 200 {object} recovery.BackupReceipt "The verified backup"
// @Failure 400 {object} AddonErrorResponse "Malformed request"
// @Failure 403 {object} AddonErrorResponse "Feature, administration or browser session denied"
// @Failure 404 {object} AddonErrorResponse "Type backup_not_found"
// @Failure 409 {object} AddonErrorResponse "Type backup_corrupt"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or outcome_unknown (with operationId)"
// @Router /api/addons/{id}/recovery/verify [post]
func VerifyBackup(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return recoveryRoute(g, db, opts, gateway.ActionRecoveryVerify, true,
		func() any { return &RecoveryVerifyRequest{} },
		func(v any) string { return v.(*RecoveryVerifyRequest).OperationID },
		func(v any) any {
			return struct {
				BackupID string `json:"backupId"`
			}{v.(*RecoveryVerifyRequest).BackupID}
		})
}

// RestoreBackup restores a backup.
// @Summary Restore a backup of an add-on (administrators, browser session)
// @Description Administrators only, from their own browser session, with the add-on ID typed as confirm. The add-on must not be running. The backup is verified before anything changes, its files are staged and swapped in atomically, and the data they replace stays as a recovery copy until it is purged. Access is not restored as it was: every grant is checked again against the current accounts and the lowerings of access recorded since the backup, so nobody gets back access that was taken away; pending proposals are cancelled and operations with an unknown outcome are looked up by receipt and never applied again. The answer carries warnings: the time the data goes back to, the number of changes made after it that are lost, and the shares that were not restored. A backup made in local-owner mode needs mapping {"local-owner": "<account id>"}; a backup of named-user data is refused in local-owner mode. Repeating the operationId returns the same receipt and does not restore again. No MCP tool maps to this route. The backup must belong to the add-on in the path; a backup of another add-on is refused with type invalid_request and nothing is touched.
// @Tags Addons
// @Accept json
// @Produce json
// @Param id path string true "Add-on ID"
// @Param request body RecoveryRestoreRequest true "Operation, backup, mapping and confirmation"
// @Success 200 {object} recovery.RestoreReceipt "What was restored, with warnings"
// @Failure 400 {object} AddonErrorResponse "Malformed request, type confirmation_mismatch or mapping_invalid"
// @Failure 403 {object} AddonErrorResponse "Feature, administration or browser session denied"
// @Failure 404 {object} AddonErrorResponse "Type backup_not_found"
// @Failure 409 {object} AddonErrorResponse "Type addon_running, backup_corrupt, incompatible_format, identity_changed, mapping_required, downgrade_unsupported, operation_conflict or busy"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or outcome_unknown (with operationId)"
// @Router /api/addons/{id}/recovery/restore [post]
func RestoreBackup(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return recoveryRoute(g, db, opts, gateway.ActionRecoveryRestore, true,
		func() any { return &RecoveryRestoreRequest{} },
		func(v any) string { return v.(*RecoveryRestoreRequest).OperationID },
		func(v any) any {
			r := v.(*RecoveryRestoreRequest)
			return struct {
				BackupID string            `json:"backupId"`
				Mapping  map[string]string `json:"mapping,omitempty"`
				Confirm  string            `json:"confirm"`
			}{r.BackupID, r.Mapping, r.Confirm}
		})
}

// DeleteBackup removes a backup.
// @Summary Delete one backup of an add-on (administrators, browser session)
// @Description Administrators only, from their own browser session. The only verified copy and the recovery copy of an upgrade are never removed this way (409 sole_recovery_copy); a purge with explicit scope removes them. No MCP tool maps to this route.
// @Tags Addons
// @Accept json
// @Produce json
// @Param id path string true "Add-on ID"
// @Param request body RecoveryDeleteBackupRequest true "Operation and backup"
// @Success 200 {object} RecoveryDeleted "The deleted backup"
// @Failure 400 {object} AddonErrorResponse "Malformed request"
// @Failure 403 {object} AddonErrorResponse "Feature, administration or browser session denied"
// @Failure 404 {object} AddonErrorResponse "Type backup_not_found"
// @Failure 409 {object} AddonErrorResponse "Type sole_recovery_copy or busy"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or outcome_unknown (with operationId)"
// @Router /api/addons/{id}/recovery/delete-backup [post]
func DeleteBackup(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return recoveryRoute(g, db, opts, gateway.ActionRecoveryDeleteBackup, true,
		func() any { return &RecoveryDeleteBackupRequest{} },
		func(v any) string { return v.(*RecoveryDeleteBackupRequest).OperationID },
		func(v any) any {
			return struct {
				BackupID string `json:"backupId"`
			}{v.(*RecoveryDeleteBackupRequest).BackupID}
		})
}

// PruneBackups applies the retention policy.
// @Summary Apply the backup retention policy now (administrators, browser session)
// @Description Administrators only, from their own browser session. Removes the backups the configured count and age policy no longer keeps. The newest verified backup and the recovery copy of an upgrade stay, whatever their age. No MCP tool maps to this route.
// @Tags Addons
// @Accept json
// @Produce json
// @Param id path string true "Add-on ID"
// @Param request body RecoveryPruneRequest true "Operation"
// @Success 200 {object} RecoveryPruned "The removed backups"
// @Failure 400 {object} AddonErrorResponse "Malformed request"
// @Failure 403 {object} AddonErrorResponse "Feature, administration or browser session denied"
// @Failure 409 {object} AddonErrorResponse "Type busy"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or outcome_unknown (with operationId)"
// @Router /api/addons/{id}/recovery/prune [post]
func PruneBackups(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return recoveryRoute(g, db, opts, gateway.ActionRecoveryPrune, true,
		func() any { return &RecoveryPruneRequest{} },
		func(v any) string { return v.(*RecoveryPruneRequest).OperationID }, nil)
}

// PurgeAddon removes retained data.
// @Summary Purge the retained data of an add-on (administrators, browser session)
// @Description Administrators only, from their own browser session, with the add-on ID typed as confirm. Removes retained data the scope names: the add-on's data and the owner's records of its resources and shares (data), its backups (backups) and the data copies that restores and failed upgrades set aside (recoveryCopies). The add-on must be uninstalled or disabled. It is not the deletion of a database and not an uninstall; those never remove retained data. The purge is journaled and finished after a crash, and it touches no other add-on. No MCP tool maps to this route.
// @Tags Addons
// @Accept json
// @Produce json
// @Param id path string true "Add-on ID"
// @Param request body RecoveryPurgeRequest true "Operation, scope and confirmation"
// @Success 200 {object} recovery.PurgeReceipt "What was purged, with warnings"
// @Failure 400 {object} AddonErrorResponse "Malformed request, empty scope or type confirmation_mismatch"
// @Failure 403 {object} AddonErrorResponse "Feature, administration or browser session denied"
// @Failure 409 {object} AddonErrorResponse "Type addon_running, operation_conflict or busy"
// @Failure 503 {object} AddonErrorResponse "Unavailable, or outcome_unknown (with operationId)"
// @Router /api/addons/{id}/purge [post]
func PurgeAddon(g *gateway.Gateway, db *gorm.DB, opts ...Option) echo.HandlerFunc {
	return recoveryRoute(g, db, opts, gateway.ActionRecoveryPurge, true,
		func() any { return &RecoveryPurgeRequest{} },
		func(v any) string { return v.(*RecoveryPurgeRequest).OperationID },
		func(v any) any {
			r := v.(*RecoveryPurgeRequest)
			return struct {
				Scope   recovery.PurgeScope `json:"scope"`
				Confirm string              `json:"confirm"`
			}{r.Scope, r.Confirm}
		})
}
