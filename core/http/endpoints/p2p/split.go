package p2p

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
)

// SplitService is what the model-split handlers need from the P2P layer.
type SplitService interface {
	ListSplitWorkers(ctx context.Context) (SplitWorkerList, error)
	SetSplitWorker(ctx context.Context, id string, req SplitWorkerUpdate) (SplitWorkerView, error)
}

// SplitWorkerView is one model-split worker: a paired device of kind split
// that lends its memory to models that run on this server, or on the backend
// node of the cluster it is attached to.
type SplitWorkerView struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	PeerID string `json:"peer_id"`
	// Engine is llama (llama.cpp RPC) or mlx.
	Engine string `json:"engine"`
	// State is online (in use), offline, slow (only a relay reaches it: it
	// waits for allow_slow_path), limited (only a public relay reaches it,
	// whose limits cannot carry model layers) or refused.
	State string `json:"state"`
	// Error says why a refused worker was refused.
	Error string `json:"error,omitempty"`
	// AllowSlowPath is the "Use anyway" choice for a relayed path.
	AllowSlowPath bool     `json:"allow_slow_path"`
	Path          PathView `json:"path"`
	// RelayID is the server's ID of the relay of a relayed path, when the
	// relay is one the server knows (see /api/p2p/relays/{id}/limits).
	RelayID string `json:"relay_id,omitempty"`
	// OwnRelay is true when that relay is a gated relay this server
	// administers: its limits can change. With a data or time limit the
	// relay ends each connection at the limit, also after allow_slow_path.
	OwnRelay bool `json:"own_relay,omitempty"`
	// MemoryBytes is the memory the worker reported: its VRAM, or its RAM
	// when it has no GPU (unified memory on Apple Silicon).
	MemoryBytes uint64 `json:"memory_bytes"`
	// Name and Version are what the worker reported.
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
	// Addr is the loopback address that reaches the worker, on this server
	// or on the node of NodeID: an entry of LLAMACPP_GRPC_SERVERS, or of the
	// MLX hostfile.
	Addr string `json:"addr,omitempty"`
	// NodeID is the backend node the worker is attached to; empty means
	// this server. NodeName is its name. The state, path and address of an
	// attached worker are what the node last reported.
	NodeID   string `json:"node_id,omitempty"`
	NodeName string `json:"node_name,omitempty"`
	// NodeOffline is true when the node of NodeID is offline: the worker
	// stays attached and waits for it; nothing moves it.
	NodeOffline bool `json:"node_offline,omitempty"`
	// NodeNote says why a worker attached to no node was detached, for
	// example because its node stopped being a split server.
	NodeNote string `json:"node_note,omitempty"`
	// NodeRelay says whether the gated relays of this server admit the node
	// of NodeID, so that the worker can reach it through them when no direct
	// or hole-punched path works. Set only for a worker attached to a node.
	NodeRelay *SplitNodeRelay `json:"node_relay,omitempty"`
	// Rank is the MLX rank of the worker; zero when not in the ring.
	Rank           int        `json:"rank,omitempty"`
	ConnectedSince *time.Time `json:"connected_since,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
}

// States of SplitNodeRelay.
const (
	// NodeRelayAdmitted: at least one gated relay of this server admits the
	// node.
	NodeRelayAdmitted = "admitted"
	// NodeRelayRefused: the node is not admitted, and will not be until
	// something changes (a relay is full, the node is not approved, or its
	// registration token is no longer valid). Reason says which.
	NodeRelayRefused = "refused"
	// NodeRelayPending: the node is not admitted yet: it has not reported its
	// host, it is offline, or the relays are not in sync yet.
	NodeRelayPending = "pending"
	// NodeRelayNotNeeded: the node joined over P2P and the relays admit it
	// as a paired device, or this server administers no gated relay.
	NodeRelayNotNeeded = "not_needed"
)

// SplitNodeRelay is the relay admission of the backend node of a split
// worker.
type SplitNodeRelay struct {
	// State is admitted, refused, pending or not_needed.
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
	// Relays is the state on each gated relay that this server administers.
	Relays []SplitRelayAdmission `json:"relays,omitempty"`
}

// SplitRelayAdmission is the admission of a node on one relay.
type SplitRelayAdmission struct {
	RelayID string `json:"relay_id"`
	Label   string `json:"label,omitempty"`
	// State is admitted, refused or pending.
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`
}

// SplitWorkerList is the answer of GET /api/p2p/split-workers.
type SplitWorkerList struct {
	Workers []SplitWorkerView `json:"workers"`
	// MemoryInUse is the memory of the workers in use, in bytes.
	MemoryInUse uint64 `json:"memory_in_use"`
	// Distributed is true in a cluster: models run on the backend nodes, so
	// a worker is used only when it is attached to one (node_id).
	Distributed bool `json:"distributed,omitempty"`
}

// SplitWorkerUpdate is the body of PATCH /api/p2p/split-workers/{id}. At
// least one field is required.
type SplitWorkerUpdate struct {
	// AllowSlowPath uses the worker over a relayed path ("Use anyway"), or
	// stops using it there.
	AllowSlowPath *bool `json:"allow_slow_path,omitempty"`
	// NodeID attaches the worker to a backend node of the cluster, or to
	// this server with "".
	NodeID *string `json:"node_id,omitempty"`
}

// ListSplitWorkersEndpoint lists the model-split workers.
//
//	@Summary	List model-split workers
//	@Description	Lists the paired llama.cpp RPC and MLX workers that lend their memory to models that run on this server or, in a cluster, on the backend node each is attached to (node_id), with their state (online, offline, slow, limited, refused), the path that reaches them and the memory they add. A worker reached only through a relay is "slow" until allow_slow_path is set. A relay of the user (own_relay: a gated relay this server administers) may carry it even with a data or time limit, and then ends each connection at the limit; its limits change with PUT /api/p2p/relays/{relay_id}/limits. A public relay never carries model layers ("limited"). Needs P2P mode share. Admin only.
//	@Tags		p2p
//	@Produce	json
//	@Success	200	{object}	SplitWorkerList
//	@Failure	409	{object}	schema.ErrorResponse
//	@Router		/api/p2p/split-workers [get]
func ListSplitWorkersEndpoint(svc SplitService) echo.HandlerFunc {
	return func(c echo.Context) error {
		list, err := svc.ListSplitWorkers(c.Request().Context())
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, list)
	}
}

// SetSplitWorkerEndpoint changes how the server uses a model-split worker.
//
//	@Summary	Use a model-split worker over a slow path, or attach it to a node
//	@Description	Sets node_id and/or allow_slow_path of a model-split worker. node_id attaches the worker to a backend node of the cluster ("" attaches it to this server): the worker moves to the node at its next session, the node forwards its port and the backends that the node starts use it. Pin the model to that node with a node selector. With allow_slow_path true the server or the node uses the worker over a relay ("Use anyway"): it works, but every token waits for the relay, and a relay of the user that limits each connection ends it at the limit. A public relay is never used. With false it uses the worker only over a direct or hole-punched path. A model that is loaded keeps its workers until it is loaded again. Needs P2P mode share. Admin only.
//	@Tags		p2p
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string				true	"Device ID of the worker"
//	@Param		request	body		SplitWorkerUpdate	true	"Change"
//	@Success	200		{object}	SplitWorkerView
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	404		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Router		/api/p2p/split-workers/{id} [patch]
func SetSplitWorkerEndpoint(svc SplitService) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req SplitWorkerUpdate
		if err := bind(c, &req); err != nil {
			return err
		}
		if req.AllowSlowPath == nil && req.NodeID == nil {
			return fail(c, fmt.Errorf("%w: allow_slow_path or node_id is required", ErrInvalid))
		}
		v, err := svc.SetSplitWorker(c.Request().Context(), c.Param("id"), req)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, v)
	}
}
