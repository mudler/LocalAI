package localai

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/xlog"
)

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
	// Apply stores and clears several settings in one transaction.
	Apply(ctx context.Context, changes []cluster.SettingChange, by string) error
	All(ctx context.Context) (map[string]string, error)
}

// ClusterState is what this frontend knows about the cluster around it: the
// broadcast bus it publishes on now, and the live frontend replicas.
type ClusterState interface {
	Bus() carrier.BusState
	Replicas(ctx context.Context) ([]cluster.LiveInstance, error)
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

func clusterError(c echo.Context, status int, msg string, extra map[string]any) error {
	body := map[string]any{"error": msg}
	maps.Copy(body, extra)
	return c.JSON(status, body)
}

// settingsView is the settings as an admin reads them. A user name, a password or a
// query string in the address of a server is dropped.
func settingsView(all map[string]string) ClusterSettingsView {
	view := ClusterSettingsView{
		CarrierWeights:        cluster.CarrierWeightsFrom(all),
		CarrierWeightsDefault: cluster.DefaultCarrierWeights,
		JoinAutoApprove:       cluster.JoinAutoApproveFrom(all),
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
	// CarrierWeights are the weights that break a tie between equally fit nodes
	// reached over different carriers (nats, websocket, p2p). A weight that is not
	// stored is the default, so these are always the values in force.
	CarrierWeights cluster.CarrierWeights `json:"carrier_weights"`
	// CarrierWeightsDefault are the weights that apply when none is stored.
	CarrierWeightsDefault cluster.CarrierWeights `json:"carrier_weights_default"`
	// JoinAutoApprove is the default of the auto-approve choice of a new join
	// token: when true, a worker that registers with such a token joins with no
	// admin approval. Off unless set.
	JoinAutoApprove bool `json:"join_auto_approve"`
}

// ReplicaView is one live frontend replica.
type ReplicaView struct {
	ID       string    `json:"id"`
	Version  string    `json:"version,omitempty"`
	LastSeen time.Time `json:"last_seen"`
}

// ClusterSettingsResponse is the answer of GET /api/cluster/settings: the
// settings, the broadcast bus of the frontend that answers, and the live
// frontend replicas.
type ClusterSettingsResponse struct {
	ClusterSettingsView
	// Bus is the broadcast bus of the frontend that answers, read from its own
	// connection: NATS while it reaches the NATS server, PostgreSQL otherwise.
	Bus carrier.BusState `json:"bus"`
	// Replicas are the live frontend replicas.
	Replicas []ReplicaView `json:"replicas"`
}

// GetClusterSettingsEndpoint reads the settings of the cluster.
//
// @Summary Read the settings of the cluster
// @Description Returns the NATS address of the frontends and of the workers, the carrier weights (nats, websocket, p2p) that break a tie between equally fit nodes, the broadcast bus of the frontend that answers (nats while it reaches the NATS server, postgres otherwise), and the live frontend replicas. A user name, password or query string in an address is dropped. Admin only.
// @Tags Nodes
// @Success 200 {object} localai.ClusterSettingsResponse
// @Failure 503 {object} map[string]string "Distributed mode is not enabled"
// @Router /api/cluster/settings [get]
func GetClusterSettingsEndpoint(settings ClusterSettings, state ClusterState) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		all, err := settings.All(ctx)
		if err != nil {
			return clusterError(c, http.StatusInternalServerError, err.Error(), nil)
		}
		live, err := state.Replicas(ctx)
		if err != nil {
			return clusterError(c, http.StatusInternalServerError, err.Error(), nil)
		}
		replicas := make([]ReplicaView, 0, len(live))
		for _, r := range live {
			replicas = append(replicas, ReplicaView{ID: r.ID, Version: r.Version, LastSeen: r.LastSeen})
		}
		return c.JSON(http.StatusOK, ClusterSettingsResponse{ClusterSettingsView: settingsView(all), Bus: state.Bus(), Replicas: replicas})
	}
}

// ClusterSettingsRequest is the body of PUT /api/cluster/settings. A field that
// is absent is left as it is, and an empty string clears the setting.
type ClusterSettingsRequest struct {
	NATSURL       *string `json:"nats_url"`
	NATSWorkerURL *string `json:"nats_worker_url"`
	// CarrierWeights sets some or all of nats, websocket and p2p, each a whole
	// number from 0 to 1000. A key that is absent stays as it is, null puts the
	// default back, and a key that is not one of the three is refused.
	CarrierWeights map[string]*int `json:"carrier_weights"`
	// JoinAutoApprove sets the default of the auto-approve choice of a new join
	// token. null puts the default (off) back.
	JoinAutoApprove *bool `json:"join_auto_approve"`
}

// natsCheckTimeout bounds the reachability check of a saved URL.
const natsCheckTimeout = 8 * time.Second

// PutClusterSettingsEndpoint stores settings of the cluster. It stores nothing
// unless it can store everything: the request is checked as a whole first.
//
// A NATS URL is stored only when this replica reaches the server at that
// address, so that an address that does not answer is not stored for every
// replica. Every frontend reads the address at start: a new address takes
// effect when the frontends restart.
//
// No address may carry a user name, a password or a query string: the settings
// are shared, and the credentials of NATS stay on each replica.
//
// @Summary Store settings of the cluster
// @Description Stores the NATS address, the address workers use, join_auto_approve (the default of the auto-approve choice of a new join token, off unless set), or the carrier weights (carrier_weights: nats, websocket and p2p, each 0 to 1000; a weight is a tie-break between equally fit nodes and never overrides fit; null puts the default back; the change applies to every frontend within seconds, with no restart). Saving a NATS address checks that the serving replica reaches it; the frontends use a new address after a restart. Admin only.
// @Tags Nodes
// @Param request body localai.ClusterSettingsRequest true "settings to store"
// @Success 200 {object} map[string]interface{} "saved, nats.reachable when a NATS address was checked, settings"
// @Failure 400 {object} map[string]string "Unknown setting, bad value, or a credential in an address"
// @Failure 422 {object} map[string]string "This replica cannot reach the NATS server"
// @Failure 503 {object} map[string]string "Distributed mode is not enabled"
// @Router /api/cluster/settings [put]
func PutClusterSettingsEndpoint(settings ClusterSettings, checker NATSChecker) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req ClusterSettingsRequest
		if err := c.Bind(&req); err != nil {
			return clusterError(c, http.StatusBadRequest, "invalid request body", nil)
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
		}
		for name, v := range req.CarrierWeights {
			key, ok := cluster.CarrierWeightKeys[name]
			if !ok {
				return clusterError(c, http.StatusBadRequest, fmt.Sprintf("carrier_weights: unknown carrier %q: use nats, websocket or p2p", name), nil)
			}
			if v == nil {
				continue
			}
			if err := cluster.CheckSetting(key, strconv.Itoa(*v)); err != nil {
				return clusterError(c, http.StatusBadRequest, fmt.Sprintf("carrier_weights.%s: %v", name, err), nil)
			}
		}
		// Validate everything before storing anything. The store checks again, and a
		// request that it would refuse half way must not be accepted here.
		for _, ch := range changes {
			if ch.val == nil || *ch.val == "" {
				continue
			}
			if err := cluster.CheckSetting(ch.key, *ch.val); err != nil {
				return clusterError(c, http.StatusBadRequest, err.Error(), nil)
			}
		}

		resp := map[string]any{"saved": true}
		if req.NATSURL != nil && *req.NATSURL != "" {
			checkCtx, cancel := context.WithTimeout(ctx, natsCheckTimeout)
			err := checker.CheckNATS(checkCtx, *req.NATSURL)
			cancel()
			if err != nil {
				xlog.Warn("The NATS URL was refused: this replica cannot reach it", "error", err)
				return clusterError(c, http.StatusUnprocessableEntity, fmt.Sprintf(
					"nats_url: this replica cannot reach the NATS server at %s: %v", cluster.PublicNATSURL(*req.NATSURL), err), nil)
			}
			resp["nats"] = map[string]any{"reachable": true}
		}

		// One transaction: a request that fails half way changes nothing.
		var batch []cluster.SettingChange
		for _, ch := range changes {
			if ch.val == nil {
				continue
			}
			batch = append(batch, cluster.SettingChange{Key: ch.key, Value: *ch.val, Clear: *ch.val == ""})
		}
		if req.JoinAutoApprove != nil {
			batch = append(batch, cluster.SettingChange{Key: cluster.SettingJoinAutoApprove, Value: strconv.FormatBool(*req.JoinAutoApprove)})
		}
		// In name order, so that two requests that carry the same weights take the
		// row locks in the same order.
		names := slices.Sorted(maps.Keys(req.CarrierWeights))
		for _, name := range names {
			v := req.CarrierWeights[name]
			key := cluster.CarrierWeightKeys[name]
			if v == nil {
				batch = append(batch, cluster.SettingChange{Key: key, Clear: true})
				continue
			}
			batch = append(batch, cluster.SettingChange{Key: key, Value: strconv.Itoa(*v)})
		}
		if err := settings.Apply(ctx, batch, by); err != nil {
			switch {
			case errors.Is(err, cluster.ErrUnknownSetting), errors.Is(err, cluster.ErrCredentialInURL):
				return clusterError(c, http.StatusBadRequest, err.Error(), nil)
			}
			return clusterError(c, http.StatusInternalServerError, err.Error(), nil)
		}
		xlog.Info("Cluster settings changed", "by", by)

		all, err := settings.All(ctx)
		if err != nil {
			return clusterError(c, http.StatusInternalServerError, err.Error(), nil)
		}
		resp["settings"] = settingsView(all)
		return c.JSON(http.StatusOK, resp)
	}
}

// UnavailableClusterEndpoint answers 503. The cluster routes are registered on
// every frontend, and a frontend that is not distributed has no cluster.
func UnavailableClusterEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		return clusterError(c, http.StatusServiceUnavailable, "distributed mode is not enabled on this frontend", nil)
	}
}
