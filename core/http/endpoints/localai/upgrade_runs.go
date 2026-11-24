package localai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"

	addonendpoints "github.com/mudler/LocalAI/core/http/endpoints/addons"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/services/upgraderun"
)

// UpgradeRunDeps are what the upgrade run routes use.
type UpgradeRunDeps struct {
	// Runs returns the service, or nil when upgrade runs are not available
	// on this server.
	Runs func() *upgraderun.Service
	// Principal is the actor a request starts a run as (see
	// addons.RunPrincipal). Nil uses no actor.
	Principal func(c echo.Context) string
}

func (d UpgradeRunDeps) service() *upgraderun.Service {
	if d.Runs == nil {
		return nil
	}
	return d.Runs()
}

func (d UpgradeRunDeps) principal(c echo.Context) string {
	if d.Principal == nil {
		return ""
	}
	return d.Principal(c)
}

// UpgradeRunError is a typed refusal of the upgrade run routes. Code is one
// of invalid (400), not_found (404), no_update, stale, run_active,
// not_cancellable, run_finished, run_not_finished, nothing_to_retry,
// source_unchecked, not_human_surface (409), unavailable (503); Key names the
// item when the refusal is about one.
type UpgradeRunError struct {
	Code    string `json:"code" example:"run_active"`
	Key     string `json:"key,omitempty" example:"gallery:kokoro"`
	Message string `json:"message" example:"another upgrade run is running"`
}

// UpgradeRunPreviewRequest limits a preview to some items; an empty list
// previews every item with an update.
type UpgradeRunPreviewRequest struct {
	Items []upgraderun.ItemRef `json:"items"`
}

// UpgradeRunRisk is one thing an update does beyond replacing the item.
// Codes: stops_models (params count, nodes), cluster (nodes), drift (count),
// config_only, migrates_data. The numbers are decimal strings.
type UpgradeRunRisk struct {
	Code   string            `json:"code" example:"stops_models"`
	Params map[string]string `json:"params"`
}

// UpgradeRunPreviewItem is one item of a preview: the plan item of the run
// (key, kind, name, from, to, expected_to, stage, order, startable, reason,
// risks, selected), with default_selected and risk as the structured forms
// of selected and risks.
type UpgradeRunPreviewItem struct {
	upgraderun.PlanItem
	DefaultSelected bool             `json:"default_selected"`
	Risk            []UpgradeRunRisk `json:"risk"`
}

// UpgradeRunPreview is the answer of a preview.
type UpgradeRunPreview struct {
	Items []UpgradeRunPreviewItem `json:"items"`
	// Order is the keys in run order.
	Order []string `json:"order"`
	// Complete is false when a source did not answer, so items may be
	// missing.
	Complete bool `json:"complete"`
	// ActiveRunID is the run that runs now; a start is refused while it runs.
	ActiveRunID string `json:"active_run_id,omitempty"`
}

// UpgradeRunList is the answer of the list route.
type UpgradeRunList struct {
	Runs []upgraderun.Run `json:"runs"`
}

func runRefusal(c echo.Context, err error) error {
	var e *upgraderun.Error
	if !errors.As(err, &e) {
		xlog.Error("Upgrade run request failed", "error", err)
		return c.JSON(http.StatusInternalServerError, UpgradeRunError{Code: "internal", Message: "the request failed on the server; see the server log"})
	}
	return c.JSON(runStatus(e.Code), UpgradeRunError{Code: e.Code, Key: e.Key, Message: e.Message})
}

// runStatus is the HTTP status of a typed refusal of the run service.
func runStatus(code string) int {
	switch code {
	case upgraderun.CodeInvalid:
		return http.StatusBadRequest
	case upgraderun.CodeNotFound:
		return http.StatusNotFound
	}
	return http.StatusConflict
}

func runsUnavailable(c echo.Context) error {
	return c.JSON(http.StatusServiceUnavailable, UpgradeRunError{Code: "unavailable", Message: "upgrade runs are not available on this server"})
}

// publicRun is a run as the routes answer it: the actor names the user only,
// the credential reference it carries stays on the server.
func publicRun(r upgraderun.Run) upgraderun.Run {
	r.Actor = addonendpoints.RunActorUser(r.Actor)
	return r
}

// readBody decodes an optional JSON body: an empty body leaves v unchanged.
func readBody(c echo.Context, v any) error {
	body, err := io.ReadAll(io.LimitReader(c.Request().Body, 1<<20))
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	return json.Unmarshal(body, v)
}

// PreviewUpgradeRunEndpoint orders the items with an update into a plan.
// @Summary Preview an upgrade run
// @Description Lists the backends, gallery models and add-ons that have an update, in run order (backends, then models, then add-ons), with what each update does and whether it can start. Starts nothing. Send expected_to of each item back in the start request. Administrators only.
// @Tags updates
// @Accept json
// @Produce json
// @Param request body UpgradeRunPreviewRequest false "Items to preview; empty previews all"
// @Success 200 {object} UpgradeRunPreview "Plan"
// @Failure 400 {object} UpgradeRunError "Malformed request"
// @Failure 503 {object} UpgradeRunError "Upgrade runs are not available"
// @Router /api/updates/runs/preview [post]
func PreviewUpgradeRunEndpoint(d UpgradeRunDeps) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := d.service()
		if svc == nil {
			return runsUnavailable(c)
		}
		var req UpgradeRunPreviewRequest
		if err := readBody(c, &req); err != nil {
			return c.JSON(http.StatusBadRequest, UpgradeRunError{Code: upgraderun.CodeInvalid, Message: "invalid body: " + err.Error()})
		}
		for _, r := range req.Items {
			if !r.Kind.Valid() || r.Name == "" {
				return c.JSON(http.StatusBadRequest, UpgradeRunError{Code: upgraderun.CodeInvalid, Key: upgraderun.Key(r.Kind, r.Name), Message: "unknown item kind or empty name"})
			}
		}
		plan, err := svc.Preview(c.Request().Context(), d.principal(c), req.Items)
		if err != nil {
			return runRefusal(c, err)
		}
		out := UpgradeRunPreview{Items: []UpgradeRunPreviewItem{}, Order: []string{}, Complete: plan.Complete, ActiveRunID: plan.ActiveRunID}
		for _, it := range plan.Items {
			risk := make([]UpgradeRunRisk, 0, len(it.Risks))
			for _, r := range it.Risks {
				params := map[string]string{}
				for k, v := range it.RiskParams[r] {
					params[k] = v
				}
				risk = append(risk, UpgradeRunRisk{Code: r, Params: params})
			}
			out.Items = append(out.Items, UpgradeRunPreviewItem{PlanItem: it, DefaultSelected: it.Selected, Risk: risk})
			out.Order = append(out.Order, it.Key)
		}
		return c.JSON(http.StatusOK, out)
	}
}

// StartUpgradeRunEndpoint starts an upgrade run.
// @Summary Start an upgrade run
// @Description Checks every item (installed, an update available, the update still goes to expected_to) and starts one run; if one item fails a check, no run is created. Only one run runs at a time (409 run_active). A held model or one whose update changes edited keys is part of the run as skipped. Add-on items run for the administrator who started the run, through the same owner checks as POST /api/addons/{id}/upgrade; they need the administrator's own browser session with the header X-LocalAI-Confirm: upgrade-run, or they are skipped with error key not_human_surface. source must be manual. Administrators only.
// @Tags updates
// @Accept json
// @Produce json
// @Param request body upgraderun.StartRequest true "Items and the version or digest each must move to"
// @Success 202 {object} upgraderun.Run "The run"
// @Failure 400 {object} UpgradeRunError "Code invalid"
// @Failure 409 {object} UpgradeRunError "Code no_update, stale, run_active or source_unchecked"
// @Failure 503 {object} UpgradeRunError "Upgrade runs are not available"
// @Router /api/updates/runs [post]
func StartUpgradeRunEndpoint(d UpgradeRunDeps) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := d.service()
		if svc == nil {
			return runsUnavailable(c)
		}
		var req upgraderun.StartRequest
		if err := readBody(c, &req); err != nil {
			return c.JSON(http.StatusBadRequest, UpgradeRunError{Code: upgraderun.CodeInvalid, Message: "invalid body: " + err.Error()})
		}
		if req.Source == "" {
			req.Source = upgraderun.SourceManual
		}
		if req.Source != upgraderun.SourceManual {
			// Automatic runs are the server's own; a request is always manual.
			return c.JSON(http.StatusBadRequest, UpgradeRunError{Code: upgraderun.CodeInvalid, Message: "source must be manual"})
		}
		run, err := svc.Start(c.Request().Context(), d.principal(c), req)
		if err != nil {
			return runRefusal(c, err)
		}
		return c.JSON(http.StatusAccepted, publicRun(run))
	}
}

// ListUpgradeRunsEndpoint lists the runs.
// @Summary List upgrade runs
// @Description The running run first, then the newest. active=1 lists only the running run. Dismissed runs are left out. Administrators only.
// @Tags updates
// @Produce json
// @Param active query string false "1 lists only the running run"
// @Param limit query int false "Most runs to return (default 20)"
// @Success 200 {object} UpgradeRunList "Runs"
// @Failure 503 {object} UpgradeRunError "Upgrade runs are not available"
// @Router /api/updates/runs [get]
func ListUpgradeRunsEndpoint(d UpgradeRunDeps) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := d.service()
		if svc == nil {
			return runsUnavailable(c)
		}
		limit := 20
		if v := c.QueryParam("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return c.JSON(http.StatusBadRequest, UpgradeRunError{Code: upgraderun.CodeInvalid, Message: "limit must be a positive number"})
			}
			limit = min(n, 200)
		}
		active := c.QueryParam("active") == "1" || c.QueryParam("active") == "true"
		runs, err := svc.List(c.Request().Context(), upgraderun.ListOptions{Limit: limit})
		if err != nil {
			return runRefusal(c, err)
		}
		out := UpgradeRunList{Runs: []upgraderun.Run{}}
		for _, r := range runs {
			if active && !r.Active() {
				continue
			}
			out.Runs = append(out.Runs, publicRun(r))
		}
		return c.JSON(http.StatusOK, out)
	}
}

// GetUpgradeRunEndpoint reads one run.
// @Summary Read an upgrade run
// @Description The run with each item: phase, progress, error key and the operation (job_id) of /api/operations it runs. Administrators only.
// @Tags updates
// @Produce json
// @Param id path string true "Run id"
// @Success 200 {object} upgraderun.Run "The run"
// @Failure 404 {object} UpgradeRunError "Code not_found"
// @Router /api/updates/runs/{id} [get]
func GetUpgradeRunEndpoint(d UpgradeRunDeps) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := d.service()
		if svc == nil {
			return runsUnavailable(c)
		}
		run, err := svc.Get(c.Request().Context(), c.Param("id"))
		if err != nil {
			return runRefusal(c, err)
		}
		return c.JSON(http.StatusOK, publicRun(run))
	}
}

// answerRun answers with the run after a change of it.
func answerRun(c echo.Context, svc *upgraderun.Service, id string) error {
	run, err := svc.Get(c.Request().Context(), id)
	if err != nil {
		return runRefusal(c, err)
	}
	return c.JSON(http.StatusOK, publicRun(run))
}

// CancelUpgradeRunEndpoint cancels a run.
// @Summary Cancel an upgrade run
// @Description Drops the queued items and stops the running ones that can stop; the run then ends cancelled. Administrators only.
// @Tags updates
// @Produce json
// @Param id path string true "Run id"
// @Success 200 {object} upgraderun.Run "The run"
// @Failure 404 {object} UpgradeRunError "Code not_found"
// @Failure 409 {object} UpgradeRunError "Code run_finished"
// @Router /api/updates/runs/{id}/cancel [post]
func CancelUpgradeRunEndpoint(d UpgradeRunDeps) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := d.service()
		if svc == nil {
			return runsUnavailable(c)
		}
		id := c.Param("id")
		if err := svc.Cancel(c.Request().Context(), id); err != nil {
			return runRefusal(c, err)
		}
		return answerRun(c, svc, id)
	}
}

// CancelUpgradeRunItemEndpoint cancels one item of a run.
// @Summary Cancel one item of an upgrade run
// @Description A queued item is dropped; a running item is stopped only while its operation says it can stop. Administrators only.
// @Tags updates
// @Produce json
// @Param id path string true "Run id"
// @Param key path string true "Item key, <kind>:<name>, URL-encoded"
// @Success 200 {object} upgraderun.Run "The run"
// @Failure 404 {object} UpgradeRunError "Code not_found"
// @Failure 409 {object} UpgradeRunError "Code not_cancellable or run_finished"
// @Router /api/updates/runs/{id}/items/{key}/cancel [post]
func CancelUpgradeRunItemEndpoint(d UpgradeRunDeps) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := d.service()
		if svc == nil {
			return runsUnavailable(c)
		}
		id := c.Param("id")
		key, err := url.PathUnescape(c.Param("key"))
		if err != nil || key == "" {
			return c.JSON(http.StatusBadRequest, UpgradeRunError{Code: upgraderun.CodeInvalid, Message: "invalid item key"})
		}
		if err := svc.CancelItem(c.Request().Context(), id, key); err != nil {
			return runRefusal(c, err)
		}
		return answerRun(c, svc, id)
	}
}

// retryNeedsHuman says whether a retry of run would start add-on items,
// which need the administrator's own browser session as the start does.
func retryNeedsHuman(run upgraderun.Run) bool {
	for i := range run.Items {
		if run.Items[i].Kind == upgraderun.KindAddon && run.Items[i].RetryEligible() {
			return true
		}
	}
	return false
}

// RetryUpgradeRunEndpoint starts a new run with the retryable items of a run
// that is over.
// @Summary Retry an upgrade run
// @Description Starts a new run with the items of a finished run that failed, rolled back or stayed unconfirmed and still have an update, toward what the checks offer now. The old run does not change. The new run is a manual run of the administrator who retries, also when the old run was automatic or was started by another administrator. When that includes add-on items, the request needs the administrator's own browser session with X-LocalAI-Confirm: upgrade-run (409 not_human_surface). Administrators only.
// @Tags updates
// @Produce json
// @Param id path string true "Run id"
// @Success 200 {object} upgraderun.Run "The new run"
// @Failure 404 {object} UpgradeRunError "Code not_found"
// @Failure 409 {object} UpgradeRunError "Code run_not_finished, nothing_to_retry, run_active, not_human_surface or source_unchecked"
// @Router /api/updates/runs/{id}/retry [post]
func RetryUpgradeRunEndpoint(d UpgradeRunDeps) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := d.service()
		if svc == nil {
			return runsUnavailable(c)
		}
		id := c.Param("id")
		old, err := svc.Get(c.Request().Context(), id)
		if err != nil {
			return runRefusal(c, err)
		}
		// The new run belongs to the caller, checked as a start is.
		actor := d.principal(c)
		if !old.Active() && retryNeedsHuman(old) {
			if _, human, ok := addonendpoints.DecodeRunActor(actor); !ok || !human {
				return c.JSON(http.StatusConflict, UpgradeRunError{Code: addonendpoints.RunErrNotHumanSurface,
					Message: "retrying add-on upgrades needs your own browser session; retry from the Updates page"})
			}
		}
		run, err := svc.Retry(c.Request().Context(), actor, id)
		if err != nil {
			return runRefusal(c, err)
		}
		return c.JSON(http.StatusOK, publicRun(run))
	}
}

// DismissUpgradeRunEndpoint hides a run that is over.
// @Summary Dismiss an upgrade run
// @Description Hides a finished run from the default list. Administrators only.
// @Tags updates
// @Param id path string true "Run id"
// @Success 204 "Dismissed"
// @Failure 404 {object} UpgradeRunError "Code not_found"
// @Failure 409 {object} UpgradeRunError "Code run_not_finished"
// @Router /api/updates/runs/{id} [delete]
func DismissUpgradeRunEndpoint(d UpgradeRunDeps) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := d.service()
		if svc == nil {
			return runsUnavailable(c)
		}
		if err := svc.Dismiss(c.Request().Context(), c.Param("id")); err != nil {
			return runRefusal(c, err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// SingleUpgradeAnswer writes the answer of a single upgrade route that a run
// of one item started: the old fields of the route with the job id of the
// item, and the run.
type SingleUpgradeAnswer func(c echo.Context, jobID string, run upgraderun.Run) error

// singleRefusal is the refusal of a single upgrade route that a run answered:
// the error text the route always had, with a stable code.
// SingleUpgradePending is the answer of a single upgrade route whose run did
// not get the operation accepted within the wait (202). The run goes on and
// starts the item; follow it at GET /api/updates/runs/{run_id}.
type SingleUpgradePending struct {
	// Code is always "pending".
	Code    string          `json:"code"`
	Message string          `json:"message"`
	RunID   string          `json:"run_id"`
	Run     *upgraderun.Run `json:"run,omitempty"`
}

// singlePending answers a run of one item that has no operation yet.
func singlePending(c echo.Context, out upgraderun.SingleResult) error {
	run := publicRun(out.Run)
	return c.JSON(http.StatusAccepted, SingleUpgradePending{
		Code: string(upgraderun.SinglePending), RunID: out.RunID, Run: &run,
		Message: "the upgrade waits in an upgrade run and has no operation yet; follow the run",
	})
}

func singleRefusal(c echo.Context, status int, code, msg string) error {
	return c.JSON(status, map[string]any{"error": msg, "code": code})
}

// BackendUpgradeViaRun wraps a backend upgrade route (POST
// /api/backends/upgrade/{name}, POST /backends/upgrade/{name}) so it starts an
// upgrade run of one item, answering with its old fields plus the run. The
// route keeps working for every existing client:
//   - the guard answers first: 404 not_installed, 409 no_update or stale
//     (see BackendUpgradeGuard), before a run is admitted;
//   - without upgrade runs on this server, legacy answers as before;
//   - while another run runs, 409 with code run_active, before anything else
//     is checked, so no direct upgrade runs beside a run;
//   - with expected_to no longer the available build, 409 with code stale;
//   - when the run's check does not list the backend (not checked yet, or no
//     update) and no run runs, legacy answers, with its own checks.
//
// A nil guard refuses nothing.
func BackendUpgradeViaRun(d UpgradeRunDeps, guard *BackendUpgradeGuard, legacy echo.HandlerFunc, answer SingleUpgradeAnswer) echo.HandlerFunc {
	return func(c echo.Context) error {
		name, err := url.PathUnescape(c.Param("name"))
		if err != nil || name == "" {
			return legacy(c)
		}
		expected, err := ExpectedToOf(c)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
		}
		if refusal := guard.Check(c.Request().Context(), name, "", expected); refusal != nil {
			return refusal.Write(c)
		}
		svc := d.service()
		if svc == nil {
			return legacy(c)
		}
		run, item, err := svc.StartOne(c.Request().Context(), d.principal(c),
			upgraderun.StartItem{Kind: upgraderun.KindGallery, Name: name, ExpectedTo: expected}, upgraderun.SourceManual, 0)
		switch code := upgraderun.CodeOf(err); code {
		case "":
		case upgraderun.CodeRunActive:
			return singleRefusal(c, http.StatusConflict, code, runActiveMessage)
		case upgraderun.CodeStale:
			if expected != "" {
				var e *upgraderun.Error
				_ = errors.As(err, &e)
				return singleRefusal(c, http.StatusConflict, code, e.Message)
			}
			return legacy(c)
		case upgraderun.CodeNoUpdate, upgraderun.CodeSourceUnchecked:
			return legacy(c)
		default:
			return runRefusal(c, err)
		}
		out := upgraderun.SingleOutcome(run, item)
		switch out.State {
		case upgraderun.SingleRefused:
			return singleRefusal(c, http.StatusConflict, out.ErrorKey, out.Error)
		case upgraderun.SinglePending:
			return singlePending(c, out)
		}
		return answer(c, out.JobID, publicRun(out.Run))
	}
}

// UIBackendUpgradeAnswer is the answer of POST /api/backends/upgrade/{name}.
func UIBackendUpgradeAnswer(c echo.Context, jobID string, run upgraderun.Run) error {
	return c.JSON(http.StatusOK, map[string]any{
		"jobID":     jobID,
		"uuid":      jobID,
		"statusUrl": fmt.Sprintf("/api/backends/job/%s", jobID),
		"message":   "Backend upgrade started",
		"run":       run,
	})
}

// BackendUpgradeRunResponse is the answer of POST /backends/upgrade/{name}
// when an upgrade run started it.
type BackendUpgradeRunResponse struct {
	ID        string          `json:"id"`
	StatusURL string          `json:"status_url"`
	Run       *upgraderun.Run `json:"run,omitempty"`
}

// APIBackendUpgradeAnswer is the answer of POST /backends/upgrade/{name}.
func APIBackendUpgradeAnswer(c echo.Context, jobID string, run upgraderun.Run) error {
	return c.JSON(http.StatusOK, BackendUpgradeRunResponse{
		ID: jobID, StatusURL: fmt.Sprintf("%sbackends/jobs/%s", middleware.BaseURL(c), jobID), Run: &run,
	})
}
