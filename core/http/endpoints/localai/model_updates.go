package localai

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/backendupdates"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/modelupdates"
	"github.com/mudler/xlog"
)

// ModelUpdatesProvider is the read model and the check of model updates
// (modelupdates.Service).
type ModelUpdatesProvider interface {
	Snapshot(ctx context.Context) modelupdates.Snapshot
	CheckNow(ctx context.Context) modelupdates.Snapshot
}

// ModelUpdatesEndpoint lists the installed gallery models and whether their
// gallery entry changed.
// @Summary List the model updates
// @Description Every model installed from a gallery, with its state: up-to-date, update-available, edited (the update would overwrite keys a person changed; editedKeys lists them), unknown (reason says why), gone (no gallery lists the entry), held (the owner holds this version) or failed (the last upgrade was rolled back; upgrade has the error). needsPerson marks what the Updates page counts: models never update automatically. What the galleries offer is as of the last check; the hold flag, an edit and an upgrade show at once. Every frontend of a cluster gives the same answer.
// @Tags models
// @Success 200 {object} modelupdates.Snapshot "Response"
// @Router /api/models/updates [get]
func ModelUpdatesEndpoint(p func() ModelUpdatesProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		prov := p()
		if prov == nil {
			return c.JSON(http.StatusOK, disabledModelUpdates())
		}
		return c.JSON(http.StatusOK, prov.Snapshot(c.Request().Context()))
	}
}

// CheckModelUpdatesEndpoint checks the galleries now and answers when the
// check is done.
// @Summary Check for model updates now
// @Description Reads every gallery index again (not the copies of the last hour), compares each installed gallery model with its entry, waits for the check (at most 60 seconds) and returns the new data. One frontend of a cluster checks at a time; a request that arrives while another check runs waits for that check. A call within 15 seconds of the last check is answered from it with throttled set. A failed check shows as an error in the state of the gallery source and keeps the previous list. Installs nothing.
// @Tags models
// @Success 200 {object} modelupdates.Snapshot "Response"
// @Router /api/models/updates/check [post]
func CheckModelUpdatesEndpoint(p func() ModelUpdatesProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		prov := p()
		if prov == nil {
			return c.JSON(http.StatusOK, disabledModelUpdates())
		}
		return c.JSON(http.StatusOK, prov.CheckNow(c.Request().Context()))
	}
}

func disabledModelUpdates() modelupdates.Snapshot {
	return modelupdates.Snapshot{
		Sources: map[string]backendupdates.Source{modelupdates.SourceGallery: {
			State: backendupdates.StateDisabled, Reason: "no model gallery is configured on this server",
		}},
		Items: []modelupdates.Item{},
	}
}

// ModelUpgradeDeps are what the upgrade routes use. Each getter may return
// nil when no model gallery is configured.
type ModelUpgradeDeps struct {
	Gallery  *galleryop.GalleryService
	Upgrader func() *modelupdates.Upgrader
	Updates  func() *modelupdates.Service
}

// UpgradeModelEndpoint queues the upgrade of an installed gallery model.
// @Summary Upgrade a model to what its gallery entry installs now
// @Description Queues an upgrade and returns its operation; follow it on status_url or /api/operations. The upgrade checks the backend, the free disk and the files other models share, downloads the changed files beside the old ones and verifies their sha256, stops the model, switches the files and the config, and loads the new version as a check. A failed load puts the old version back (state failed, with the error and a retry). The old files are kept until removed with POST /api/models/{name}/remove-old. Keys a person edited are kept; an edited key the update also changes needs a choice in choices, or the request is refused with 409 and the keys. A held model is refused with 409; release the hold first. Nothing upgrades without this request.
// @Tags models
// @Param name path string true "Installed model name"
// @Param request body schema.ModelUpgradeRequest false "Options"
// @Success 200 {object} schema.ModelUpgradeResponse "Operation"
// @Failure 404 {object} schema.ModelUpgradeError "Not installed"
// @Failure 409 {object} schema.ModelUpgradeError "Running, held, or edited keys need a choice"
// @Failure 422 {object} schema.ModelUpgradeError "Not installed from a gallery with a recorded origin"
// @Router /api/models/upgrade/{name} [post]
func UpgradeModelEndpoint(d ModelUpgradeDeps) echo.HandlerFunc {
	return func(c echo.Context) error { return queueModelUpgrade(c, d, false) }
}

// RetryModelUpgradeEndpoint queues a failed upgrade again.
// @Summary Retry a failed model upgrade
// @Description Runs the last upgrade of the model again, with its options and choices, toward what the gallery entry installs now. Refused with 409 when the last upgrade did not fail or the model is held.
// @Tags models
// @Param name path string true "Installed model name"
// @Success 200 {object} schema.ModelUpgradeResponse "Operation"
// @Failure 404 {object} schema.ModelUpgradeError "Not installed"
// @Failure 409 {object} schema.ModelUpgradeError "Nothing to retry, held, or an upgrade runs"
// @Router /api/models/{name}/upgrade/retry [post]
func RetryModelUpgradeEndpoint(d ModelUpgradeDeps) echo.HandlerFunc {
	return func(c echo.Context) error { return queueModelUpgrade(c, d, true) }
}

func modelUpgradeError(c echo.Context, err error) error {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, modelupdates.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, modelupdates.ErrNotUpgradable):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, modelupdates.ErrBusy), errors.Is(err, modelupdates.ErrNothingFailed), errors.Is(err, modelupdates.ErrHeld), errors.Is(err, modelupdates.ErrNoOldVersion), errors.Is(err, modelupdates.ErrFilesKept):
		status = http.StatusConflict
	}
	msg := err.Error()
	if status == http.StatusInternalServerError {
		// An unexpected error can carry server paths and internals: the
		// answer says what failed, the log has the detail.
		xlog.Error("Model update request failed", "error", err)
		msg = "the request failed on the server; see the server log"
	}
	return c.JSON(status, schema.ModelUpgradeError{Error: msg})
}

func queueModelUpgrade(c echo.Context, d ModelUpgradeDeps, retry bool) error {
	up := upgraderOf(d)
	if up == nil || d.Gallery == nil {
		return c.JSON(http.StatusServiceUnavailable, schema.ModelUpgradeError{Error: "model upgrades are not available: no model gallery is configured"})
	}
	name := c.Param("name")
	var req schema.ModelUpgradeRequest
	if !retry && c.Request().ContentLength != 0 {
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, schema.ModelUpgradeError{Error: "invalid body: " + err.Error()})
		}
	}
	var svc *modelupdates.Service
	if d.Updates != nil {
		svc = d.Updates()
	}
	if err := modelupdates.CheckRequest(up, svc, name, retry, req.Choices); err != nil {
		var needs *modelupdates.ErrNeedsChoices
		switch {
		case errors.Is(err, modelupdates.ErrBadChoice):
			return c.JSON(http.StatusBadRequest, schema.ModelUpgradeError{Error: err.Error()})
		case errors.As(err, &needs):
			return c.JSON(http.StatusConflict, schema.ModelUpgradeError{
				Error: "the update changes keys that were edited; choose mine or gallery for each", Keys: needs.Keys,
			})
		}
		return modelUpgradeError(c, err)
	}
	uid, err := d.Gallery.EnqueueModelUpgrade(name, galleryop.ModelUpgradeOptions{ConfigOnly: req.ConfigOnly, Choices: req.Choices, Retry: retry})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, schema.ModelUpgradeResponse{
		ID: uid, JobID: uid, StatusURL: fmt.Sprintf("%smodels/jobs/%s", middleware.BaseURL(c), uid), Message: "Upgrade started",
	})
}

// RemoveOldModelVersionEndpoint deletes the files a confirmed upgrade kept.
// @Summary Remove the old version of an upgraded model
// @Description Deletes the old files that the last upgrade of the model kept (retainedFiles of GET /api/models/updates). Only after a confirmed upgrade; refused with 409 while an upgrade runs or when there is nothing to remove. A kept file that another model now uses is not deleted. The model config does not change.
// @Tags models
// @Param name path string true "Installed model name"
// @Success 200 {object} schema.ModelRemoveOldResponse "Removed files"
// @Failure 404 {object} schema.ModelUpgradeError "Not installed"
// @Failure 409 {object} schema.ModelUpgradeError "Nothing to remove, an upgrade runs, or a file is shared"
// @Router /api/models/{name}/remove-old [post]
func RemoveOldModelVersionEndpoint(d ModelUpgradeDeps) echo.HandlerFunc {
	return func(c echo.Context) error {
		up := upgraderOf(d)
		if up == nil {
			return c.JSON(http.StatusServiceUnavailable, schema.ModelUpgradeError{Error: "model upgrades are not available: no model gallery is configured"})
		}
		name := c.Param("name")
		res, err := up.RemoveOldVersion(c.Request().Context(), name)
		if err != nil {
			// A removal that deleted nothing: the known refusals keep their
			// status, any other error is answered without its text, which can
			// carry server paths.
			return modelUpgradeError(c, err)
		}
		return c.JSON(http.StatusOK, schema.ModelRemoveOldResponse{Name: name, Removed: res.Removed, Kept: res.Kept, Errors: res.Errors})
	}
}

// SetModelHoldEndpoint sets or clears the hold of a model.
// @Summary Hold a model at its installed version
// @Description With hold true the model is reported as held and offered no update until the hold is cleared. Only the gallery file changes: the model config, its revision and the loaded model stay as they are, and a reinstall keeps the hold. Refused with 409 while an upgrade of the model runs.
// @Tags models
// @Param name path string true "Installed model name"
// @Param request body schema.ModelHoldRequest true "Hold or release"
// @Success 200 {object} schema.ModelHoldResponse "The hold"
// @Failure 404 {object} schema.ModelUpgradeError "Not installed"
// @Failure 409 {object} schema.ModelUpgradeError "An upgrade runs"
// @Failure 422 {object} schema.ModelUpgradeError "Not installed from a gallery with a recorded origin"
// @Router /api/models/{name}/hold [put]
func SetModelHoldEndpoint(d ModelUpgradeDeps) echo.HandlerFunc {
	return func(c echo.Context) error {
		up := upgraderOf(d)
		if up == nil {
			return c.JSON(http.StatusServiceUnavailable, schema.ModelUpgradeError{Error: "model upgrades are not available: no model gallery is configured"})
		}
		var req schema.ModelHoldRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, schema.ModelUpgradeError{Error: "invalid body: " + err.Error()})
		}
		name := c.Param("name")
		if err := up.SetHold(c.Request().Context(), name, req.Hold); err != nil {
			return modelUpgradeError(c, err)
		}
		return c.JSON(http.StatusOK, schema.ModelHoldResponse{Name: name, Hold: req.Hold})
	}
}

// AdoptModelEndpoint records the gallery origin of a model installed by an
// earlier release.
// @Summary Track the updates of a model installed by an earlier release
// @Description A model installed before LocalAI recorded the origin of gallery installs shows as unknown in GET /api/models/updates. Adopt records the gallery entry it came from, the files it has and what the gallery would write as its config, so the next check compares it like any other model. The model config on disk is not changed: a key changed by hand reads as an edit. Without a body the entry is found by the model's names, and only when exactly one gallery lists it; otherwise the answer lists the candidates (gallery@entry) and the request must name the gallery (and the entry). Refused with 409 when the model already records its origin, an upgrade runs, or more than one gallery matches; with 422 when the model has no record of a gallery install or no gallery entry matches.
// @Tags models
// @Param name path string true "Installed model name"
// @Param request body schema.ModelAdoptRequest false "Gallery and entry"
// @Success 200 {object} schema.ModelAdoptResponse "Adopted"
// @Failure 404 {object} schema.ModelUpgradeError "Not installed"
// @Failure 409 {object} schema.ModelUpgradeError "Already adopted, busy, or more than one candidate"
// @Failure 422 {object} schema.ModelUpgradeError "No record of a gallery install, or no candidate"
// @Router /api/models/{name}/adopt [post]
func AdoptModelEndpoint(d ModelUpgradeDeps) echo.HandlerFunc {
	return func(c echo.Context) error {
		up := upgraderOf(d)
		if up == nil {
			return c.JSON(http.StatusServiceUnavailable, schema.ModelUpgradeError{Error: "model upgrades are not available: no model gallery is configured"})
		}
		var req schema.ModelAdoptRequest
		if c.Request().ContentLength != 0 {
			if err := c.Bind(&req); err != nil {
				return c.JSON(http.StatusBadRequest, schema.ModelUpgradeError{Error: "invalid body: " + err.Error()})
			}
		}
		name := c.Param("name")
		res, err := up.Adopt(c.Request().Context(), modelupdates.AdoptRequest{Name: name, Gallery: req.Gallery, Entry: req.Entry})
		if err != nil {
			var ambiguous *modelupdates.ErrAmbiguousEntry
			var none *modelupdates.ErrNoCandidate
			switch {
			case errors.As(err, &ambiguous):
				return c.JSON(http.StatusConflict, schema.ModelUpgradeError{Error: err.Error(), Candidates: candidateStrings(ambiguous.Candidates)})
			case errors.As(err, &none):
				return c.JSON(http.StatusUnprocessableEntity, schema.ModelUpgradeError{Error: err.Error(), Candidates: candidateStrings(none.Candidates)})
			case errors.Is(err, modelupdates.ErrNoInstallRecord):
				return c.JSON(http.StatusUnprocessableEntity, schema.ModelUpgradeError{Error: err.Error()})
			case errors.Is(err, modelupdates.ErrAlreadyAdopted):
				return c.JSON(http.StatusConflict, schema.ModelUpgradeError{Error: err.Error()})
			}
			return modelUpgradeError(c, err)
		}
		return c.JSON(http.StatusOK, schema.ModelAdoptResponse{Name: res.Name, Gallery: res.Gallery, Entry: res.Entry, FilesMatch: res.FilesMatch})
	}
}

func candidateStrings(cs []modelupdates.AdoptCandidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.String()
	}
	return out
}

func upgraderOf(d ModelUpgradeDeps) *modelupdates.Upgrader {
	if d.Upgrader == nil {
		return nil
	}
	return d.Upgrader()
}

// RemovedModelConfigEndpoint answers the removed route that rewrote a model
// config from its gallery entry. It wrote the config at once, outside the
// operation queue, without a backup, a load check or the edits a person made,
// and told no other frontend of a cluster. An upgrade does all of that.
// @Summary Removed: rewrite a model config from the gallery
// @Description Removed. Always answers 410 Gone with a Link header to POST /api/models/upgrade/{name}; use that route with config_only true to apply the gallery config of an installed model. This answer stays for one release, then the route goes away.
// @Tags models
// @Deprecated
// @Param id path string true "Gallery model ID"
// @Failure 410 {object} schema.RemovedRouteResponse "Removed"
// @Router /api/models/config/{id} [post]
func RemovedModelConfigEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		c.Response().Header().Set("Link", `</api/models/upgrade/{name}>; rel="successor-version"`)
		return c.JSON(http.StatusGone, schema.RemovedRouteResponse{
			Error:       "this route was removed: upgrade the installed model with config_only true instead",
			Replacement: "POST /api/models/upgrade/{name}",
		})
	}
}
