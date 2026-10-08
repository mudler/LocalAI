package localai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/xlog"
)

// CarrierSwitchAdmin is the protocol of a change of carrier, as the admin API
// uses it. *cluster.Switch is one.
type CarrierSwitchAdmin interface {
	Status(ctx context.Context) (cluster.Report, error)
	Preflight(ctx context.Context, target cluster.Carrier) (cluster.Report, error)
	// PreflightWithin is Preflight where a report of what a replica can build
	// counts only when it is younger than within.
	PreflightWithin(ctx context.Context, target cluster.Carrier, within time.Duration) (cluster.Report, error)
	Request(ctx context.Context, req cluster.Request) (cluster.CarrierRow, cluster.Report, error)
	Abort(ctx context.Context, by string) (cluster.CarrierRow, error)
}

// ReplicaProber asks the replicas to look at which carriers they could build.
type ReplicaProber interface {
	// ProbeReplicas asks and waits for the answers, or for a time limit.
	ProbeReplicas(ctx context.Context)
	// AskReplicas asks and does not wait.
	AskReplicas(ctx context.Context)
}

// NATSChecker tells whether this replica can reach a NATS server with its own
// credentials.
type NATSChecker interface {
	CheckNATS(ctx context.Context, url string) error
}

// ClusterSettings is the store of the settings of the cluster.
// *cluster.SettingsStore is one.
type ClusterSettings interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value, by string) error
	Clear(ctx context.Context, key string) error
	All(ctx context.Context) (map[string]string, error)
}

func actor(c echo.Context) string {
	if u := auth.GetUser(c); u != nil {
		if u.Email != "" {
			return u.Email
		}
		return u.ID
	}
	return "admin"
}

func carrierError(c echo.Context, status int, msg string, extra map[string]any) error {
	body := map[string]any{"error": msg}
	for k, v := range extra {
		body[k] = v
	}
	return c.JSON(status, body)
}

// settingsView is the settings as an admin reads them. A user name, a password or a
// query string in the address of a server is dropped.
func settingsView(all map[string]string) ClusterSettingsView {
	view := ClusterSettingsView{
		PrepareTimeout:   all[cluster.SettingPrepareTimeout],
		TransitionWindow: all[cluster.SettingTransitionWindow],
		MaxDrain:         all[cluster.SettingMaxDrain],
	}
	if v := all[cluster.SettingNATSURL]; v != "" {
		view.NATSURL = cluster.PublicNATSURL(v)
	}
	if v := all[cluster.SettingNATSWorkerURL]; v != "" {
		view.NATSWorkerURL = cluster.PublicNATSURL(v)
	}
	return view
}

// ClusterSettingsView is the settings of the cluster as an admin reads them. A
// value that is not stored is empty and the default applies.
type ClusterSettingsView struct {
	// NATSURL is the NATS address that the frontends use.
	NATSURL string `json:"nats_url"`
	// NATSWorkerURL is the NATS address that workers are told to use. Empty means NATSURL.
	NATSWorkerURL string `json:"nats_worker_url"`
	// PrepareTimeout is how long a change waits for every replica to be ready (a Go duration).
	PrepareTimeout string `json:"prepare_timeout"`
	// TransitionWindow is how long a replica may take to confirm a commit (a Go duration).
	TransitionWindow string `json:"transition_window"`
	// MaxDrain is how long the previous carrier stays attached after a commit (a Go duration).
	MaxDrain string `json:"max_drain"`
}

// carrierView is the report with the settings next to it.
type CarrierStatusResponse struct {
	cluster.Report
	Settings ClusterSettingsView `json:"settings"`
}

// GetCarrierEndpoint reports the state of the carrier of the cluster: the active
// carrier, the epoch, the state of a change, each replica and whether it is ready,
// the workers that could not follow a change to the other carrier and why, the
// work in flight, and the list of live replicas for the admin to confirm before a
// change.
//
// @Summary Report the transport of a distributed cluster
// @Description Returns the active carrier (nats or tunnel), the epoch, the state of a change (stable, prepare or commit), the live replicas with their readiness, the workers and whether each could follow a change to the other carrier, the work in flight, and the cluster settings. Admin only. Answers 503 when distributed mode is off.
// @Tags Nodes
// @Success 200 {object} localai.CarrierStatusResponse
// @Failure 503 {object} map[string]string "Distributed mode is not enabled"
// @Router /api/cluster/carrier [get]
func GetCarrierEndpoint(sw CarrierSwitchAdmin, settings ClusterSettings) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		status, err := sw.Status(ctx)
		if err != nil {
			return carrierError(c, http.StatusInternalServerError, err.Error(), nil)
		}
		// The report is for a change to the other carrier, so the workers that could
		// not follow it are listed with their reasons.
		other := cluster.CarrierTunnel
		if status.Active == cluster.CarrierTunnel {
			other = cluster.CarrierNATS
		}
		report, err := sw.Preflight(ctx, other)
		if err != nil {
			return carrierError(c, http.StatusInternalServerError, err.Error(), nil)
		}
		all, err := settings.All(ctx)
		if err != nil {
			return carrierError(c, http.StatusInternalServerError, err.Error(), nil)
		}
		return c.JSON(http.StatusOK, CarrierStatusResponse{Report: report, Settings: settingsView(all)})
	}
}

// CarrierSwitchRequest is the body of POST /api/cluster/carrier.
type CarrierSwitchRequest struct {
	Target string `json:"target"`
	DryRun bool   `json:"dry_run"`
	Force  bool   `json:"force"`
	Abort  bool   `json:"abort"`
}

// SwitchCarrierEndpoint changes the carrier of the cluster. It takes
// {"target": "nats"|"tunnel", "dry_run": bool, "force": bool}, or {"abort": true}
// to abort a change that is being prepared.
//
// A dry run asks every replica to look at what it can build, runs the preflight,
// and changes nothing. A request runs the same preflight and starts the change
// when nothing blocks. The change itself is carried out by the replicas and led by
// one of them, so the answer is 202: it says what was started and the epoch to
// watch.
//
// @Summary Dry-run, start or abort a change of carrier
// @Description Takes a target (nats or tunnel) with dry_run and force, or abort. A dry run returns the preflight report and changes nothing. A request starts the change and returns 202 with the epoch to watch. A request that a blocker stops returns 422 with the blockers; force accepts the blockers that are forceable. A change already under way returns 409, and only abort is accepted then. Admin only.
// @Tags Nodes
// @Param request body localai.CarrierSwitchRequest true "target, dry_run, force or abort"
// @Success 200 {object} cluster.Report "Dry run report, or the row after an abort"
// @Success 202 {object} map[string]interface{} "Change started: state, active, target, epoch, force, warnings, row"
// @Failure 400 {object} map[string]string "Bad body or target"
// @Failure 409 {object} map[string]string "A change is under way, or there is nothing to abort"
// @Failure 422 {object} map[string]interface{} "Blocked: error, blockers, report"
// @Failure 503 {object} map[string]string "Distributed mode is not enabled"
// @Router /api/cluster/carrier [post]
func SwitchCarrierEndpoint(sw CarrierSwitchAdmin, prober ReplicaProber) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req CarrierSwitchRequest
		if err := c.Bind(&req); err != nil {
			return carrierError(c, http.StatusBadRequest, "invalid request body", nil)
		}
		ctx := c.Request().Context()
		by := actor(c)

		if req.Abort {
			row, err := sw.Abort(ctx, by)
			switch {
			case errors.Is(err, cluster.ErrNotAbortable):
				return carrierError(c, http.StatusConflict, err.Error(), nil)
			case err != nil:
				return carrierError(c, http.StatusInternalServerError, err.Error(), nil)
			}
			xlog.Warn("Change of carrier aborted by an admin", "by", by, "epoch", row.Epoch)
			return c.JSON(http.StatusOK, row)
		}

		target := cluster.Carrier(req.Target)
		if target != cluster.CarrierNATS && target != cluster.CarrierTunnel {
			return carrierError(c, http.StatusBadRequest, fmt.Sprintf("target must be %q or %q", cluster.CarrierNATS, cluster.CarrierTunnel), nil)
		}
		// The replicas write what they can build when they are asked, so the answer
		// of the preflight is about now and not about the last time they looked.
		// A report counts as the answer only if it was written after the question.
		asked := time.Now()
		prober.ProbeReplicas(ctx)
		answeredWithin := time.Since(asked)

		if req.DryRun {
			report, err := sw.PreflightWithin(ctx, target, answeredWithin)
			if err != nil {
				return carrierError(c, http.StatusInternalServerError, err.Error(), nil)
			}
			return c.JSON(http.StatusOK, report)
		}

		row, report, err := sw.Request(ctx, cluster.Request{Target: target, By: by, Force: req.Force, ReportedWithin: answeredWithin})
		var blocked *cluster.BlockedError
		switch {
		case errors.As(err, &blocked):
			return carrierError(c, http.StatusUnprocessableEntity, err.Error(), map[string]any{"blockers": blocked.Report.Blockers, "report": blocked.Report})
		case errors.Is(err, cluster.ErrBusy):
			return carrierError(c, http.StatusConflict, err.Error(), nil)
		case err != nil:
			return carrierError(c, http.StatusInternalServerError, err.Error(), nil)
		}
		xlog.Info("Change of carrier started by an admin", "by", by, "target", target, "force", req.Force, "epoch", row.Epoch)
		return c.JSON(http.StatusAccepted, map[string]any{
			"state": row.State, "active": row.Active, "target": row.Target, "epoch": row.Epoch,
			"force": row.Force, "warnings": report.Warnings, "row": row,
		})
	}
}

// GetClusterSettingsEndpoint reads the settings of the cluster.
//
// @Summary Read the settings of the cluster
// @Description Returns the NATS address of the frontends and of the workers and the waits of a change. A user name, password or query string in an address is dropped. Admin only.
// @Tags Nodes
// @Success 200 {object} localai.ClusterSettingsView
// @Failure 503 {object} map[string]string "Distributed mode is not enabled"
// @Router /api/cluster/settings [get]
func GetClusterSettingsEndpoint(settings ClusterSettings) echo.HandlerFunc {
	return func(c echo.Context) error {
		all, err := settings.All(c.Request().Context())
		if err != nil {
			return carrierError(c, http.StatusInternalServerError, err.Error(), nil)
		}
		return c.JSON(http.StatusOK, settingsView(all))
	}
}

// ClusterSettingsRequest is the body of PUT /api/cluster/settings. A field that
// is absent is left as it is, and an empty string clears the setting.
type ClusterSettingsRequest struct {
	NATSURL          *string `json:"nats_url"`
	NATSWorkerURL    *string `json:"nats_worker_url"`
	PrepareTimeout   *string `json:"prepare_timeout"`
	TransitionWindow *string `json:"transition_window"`
	MaxDrain         *string `json:"max_drain"`
}

// natsCheckTimeout bounds the reachability check of a saved URL.
const natsCheckTimeout = 8 * time.Second

// CarrierStatus reads the state of the cluster carrier. *cluster.Switch is one.
type CarrierStatus interface {
	Status(ctx context.Context) (cluster.Report, error)
}

// PutClusterSettingsEndpoint stores settings of the cluster. It stores nothing
// unless it can store everything: the request is checked as a whole first.
//
// A NATS URL is stored only when no change of carrier is under way and this
// replica reaches the server at that address. A change builds the NATS carrier
// from the address that the row of the change holds, so an edit during one
// would only confuse the operator; an address that does not answer would stay in
// the settings until a change to NATS failed on it. Saving an address makes NATS
// available; it does not switch the cluster to NATS. The change of carrier is a
// separate request with a dry run. Every replica checks the stored URL on its
// next report, and a dry run asks them to.
//
// No address may carry a user name, a password or a query string: the settings
// are shared, and the credentials of NATS stay on each replica.
//
// @Summary Store settings of the cluster
// @Description Stores the NATS address, the address workers use, or the waits of a change. Saving a NATS address checks that the serving replica reaches it and makes NATS available; it does not switch the cluster. Admin only.
// @Tags Nodes
// @Param request body localai.ClusterSettingsRequest true "settings to store"
// @Success 200 {object} map[string]interface{} "saved, nats.reachable when a NATS address was checked, settings"
// @Failure 400 {object} map[string]string "Unknown setting, bad value, or a credential in an address"
// @Failure 409 {object} map[string]string "A change of carrier is under way"
// @Failure 422 {object} map[string]string "This replica cannot reach the NATS server"
// @Failure 503 {object} map[string]string "Distributed mode is not enabled"
// @Router /api/cluster/settings [put]
func PutClusterSettingsEndpoint(settings ClusterSettings, checker NATSChecker, prober ReplicaProber, state CarrierStatus) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req ClusterSettingsRequest
		if err := c.Bind(&req); err != nil {
			return carrierError(c, http.StatusBadRequest, "invalid request body", nil)
		}
		ctx := c.Request().Context()
		by := actor(c)

		type change struct {
			key string
			val *string
		}
		changes := []change{
			{cluster.SettingNATSURL, req.NATSURL},
			{cluster.SettingNATSWorkerURL, req.NATSWorkerURL},
			{cluster.SettingPrepareTimeout, req.PrepareTimeout},
			{cluster.SettingTransitionWindow, req.TransitionWindow},
			{cluster.SettingMaxDrain, req.MaxDrain},
		}
		// Validate everything before storing anything. The store checks again, and a
		// request that it would refuse half way must not be accepted here.
		for _, ch := range changes {
			if ch.val == nil || *ch.val == "" {
				continue
			}
			if err := cluster.CheckSetting(ch.key, *ch.val); err != nil {
				return carrierError(c, http.StatusBadRequest, err.Error(), nil)
			}
		}

		resp := map[string]any{"saved": true}
		if req.NATSURL != nil {
			st, err := state.Status(ctx)
			if err != nil {
				return carrierError(c, http.StatusInternalServerError, err.Error(), nil)
			}
			if st.State != cluster.StateStable {
				return carrierError(c, http.StatusConflict, fmt.Sprintf(
					"the NATS URL cannot be changed while a change of carrier is under way (%s to %s): wait for it to settle or abort it",
					st.State, st.Row.Target), nil)
			}
			if *req.NATSURL != "" {
				checkCtx, cancel := context.WithTimeout(ctx, natsCheckTimeout)
				err := checker.CheckNATS(checkCtx, *req.NATSURL)
				cancel()
				if err != nil {
					xlog.Warn("The NATS URL was refused: this replica cannot reach it", "error", err)
					return carrierError(c, http.StatusUnprocessableEntity, fmt.Sprintf(
						"nats_url: this replica cannot reach the NATS server at %s: %v", cluster.PublicNATSURL(*req.NATSURL), err), nil)
				}
				resp["nats"] = map[string]any{"reachable": true}
			}
		}

		for _, ch := range changes {
			if ch.val == nil {
				continue
			}
			var err error
			if *ch.val == "" {
				err = settings.Clear(ctx, ch.key)
			} else {
				err = settings.Set(ctx, ch.key, *ch.val, by)
			}
			switch {
			case errors.Is(err, cluster.ErrUnknownSetting), errors.Is(err, cluster.ErrCredentialInURL):
				return carrierError(c, http.StatusBadRequest, err.Error(), nil)
			case err != nil:
				return carrierError(c, http.StatusInternalServerError, err.Error(), nil)
			}
		}
		xlog.Info("Cluster settings changed", "by", by)

		if req.NATSURL != nil || req.NATSWorkerURL != nil {
			// The other replicas look at the new address and write what they find.
			prober.AskReplicas(ctx)
		}
		all, err := settings.All(ctx)
		if err != nil {
			return carrierError(c, http.StatusInternalServerError, err.Error(), nil)
		}
		resp["settings"] = settingsView(all)
		return c.JSON(http.StatusOK, resp)
	}
}

// UnavailableCarrierEndpoint answers 503. The routes of the carrier are
// registered on every frontend, and a frontend that is not distributed has no
// cluster to change.
func UnavailableCarrierEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		return carrierError(c, http.StatusServiceUnavailable, "distributed mode is not enabled on this frontend", nil)
	}
}
