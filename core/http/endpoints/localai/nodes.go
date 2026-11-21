package localai

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/http/auth"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
	"github.com/mudler/LocalAI/core/services/workerctl"
	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/LocalAI/pkg/natsauth"
	"github.com/mudler/LocalAI/pkg/vrambudget"
)

// nodeError builds a schema.ErrorResponse for node endpoints.
func nodeError(code int, message string) schema.ErrorResponse {
	return schema.ErrorResponse{
		Error: &schema.APIError{
			Code:    code,
			Message: message,
			Type:    "node_error",
		},
	}
}

// NodeRouteStates says which routes of a node are live. nodes.RouteJudge is one.
type NodeRouteStates interface {
	States(ctx context.Context, n *nodes.BackendNode, now time.Time) []nodes.RouteState
}

// WithRouteStates makes the node list and the node detail carry route_state:
// for each route of the node (heartbeat, nats, tunnel), whether it is live.
func WithRouteStates(r NodeRouteStates) RegisterOption {
	return func(o *registerOptions) { o.routes = r }
}

func (o registerOptions) fillRouteStates(ctx context.Context, list []nodes.NodeWithExtras) {
	if o.routes == nil {
		return
	}
	now := time.Now()
	for i := range list {
		list[i].RouteState = o.routes.States(ctx, &list[i].BackendNode, now)
	}
}

// ListNodesEndpoint returns all registered backend nodes.
func ListNodesEndpoint(registry *nodes.NodeRegistry, options ...RegisterOption) echo.HandlerFunc {
	opts := newRegisterOptions(options)
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeList, err := registry.ListWithExtras(ctx)
		if err != nil {
			xlog.Error("Failed to list nodes", "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to list nodes"))
		}
		opts.fillRouteStates(ctx, nodeList)
		return c.JSON(http.StatusOK, nodeList)
	}
}

// GetNodeEndpoint returns a single node by ID.
func GetNodeEndpoint(registry *nodes.NodeRegistry, options ...RegisterOption) echo.HandlerFunc {
	opts := newRegisterOptions(options)
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		// GetWithExtras (not Get) so the response carries the node's labels,
		// loaded-model count, and in-flight total — the bare BackendNode keeps
		// labels in a separate table, leaving the detail view's label list empty.
		node, err := registry.GetWithExtras(ctx, id)
		if err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		if node != nil && opts.routes != nil {
			node.RouteState = opts.routes.States(ctx, &node.BackendNode, time.Now())
		}
		return c.JSON(http.StatusOK, node)
	}
}

// RegisterNodeRequest is the request body for registering a new worker node.
type RegisterNodeRequest struct {
	Name            string  `json:"name"`
	NodeType        string  `json:"node_type,omitempty"` // "backend" (default) or "agent"
	Address         string  `json:"address"`
	HTTPAddress     string  `json:"http_address,omitempty"`
	Token           string  `json:"token,omitempty"`
	TotalVRAM       uint64  `json:"total_vram,omitempty"`
	AvailableVRAM   uint64  `json:"available_vram,omitempty"`
	TotalRAM        uint64  `json:"total_ram,omitempty"`
	AvailableRAM    uint64  `json:"available_ram,omitempty"`
	CPULogicalCores uint64  `json:"cpu_logical_cores,omitempty"`
	CPUUsagePercent float64 `json:"cpu_usage_percent,omitempty"`
	CPULoad1        float64 `json:"cpu_load_1,omitempty"`
	// TotalDisk / AvailableDisk describe the filesystem backing the worker's
	// MODELS directory (where staged weights land), not the root filesystem.
	// Omitted by workers that predate the fields; the scheduler treats
	// total_disk == 0 as "unknown" and leaves such a node in rotation.
	TotalDisk     uint64 `json:"total_disk,omitempty"`
	AvailableDisk uint64 `json:"available_disk,omitempty"`
	GPUVendor     string `json:"gpu_vendor,omitempty"`
	// GPUComputeCapability is the worker GPU's compute capability ("major.minor",
	// e.g. "12.1" for GB10). Used by the router for per-arch option tuning.
	GPUComputeCapability string `json:"gpu_compute_capability,omitempty"`
	// Capability is the worker's own meta-backend capability string. The
	// controller stores it so backend discovery can reflect what the cluster
	// can run rather than what the (typically GPU-less) controller can run.
	Capability string            `json:"capability,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	// SplitServer is the --split-server flag of the worker: it declares the
	// worker a split server, unless the admin chose otherwise.
	SplitServer bool `json:"split_server,omitempty"`
	// MaxReplicasPerModel is the per-node cap on replicas of any single model.
	// Workers older than this field omit it; we coerce 0 → 1 below to preserve
	// historical single-replica behavior.
	MaxReplicasPerModel int `json:"max_replicas_per_model,omitempty"`
	// VRAMBudget is the worker's operator-set VRAM cap ("80%" or "12GB"). The
	// registry resolves and enforces it against the raw reported VRAM.
	VRAMBudget string `json:"vram_budget,omitempty"`
	// Version is the LocalAI build version reported by the worker at
	// registration. Empty for workers registered before this field existed.
	Version string `json:"version,omitempty"`
	// Commit is the git commit hash the worker binary was built from.
	Commit string `json:"commit,omitempty"`
	// Routable says if the address of the worker can be reached from the
	// frontends. A worker started with an address, or holding the NATS route,
	// sends true. A worker that holds only a tunnel and has no address sends
	// false: nothing dials it, and its addresses are dropped. Nothing sent means
	// true.
	Routable *bool `json:"routable,omitempty"`
	// Routes are the routes ("nats", "tunnel") whose credential the
	// registration asks for. The answer mints one for each: the tunnel token for
	// "tunnel", the NATS user JWT with the NATS address for "nats". A worker asks
	// for one route at a time, so that the refresh of its NATS credential never
	// replaces its tunnel token. No route gets the node ID and no credential.
	Routes []string `json:"routes,omitempty"`
}

// The routes a registration can ask a credential for. They are the names of
// the route columns that nodes.RoutesOf reads.
const (
	routeNATS   = "nats"
	routeTunnel = "tunnel"
)

// requestedRoutes reads which credentials a registration asks for. A name that
// is not a route is ignored.
func requestedRoutes(names []string) (nats, tunnel bool) {
	for _, n := range names {
		switch strings.TrimSpace(n) {
		case routeNATS:
			nats = true
		case routeTunnel:
			tunnel = true
		}
	}
	return nats, tunnel
}

type registerOptions struct {
	tunnels TunnelDisconnector
	nats    NATSHandover
	workers WorkerPeers
	split   SplitNodes
	routes  NodeRouteStates
}

// WorkerPeers says whether a peer of the P2P layer is a paired worker: a peer
// that redeemed a worker invite and is not revoked.
type WorkerPeers interface {
	IsWorkerPeer(ctx context.Context, peer string) bool
}

// WithWorkerPeers lets a paired worker that calls over P2P register and use
// the node routes without the registration token. The node is pinned to its
// peer: it may act on that node only, and the node's name cannot be taken by
// anyone else.
func WithWorkerPeers(w WorkerPeers) RegisterOption {
	return func(o *registerOptions) { o.workers = w }
}

// P2PWorkerPeer returns the peer of a request that a paired worker sent over
// P2P. A request that came over TCP never has one: the peer is set by the
// server of the P2P layer, not by anything the caller sends.
func P2PWorkerPeer(ctx context.Context, options ...RegisterOption) (string, bool) {
	return newRegisterOptions(options).workerPeer(ctx)
}

func (o registerOptions) workerPeer(ctx context.Context) (string, bool) {
	if o.workers == nil {
		return "", false
	}
	peer, ok := p2papi.PeerFrom(ctx)
	if !ok || !o.workers.IsWorkerPeer(ctx, peer) {
		return "", false
	}
	return peer, true
}

// NATSHandover says what a worker needs to reach NATS besides its own credential.
// The frontend hands it over in the answer to a registration that asks for the
// NATS route, so that a worker that was never given the address of NATS can
// attach to it.
type NATSHandover interface {
	// WorkerURL is the address that workers use. It is empty when no NATS is
	// configured.
	WorkerURL(ctx context.Context) (string, error)
	// CAPEM is the CA of the NATS server as a PEM bundle, or empty when the
	// server does not use a private CA.
	CAPEM() (string, error)
	// ClientTLS says that the server asks for a client certificate. Files of that
	// kind are not handed over, so a worker needs its own.
	ClientTLS() bool
}

// WithNATSHandover makes a registration that hands over a NATS credential also
// hand over the address of the server, its CA, and whether the worker needs a
// client certificate of its own.
func WithNATSHandover(h NATSHandover) RegisterOption {
	return func(o *registerOptions) { o.nats = h }
}

// TunnelDisconnector ends the tunnel that this replica holds for a node.
// *tunnel.Registry is one.
type TunnelDisconnector interface {
	Disconnect(nodeID string) bool
}

// WithTunnelDisconnector makes a registration that replaces the tunnel
// credential of a node, and the removal of a node, end the tunnel session that
// the replica holds for it. The credential is checked when a worker dials, so
// without this a session that was open stays open for a node that no longer has
// the right to it.
//
// It acts on this replica only. A session held by another replica ends when its
// worker dials again and is refused, or when the session ends for another
// reason.
func WithTunnelDisconnector(d TunnelDisconnector) RegisterOption {
	return func(o *registerOptions) { o.tunnels = d }
}

// RegisterOption changes how a registration or an approval answers.
type RegisterOption func(*registerOptions)

func newRegisterOptions(opts []RegisterOption) registerOptions {
	var o registerOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// RegisterNodeEndpoint registers a new backend node.
// expectedToken is the registration token configured on the frontend (may be empty to disable auth).
// autoApprove controls whether new nodes go directly to "healthy" or require admin approval.
func RegisterNodeEndpoint(registry *nodes.NodeRegistry, expectedToken string, autoApprove bool, authDB *gorm.DB, _ string, natsCfg natsauth.Config, options ...RegisterOption) echo.HandlerFunc {
	opts := newRegisterOptions(options)
	var openRegistrationWarning sync.Once
	return func(c echo.Context) error {
		var req RegisterNodeRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid request body"))
		}

		// A worker that joined with a worker invite proves who it is with its
		// peer ID, which the P2P layer authenticated. It has no registration
		// token.
		peer, viaP2P := opts.workerPeer(c.Request().Context())

		// Validate registration token if one is configured on the frontend
		if expectedToken != "" && !viaP2P {
			if req.Token == "" {
				return c.JSON(http.StatusUnauthorized, nodeError(http.StatusUnauthorized, "registration token required"))
			}
			expectedHash := sha256.Sum256([]byte(expectedToken))
			providedHash := sha256.Sum256([]byte(req.Token))
			if subtle.ConstantTimeCompare(expectedHash[:], providedHash[:]) != 1 {
				return c.JSON(http.StatusUnauthorized, nodeError(http.StatusUnauthorized, "invalid registration token"))
			}
		}

		// Determine node type
		nodeType := req.NodeType
		if nodeType == "" {
			nodeType = nodes.NodeTypeBackend
		}
		if nodeType != nodes.NodeTypeBackend && nodeType != nodes.NodeTypeAgent {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest,
				fmt.Sprintf("invalid node_type %q; must be %q or %q", nodeType, nodes.NodeTypeBackend, nodes.NodeTypeAgent)))
		}

		ctx := c.Request().Context()
		wantsNATS, wantsTunnel := requestedRoutes(req.Routes)
		// A worker over P2P reaches the cluster through its tunnel and nothing
		// else: it never gets a NATS credential, whatever it asks for.
		if viaP2P {
			wantsNATS = false
		}
		// A worker that said that its address cannot be reached holds only an
		// outbound tunnel and nothing dials it. Its address is dropped and not
		// stored: a dialable-looking endpoint that nothing may dial misleads. The
		// address of a worker over P2P is never stored.
		tunnelOnly := viaP2P || req.Routable != nil && !*req.Routable
		if tunnelOnly {
			req.Address, req.HTTPAddress = "", ""
		}

		// A backend worker on NATS is reached by a dial of its address, so a
		// registration for the NATS route needs one. Agent workers don't serve
		// gRPC, and a worker on the tunnel needs no address.
		if req.Name == "" {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "name is required"))
		}
		if nodeType == nodes.NodeTypeBackend && req.Address == "" && wantsNATS {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "address is required for a backend worker on the NATS route"))
		}
		if len(req.Name) > 255 {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "name exceeds 255 characters"))
		}
		if len(req.Address) > 512 {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "address exceeds 512 characters"))
		}
		if len(req.HTTPAddress) > 512 {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "http_address exceeds 512 characters"))
		}

		// Hash the token for storage (if provided)
		var tokenHash string
		if req.Token != "" {
			h := sha256.Sum256([]byte(req.Token))
			tokenHash = hex.EncodeToString(h[:])
		}

		// Coerce 0 → 1 for backward compat with workers that don't send the field.
		// GORM's `default:1` only fires for a missing column; once Go zero-values
		// reach the struct field they're written as 0 unless explicitly set here.
		maxReplicasPerModel := req.MaxReplicasPerModel
		if maxReplicasPerModel < 1 {
			maxReplicasPerModel = 1
		}

		node := &nodes.BackendNode{
			Name:                 req.Name,
			NodeType:             nodeType,
			Address:              req.Address,
			HTTPAddress:          req.HTTPAddress,
			TokenHash:            tokenHash,
			TotalVRAM:            req.TotalVRAM,
			AvailableVRAM:        req.AvailableVRAM,
			TotalRAM:             req.TotalRAM,
			AvailableRAM:         req.AvailableRAM,
			CPULogicalCores:      req.CPULogicalCores,
			CPUUsagePercent:      req.CPUUsagePercent,
			CPULoad1:             req.CPULoad1,
			TotalDisk:            req.TotalDisk,
			AvailableDisk:        req.AvailableDisk,
			GPUVendor:            req.GPUVendor,
			GPUComputeCapability: req.GPUComputeCapability,
			Capability:           req.Capability,
			MaxReplicasPerModel:  maxReplicasPerModel,
			SplitServer:          req.SplitServer && nodeType != nodes.NodeTypeAgent,
			VRAMBudget:           req.VRAMBudget,
			Version:              req.Version,
			Commit:               req.Commit,
			PeerID:               peer,
		}

		// A worker over P2P waits for an admin the first time it registers,
		// whatever the setting of automatic approval says: the admin checks
		// its peer ID with the person who runs it. Register keeps the
		// approval of a node that registers again from the same peer.
		approve := autoApprove && !viaP2P
		if err := registry.Register(ctx, node, approve); err != nil {
			if errors.Is(err, nodes.ErrNodePinned) {
				xlog.Warn("Refusing a registration under the name of a node that belongs to another worker", "name", req.Name, "p2p", viaP2P)
				return c.JSON(http.StatusConflict, nodeError(http.StatusConflict,
					"this node name belongs to another worker: choose another name with LOCALAI_NODE_NAME"))
			}
			xlog.Error("Failed to register node", "name", req.Name, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to register node"))
		}
		if tunnelOnly {
			// Register keeps a column when the new value is empty, so a node that
			// had addresses from an earlier registration still has them.
			if err := registry.ClearAddresses(ctx, node.ID); err != nil {
				xlog.Warn("Failed to clear the addresses of a tunnel-only node", "node", node.Name, "error", err)
			}
		}

		// Merge worker-supplied labels into the node's existing label set,
		// then apply auto-labels. Using SetNodeLabels here would have
		// delete-then-recreate semantics — every worker re-register would
		// wipe any UI-added label (since PR #9583 the worker always sends
		// the auto-mirror `node.replica-slots`, which made the latent bug
		// from PR #9186 trigger universally instead of only for workers
		// with `--node-labels` set).
		for k, v := range req.Labels {
			if err := registry.SetNodeLabel(ctx, node.ID, k, v); err != nil {
				xlog.Warn("Failed to set node label", "node", node.ID, "key", k, "error", err)
			}
		}
		registry.ApplyAutoLabels(ctx, node.ID, node)

		response := map[string]any{
			"id":         node.ID,
			"name":       node.Name,
			"node_type":  node.NodeType,
			"status":     node.Status,
			"created_at": node.CreatedAt,
		}

		// An earlier release gave every agent worker a user and an API key with
		// the collections grant. The worker no longer calls the HTTP API, so
		// remove what is left of that account the next time the node registers.
		if nodeType == nodes.NodeTypeAgent {
			revokeLegacyAgentWorkerAccount(ctx, authDB, registry, node)
		}

		// A credential is minted for each route asked for, and for no other. A
		// tunnel token replaces the previous one and ends the session that the
		// replica holds for the node, so a registration that asks only for NATS,
		// such as the refresh of the NATS credential, leaves the tunnel alone.
		if wantsTunnel {
			attachTunnelToken(ctx, response, registry, node, opts.tunnels)
		}
		if wantsNATS {
			attachNatsJWT(response, node, natsCfg)
			attachNATSHandover(ctx, response, opts.nats)
		}
		if wantsTunnel && expectedToken == "" && autoApprove {
			openRegistrationWarning.Do(func() {
				xlog.Warn("Worker registration is open: there is no registration token and new nodes are approved at once, " +
					"so anyone who can reach this frontend can register a node, get a tunnel to it, and receive the requests that are sent to workers. " +
					"Set a registration token or turn off automatic approval.")
			})
		}

		return c.JSON(http.StatusCreated, response)
	}
}

// ApproveNodeEndpoint approves a pending node, setting its status to healthy.
func ApproveNodeEndpoint(registry *nodes.NodeRegistry, _ *gorm.DB, _ string, natsCfg natsauth.Config, _ ...RegisterOption) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		if err := registry.ApproveNode(ctx, id); err != nil {
			xlog.Error("Failed to approve node", "id", id, "error", err)
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "failed to approve node"))
		}
		node, err := registry.Get(ctx, id)
		if err != nil {
			return c.JSON(http.StatusOK, map[string]string{"message": "node approved"})
		}

		response := map[string]any{
			"id":        node.ID,
			"name":      node.Name,
			"node_type": node.NodeType,
			"status":    node.Status,
			"message":   "node approved",
		}

		// A node that waited for approval already holds its tunnel credential,
		// which was minted when it registered and is inert until now. The NATS
		// credential needs an approved node, so it is minted here; the worker
		// gets its own at its next registration for the NATS route. A worker
		// over P2P never uses NATS.
		if node.PeerID == "" {
			attachNatsJWT(response, node, natsCfg)
		}

		return c.JSON(http.StatusOK, response)
	}
}

// attachNATSHandover adds the address of the NATS server and what is needed to
// verify it. A failure is logged and the answer goes out without it: the worker
// then uses a URL of its own, or says that it cannot attach.
func attachNATSHandover(ctx context.Context, response map[string]any, h NATSHandover) {
	if h == nil {
		return
	}
	url, err := h.WorkerURL(ctx)
	if err != nil {
		xlog.Warn("Reading the NATS address for a registration failed", "error", err)
		return
	}
	if url == "" {
		return
	}
	response["nats_url"] = url
	ca, err := h.CAPEM()
	if err != nil {
		xlog.Warn("Reading the NATS CA for a registration failed", "error", err)
	} else if ca != "" {
		response["nats_ca"] = ca
	}
	if h.ClientTLS() {
		response["nats_client_tls"] = true
	}
}

// attachTunnelToken mints a new tunnel credential for the node, stores only its
// hash and puts the secret in the response.
//
// Every registration mints a new one, so a second process that registers under
// the same node name makes the next dial of the first process fail. The worker
// reads the token each time it dials.
//
// A node that waits for approval gets one too. That differs from the API key of
// an agent worker, which is not given before approval because it works at once.
// The tunnel credential does not: the connect endpoint reads the status of the
// node at every dial and refuses a pending node with 403. A worker that
// registers once, and gets no credential until it is approved, could not
// connect when the approval came, because an approval does not make it register
// again.
//
// Only a backend or agent node holds a credential. For any other type the
// column is cleared and not left alone. The connect endpoint does not look at
// the type of the node. It refuses a node with an empty hash, so a cleared
// column is what keeps such a node out. Register skips an empty value, so a
// node that registered again as another type would keep a credential that its
// type is not entitled to.
//
// A failure to mint or to store is logged and the answer goes out without the
// token. Registration is how a worker joins the cluster, and a tunnel problem
// must not become a node that cannot join. The worker sees no tunnel_token,
// says so, and tries at its next registration.
func attachTunnelToken(ctx context.Context, response map[string]any, registry *nodes.NodeRegistry, node *nodes.BackendNode, tunnels TunnelDisconnector) {
	if node == nil {
		return
	}
	if !tunnelEligible(node.NodeType) {
		if err := registry.SetTunnelTokenHash(ctx, node.ID, ""); err != nil {
			xlog.Error("Failed to clear the tunnel credential of a node whose type holds none",
				"node", node.Name, "type", node.NodeType, "error", err)
			return
		}
		if tunnels != nil {
			tunnels.Disconnect(node.ID)
		}
		return
	}
	// crypto/rand.Text has at least 128 bits of randomness and no error.
	secret := rand.Text()
	sum := sha256.Sum256([]byte(secret))
	if err := registry.SetTunnelTokenHash(ctx, node.ID, hex.EncodeToString(sum[:])); err != nil {
		xlog.Error("Failed to store a tunnel credential for node", "node", node.Name, "error", err)
		return
	}
	response["tunnel_token"] = secret
	// The old credential is gone from the table. A session that was opened with
	// it is ended, so that the old holder has to present the new one.
	if tunnels != nil && tunnels.Disconnect(node.ID) {
		xlog.Info("Ended the tunnel session of a node whose credential was replaced", "node", node.Name)
	}
}

// tunnelEligible reports whether a node of this type holds a tunnel credential.
// The minting and the clearing both use it, so they cannot disagree: if only one
// were widened, a type would get a credential and never lose it, or lose it at
// every registration.
func tunnelEligible(nodeType string) bool {
	return nodeType == nodes.NodeTypeBackend || nodeType == nodes.NodeTypeAgent
}

// attachNatsJWT adds a per-node NATS user JWT to a register/approve response when minting is enabled.
func attachNatsJWT(response map[string]any, node *nodes.BackendNode, natsCfg natsauth.Config) {
	if !natsCfg.CanMintWorkers() || node == nil || node.Status == nodes.StatusPending {
		return
	}
	jwt, seed, err := natsCfg.MintWorkerJWT(node.ID, node.NodeType)
	if err != nil {
		xlog.Warn("Failed to mint NATS JWT for node", "node", node.Name, "id", node.ID, "error", err)
		return
	}
	response["nats_jwt"] = jwt
	response["nats_user_seed"] = seed
}

// revokeLegacyAgentWorkerAccount deletes the user and API key an earlier release
// provisioned for an agent worker, and clears the references on the node. It
// does nothing when the node has no such account. Failures are logged: the
// registration itself must not fail because of cleanup.
func revokeLegacyAgentWorkerAccount(ctx context.Context, authDB *gorm.DB, registry *nodes.NodeRegistry, node *nodes.BackendNode) {
	if authDB == nil || node.AuthUserID == "" {
		return
	}
	// Delete only the account that was provisioned for a worker, never a
	// user the reference might point to by mistake.
	var n int64
	if err := authDB.Model(&auth.User{}).Where("id = ? AND provider = ?", node.AuthUserID, auth.ProviderAgentWorker).Count(&n).Error; err != nil {
		xlog.Warn("Failed to look up legacy agent worker account", "node", node.Name, "user", node.AuthUserID, "error", err)
		return
	}
	if n > 0 {
		// The cascade removes the keys and sessions with the user.
		if err := auth.DeleteUserCascade(authDB, node.AuthUserID); err != nil {
			xlog.Warn("Failed to remove legacy agent worker account", "node", node.Name, "user", node.AuthUserID, "error", err)
			return
		}
	}
	if err := registry.UpdateAuthRefs(ctx, node.ID, "", ""); err != nil {
		xlog.Warn("Failed to clear legacy agent worker references", "node", node.Name, "error", err)
		return
	}
	node.AuthUserID = ""
	node.APIKeyID = ""
	xlog.Info("Removed legacy agent worker account", "node", node.Name)
}

// loadCancelService builds the cancel path unload, drain and deregister share
// with the load-cancel endpoint. A sender that cannot stop operations still
// gets the cancel recorded; the worker's watchdog and the stop window then
// bound the work.
func loadCancelService(registry *nodes.NodeRegistry, unloader nodes.NodeCommandSender) *nodes.LoadCancelService {
	svc := &nodes.LoadCancelService{Registry: registry}
	if unloader != nil {
		svc.Stopper = unloader
	}
	return svc
}

// cancelNodeLoads cancels the loads placed on a node before its rows go. It
// logs and carries on: a node being removed must not stay because a cancel
// failed.
func cancelNodeLoads(ctx context.Context, registry *nodes.NodeRegistry, unloader nodes.NodeCommandSender, nodeID string) {
	if err := loadCancelService(registry, unloader).CancelNodeLoads(ctx, nodeID); err != nil {
		xlog.Warn("Failed to cancel the loads placed on a node", "node", nodeID, "error", err)
	}
}

// DeregisterNodeEndpoint removes a backend node permanently (admin use).
func DeregisterNodeEndpoint(registry *nodes.NodeRegistry, unloader nodes.NodeCommandSender, options ...RegisterOption) echo.HandlerFunc {
	opts := newRegisterOptions(options)
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		cancelNodeLoads(ctx, registry, unloader, id)
		if err := registry.Deregister(ctx, id); err != nil {
			xlog.Error("Failed to deregister node", "id", id, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to deregister node"))
		}
		// The row is gone, and so is the right to a tunnel. Without this the
		// session stays up, because the credential is checked at the dial only.
		if opts.tunnels != nil {
			opts.tunnels.Disconnect(id)
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "node deregistered"})
	}
}

// DeactivateNodeEndpoint marks a node as offline without deleting it.
// Used by workers on graceful shutdown to preserve approval status across restarts.
func DeactivateNodeEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		if err := registry.MarkOffline(ctx, id); err != nil {
			xlog.Error("Failed to deactivate node", "id", id, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to deactivate node"))
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "node set to offline"})
	}
}

// HeartbeatEndpoint updates the heartbeat for a node.
//
// A worker reports in it the routes its operator gave it, the ones it holds,
// and why the others are not held. The report is stored, and nodes.RoutesOf
// reads it. A heartbeat never mints a credential: a new tunnel token would end
// the tunnel session of the worker.
func HeartbeatEndpoint(registry *nodes.NodeRegistry, _ ...RegisterOption) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")

		// Parse optional VRAM update from body
		var update nodes.HeartbeatUpdate
		_ = c.Bind(&update) // best-effort — empty body is fine

		var updatePtr *nodes.HeartbeatUpdate
		if update.AvailableVRAM != nil || update.TotalVRAM != nil || update.AvailableRAM != nil ||
			update.AvailableDisk != nil || update.TotalDisk != nil || update.GPUVendor != "" ||
			update.CPUUsagePercent != nil || update.CPULoad1 != nil {
			updatePtr = &update
		}

		ctx := c.Request().Context()
		// A new incarnation means the worker restarted and every load operation of
		// the previous process ended. Failures here must not fail the heartbeat.
		if err := registry.ObserveWorkerIncarnation(ctx, id, update.WorkerIncarnation); err != nil {
			xlog.Warn("Failed to record worker incarnation", "id", id, "error", err)
		}
		if err := registry.Heartbeat(ctx, id, updatePtr); err != nil {
			xlog.Warn("Heartbeat failed for node", "id", id, "error", err)
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		if report, ok := update.RouteReport(); ok {
			if err := registry.SetRouteReport(ctx, id, report); err != nil {
				xlog.Warn("Failed to store the route report of a node", "id", id, "error", err)
			}
		}
		return c.JSON(http.StatusOK, map[string]any{"message": "heartbeat received"})
	}
}

// GetNodeModelsEndpoint returns the models loaded on a node.
func GetNodeModelsEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		models, err := registry.GetNodeModels(ctx, id)
		if err != nil {
			xlog.Error("Failed to get node models", "id", id, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to get node models"))
		}
		return c.JSON(http.StatusOK, models)
	}
}

// ListAllNodeModelsEndpoint returns all loaded models across all healthy nodes.
// @Summary List all loaded models cluster-wide
// @Tags Nodes
// @Success 200 {array} nodes.NodeModel
// @Router /api/nodes/models [get]
func ListAllNodeModelsEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		models, err := registry.ListAllLoadedModels(ctx)
		if err != nil {
			xlog.Error("Failed to list all node models", "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to list node models"))
		}
		return c.JSON(http.StatusOK, models)
	}
}

// DrainNodeEndpoint sets a node to draining status (no new requests).
func DrainNodeEndpoint(registry *nodes.NodeRegistry, unloader nodes.NodeCommandSender) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		cancelNodeLoads(ctx, registry, unloader, id)
		if err := registry.MarkDraining(ctx, id); err != nil {
			if errors.Is(err, nodes.ErrNodeNotFound) {
				return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
			}
			if errors.Is(err, nodes.ErrNodeStatusConflict) {
				return c.JSON(http.StatusConflict, nodeError(http.StatusConflict, "node must be healthy to drain"))
			}
			xlog.Error("Failed to drain node", "id", id, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to drain node"))
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "node set to draining"})
	}
}

// ResumeNodeEndpoint sets a draining node back to healthy status.
func ResumeNodeEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		id := c.Param("id")
		if err := registry.ResumeNode(ctx, id); err != nil {
			if errors.Is(err, nodes.ErrNodeNotFound) {
				return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
			}
			if errors.Is(err, nodes.ErrNodeStatusConflict) {
				return c.JSON(http.StatusConflict, nodeError(http.StatusConflict, "node must be draining to resume"))
			}
			xlog.Error("Failed to resume node", "id", id, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to resume node"))
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "node resumed"})
	}
}

// InstallBackendOnNodeEndpoint triggers backend installation on a worker node.
// Async: enqueues a ManagementOp on the gallery service channel and returns a
// jobID immediately. The gallery service worker goroutine drives the actual
// install via DistributedBackendManager.InstallBackend, which honors the op's
// TargetNodeID to scope the fan-out to one node. The UI polls /api/backends/job/:uid
// for progress, mirroring /api/backends/install/:id.
//
// Backend can be either a gallery ID (resolved against BackendGalleries) or a
// direct URI install (URI + Name + optional Alias) - same shape as the
// standalone /api/backends/install-external path, just scoped to one node.
//
// The legacy unloader argument is retained for signature symmetry with
// DeleteBackendOnNodeEndpoint / ListBackendsOnNodeEndpoint but is no longer
// used here - the async path goes through galleryService.
func InstallBackendOnNodeEndpoint(_ nodes.NodeCommandSender, galleryService *galleryop.GalleryService, opcache *galleryop.OpCache, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		if galleryService == nil {
			return c.JSON(http.StatusServiceUnavailable, nodeError(http.StatusServiceUnavailable, "gallery service not configured"))
		}
		nodeID := c.Param("id")
		var req struct {
			Backend          string `json:"backend"`
			BackendGalleries string `json:"backend_galleries,omitempty"`
			URI              string `json:"uri,omitempty"`
			Name             string `json:"name,omitempty"`
			Alias            string `json:"alias,omitempty"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid request body"))
		}
		if req.Backend == "" && req.URI == "" {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "backend name or uri required"))
		}

		jobUUID, err := uuid.NewUUID()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to generate job id"))
		}
		jobID := jobUUID.String()

		// Cache key: for gallery installs, use the backend slug; for URI
		// installs prefer the provided Name (falling back to URI). All keys
		// are node-scoped so concurrent installs of the same backend on
		// different nodes do not stomp each other in opcache.
		backendKey := req.Backend
		if backendKey == "" {
			backendKey = req.Name
			if backendKey == "" {
				backendKey = req.URI
			}
		}
		cacheKey := galleryop.NodeScopedKey(nodeID, backendKey)
		opcache.SetBackend(cacheKey, jobID)

		// Optional caller-supplied galleries override. Mirrors the standalone
		// install path so an admin can point at a private gallery.
		galleries := appConfig.BackendGalleries
		if req.BackendGalleries != "" {
			var custom []config.Gallery
			if err := json.Unmarshal([]byte(req.BackendGalleries), &custom); err != nil {
				xlog.Warn("Ignoring malformed backend_galleries override; falling back to configured galleries", "error", err, "nodeID", nodeID)
			} else if len(custom) > 0 {
				galleries = custom
			}
		}

		ctx, cancelFunc := context.WithCancel(context.Background())
		op := galleryop.ManagementOp[gallery.GalleryBackend, any]{
			ID:                 jobID,
			GalleryElementName: req.Backend,
			Galleries:          galleries,
			TargetNodeID:       nodeID,
			ExternalURI:        req.URI,
			ExternalName:       req.Name,
			ExternalAlias:      req.Alias,
			Context:            ctx,
			CancelFunc:         cancelFunc,
		}
		galleryService.StoreCancellation(jobID, cancelFunc)
		galleryService.EnqueueBackendOp(op)

		xlog.Info("Node-scoped backend install dispatched", "node", nodeID, "backend", req.Backend, "uri", req.URI, "jobID", jobID)
		return c.JSON(http.StatusAccepted, map[string]string{
			"jobID":     jobID,
			"statusUrl": "/api/backends/job/" + jobID,
			"message":   "backend installation started",
		})
	}
}

// UpgradeBackendOnNodeEndpoint triggers a backend upgrade (force-reinstall)
// on a single worker node. Async like InstallBackendOnNodeEndpoint: enqueues
// a ManagementOp with Upgrade=true and TargetNodeID set, returns 202 + jobID.
// The gallery service routes Upgrade ops to
// DistributedBackendManager.UpgradeBackend, which fires the NATS
// backend.upgrade subject: the worker stops running processes for the
// backend and reinstalls from the gallery even when the artifact already
// exists on disk. Reusing the install path here would no-op: the worker's
// backend.install handler is "ensure installed" and short-circuits on an
// already-present binary (the "backend upgraded but nothing happens" bug).
//
// Only gallery-name upgrades are supported: the distributed upgrade path
// resolves galleries from server config, so unlike install there is no
// URI/name/alias or galleries-override surface.
func UpgradeBackendOnNodeEndpoint(galleryService *galleryop.GalleryService, opcache *galleryop.OpCache, appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		if galleryService == nil {
			return c.JSON(http.StatusServiceUnavailable, nodeError(http.StatusServiceUnavailable, "gallery service not configured"))
		}
		nodeID := c.Param("id")
		var req struct {
			Backend string `json:"backend"`
		}
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid request body"))
		}
		if req.Backend == "" {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "backend name required"))
		}

		jobUUID, err := uuid.NewUUID()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to generate job id"))
		}
		jobID := jobUUID.String()

		// Node-scoped cache key so a concurrent upgrade of the same backend on
		// another node doesn't stomp this job in opcache.
		cacheKey := galleryop.NodeScopedKey(nodeID, req.Backend)
		opcache.SetBackend(cacheKey, jobID)

		ctx, cancelFunc := context.WithCancel(context.Background())
		op := galleryop.ManagementOp[gallery.GalleryBackend, any]{
			ID:                 jobID,
			GalleryElementName: req.Backend,
			Galleries:          appConfig.BackendGalleries,
			TargetNodeID:       nodeID,
			Upgrade:            true,
			Context:            ctx,
			CancelFunc:         cancelFunc,
		}
		galleryService.StoreCancellation(jobID, cancelFunc)
		galleryService.EnqueueBackendOp(op)

		xlog.Info("Node-scoped backend upgrade dispatched", "node", nodeID, "backend", req.Backend, "jobID", jobID)
		return c.JSON(http.StatusAccepted, map[string]string{
			"jobID":     jobID,
			"statusUrl": "/api/backends/job/" + jobID,
			"message":   "backend upgrade started",
		})
	}
}

// DeleteBackendOnNodeEndpoint deletes a backend from a worker node via NATS.
func DeleteBackendOnNodeEndpoint(unloader nodes.NodeCommandSender) echo.HandlerFunc {
	return func(c echo.Context) error {
		if unloader == nil {
			return c.JSON(http.StatusServiceUnavailable, nodeError(http.StatusServiceUnavailable, "NATS not configured"))
		}
		nodeID := c.Param("id")
		var req struct {
			Backend string `json:"backend"`
		}
		if err := c.Bind(&req); err != nil || req.Backend == "" {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "backend name required"))
		}
		reply, err := unloader.DeleteBackend(nodeID, req.Backend)
		if err != nil {
			xlog.Error("Failed to delete backend on node", "node", nodeID, "backend", req.Backend, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to delete backend on node"))
		}
		if !reply.Success {
			xlog.Error("Backend delete failed on node", "node", nodeID, "backend", req.Backend, "error", reply.Error)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "backend deletion failed"))
		}
		return c.JSON(http.StatusOK, map[string]string{"message": "backend deleted"})
	}
}

// ListBackendsOnNodeEndpoint lists installed backends on a worker node via NATS.
func ListBackendsOnNodeEndpoint(unloader nodes.NodeCommandSender, registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		nodeID := c.Param("id")
		// Agent-type workers don't run backends and never subscribe to the
		// nodes.<id>.backend.list NATS subject, so the request would hang
		// until timeout with "no responders". Their backend list is simply
		// empty. Mirror the aggregate-list guard in managers_distributed.go
		// (skip nodes whose NodeType is set and not "backend") so the
		// single-node and cluster-wide views stay consistent.
		if node, err := registry.Get(c.Request().Context(), nodeID); err == nil {
			if node.NodeType != "" && node.NodeType != nodes.NodeTypeBackend {
				return c.JSON(http.StatusOK, []workerctl.NodeBackendInfo{})
			}
		}
		if unloader == nil {
			return c.JSON(http.StatusServiceUnavailable, nodeError(http.StatusServiceUnavailable, "NATS not configured"))
		}
		reply, err := unloader.ListBackends(nodeID)
		if err != nil {
			xlog.Error("Failed to list backends on node", "node", nodeID, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to list backends on node"))
		}
		if reply.Error != "" {
			xlog.Error("List backends failed on node", "node", nodeID, "error", reply.Error)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to list backends on node"))
		}
		return c.JSON(http.StatusOK, reply.Backends)
	}
}

// UnloadModelOnNodeEndpoint unloads a model from a worker node (gRPC Free) via NATS.
func UnloadModelOnNodeEndpoint(unloader nodes.NodeCommandSender, registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		if unloader == nil {
			return c.JSON(http.StatusServiceUnavailable, nodeError(http.StatusServiceUnavailable, "NATS not configured"))
		}
		nodeID := c.Param("id")
		var req struct {
			ModelName string `json:"model_name"`
		}
		if err := c.Bind(&req); err != nil || req.ModelName == "" {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "model_name required"))
		}
		// A load of this model on this node (or one not placed yet) is cancelled
		// through the same stop path first. The unload of a loaded replica then
		// proceeds as usual: it is no longer swallowed by the load.
		if _, err := loadCancelService(registry, unloader).CancelModelOnNode(c.Request().Context(), nodeID, req.ModelName); err != nil {
			xlog.Warn("Failed to cancel the load before unloading", "node", nodeID, "model", req.ModelName, "error", err)
		}
		if err := unloader.UnloadModelOnNode(nodeID, req.ModelName); err != nil {
			xlog.Error("Failed to unload model on node", "node", nodeID, "model", req.ModelName, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to unload model on node"))
		}
		// Also stop the backend process
		if err := unloader.StopBackend(nodeID, req.ModelName); err != nil {
			xlog.Error("Failed to stop backend after model unload", "node", nodeID, "model", req.ModelName, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "model unloaded but backend stop failed"))
		}
		// Remove every replica of this model on the node from the registry.
		registry.RemoveAllNodeModelReplicas(c.Request().Context(), nodeID, req.ModelName)
		return c.JSON(http.StatusOK, map[string]string{"message": "model unloaded"})
	}
}

// DeleteModelOnNodeEndpoint deletes model files from a worker node via NATS.
func DeleteModelOnNodeEndpoint(unloader nodes.NodeCommandSender, registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		if unloader == nil {
			return c.JSON(http.StatusServiceUnavailable, nodeError(http.StatusServiceUnavailable, "NATS not configured"))
		}
		nodeID := c.Param("id")
		var req struct {
			ModelName string `json:"model_name"`
		}
		if err := c.Bind(&req); err != nil || req.ModelName == "" {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "model_name required"))
		}
		// Unload model first if loaded
		if err := unloader.UnloadModelOnNode(nodeID, req.ModelName); err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to unload model before deletion"))
		}
		if err := unloader.StopBackend(nodeID, req.ModelName); err != nil {
			// Non-fatal — backend process may not be running
			xlog.Warn("StopBackend failed during model deletion (non-fatal)", "node", nodeID, "model", req.ModelName, "error", err)
		}
		registry.RemoveAllNodeModelReplicas(c.Request().Context(), nodeID, req.ModelName)
		return c.JSON(http.StatusOK, map[string]string{"message": "model deleted from node"})
	}
}

// NodeBackendLogsListEndpoint proxies a request to a worker node's /v1/backend-logs
// endpoint to list model IDs that have backend logs.
func NodeBackendLogsListEndpoint(registry *nodes.NodeRegistry, registrationToken string, dialFor nodes.WorkerNetDialerFor) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		node, err := registry.Get(ctx, nodeID)
		if err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}

		host, ok := workerLogsHost(node)
		if !ok {
			return c.JSON(http.StatusBadGateway, nodeError(http.StatusBadGateway, "node has no HTTP address"))
		}

		resp, err := proxyHTTPToWorker(ctx, dialFor, node.ID, host, "/v1/backend-logs", registrationToken)
		if err != nil {
			return c.JSON(http.StatusBadGateway, nodeError(http.StatusBadGateway, fmt.Sprintf("failed to reach worker: %v", err)))
		}
		defer resp.Body.Close()

		c.Response().Header().Set("Content-Type", "application/json")
		c.Response().WriteHeader(resp.StatusCode)
		io.Copy(c.Response(), resp.Body)
		return nil
	}
}

// NodeBackendLogsLinesEndpoint proxies a request to a worker node's
// /v1/backend-logs/{modelId} endpoint to get buffered log lines.
func NodeBackendLogsLinesEndpoint(registry *nodes.NodeRegistry, registrationToken string, dialFor nodes.WorkerNetDialerFor) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		modelID := c.Param("modelId")

		node, err := registry.Get(ctx, nodeID)
		if err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}

		host, ok := workerLogsHost(node)
		if !ok {
			return c.JSON(http.StatusBadGateway, nodeError(http.StatusBadGateway, "node has no HTTP address"))
		}

		path := "/v1/backend-logs/" + url.PathEscape(modelID)
		resp, err := proxyHTTPToWorker(ctx, dialFor, node.ID, host, path, registrationToken)
		if err != nil {
			return c.JSON(http.StatusBadGateway, nodeError(http.StatusBadGateway, fmt.Sprintf("failed to reach worker: %v", err)))
		}
		defer resp.Body.Close()

		c.Response().Header().Set("Content-Type", "application/json")
		c.Response().WriteHeader(resp.StatusCode)
		io.Copy(c.Response(), resp.Body)
		return nil
	}
}

// NodeBackendLogsWSEndpoint proxies a WebSocket connection to a worker node's
// /v1/backend-logs/{modelId}/ws endpoint for real-time log streaming.
func NodeBackendLogsWSEndpoint(registry *nodes.NodeRegistry, registrationToken string, dialFor nodes.WorkerNetDialerFor) echo.HandlerFunc {
	browserUpgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if origin == "" {
				return true // no origin header = same-origin or non-browser
			}
			// Parse origin URL and compare host with request host
			u, err := url.Parse(origin)
			if err != nil {
				return false
			}
			return u.Host == r.Host
		},
	}

	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		modelID := c.Param("modelId")

		node, err := registry.Get(ctx, nodeID)
		if err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}

		// Upgrade browser connection
		browserWS, err := browserUpgrader.Upgrade(c.Response(), c.Request(), nil)
		if err != nil {
			return err
		}

		// Dial the worker WebSocket
		workerURL := fmt.Sprintf("ws://%s/v1/backend-logs/%s/ws", nodes.WorkerHTTPHost(node.ID, node.HTTPAddress), url.PathEscape(modelID))
		workerHeaders := http.Header{}
		if registrationToken != "" {
			workerHeaders.Set("Authorization", "Bearer "+registrationToken)
		}

		workerDialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second, NetDialContext: dialFor(node.ID)}
		workerWS, _, err := workerDialer.Dial(workerURL, workerHeaders)
		if err != nil {
			browserWS.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "failed to connect to worker"))
			browserWS.Close()
			return nil
		}

		// Use sync.OnceFunc wrappers to avoid double-close and ensure each
		// goroutine can safely close the *other* connection to unblock
		// its peer's ReadMessage call.
		done := make(chan struct{})
		closeWorker := sync.OnceFunc(func() { workerWS.Close() })
		closeBrowser := sync.OnceFunc(func() { browserWS.Close() })

		// Worker → Browser
		go func() {
			defer close(done)
			defer closeBrowser() // unblock Browser→Worker goroutine
			for {
				msgType, msg, err := workerWS.ReadMessage()
				if err != nil {
					return
				}
				if err := browserWS.WriteMessage(msgType, msg); err != nil {
					return
				}
			}
		}()

		// Browser → Worker (mainly for close detection)
		go func() {
			defer closeWorker() // unblock Worker→Browser goroutine
			for {
				msgType, msg, err := browserWS.ReadMessage()
				if err != nil {
					return
				}
				if err := workerWS.WriteMessage(msgType, msg); err != nil {
					return
				}
			}
		}()

		<-done
		closeWorker()
		closeBrowser()
		return nil
	}
}

// GetNodeLabelsEndpoint returns labels for a specific node.
func GetNodeLabelsEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		labels, err := registry.GetNodeLabels(ctx, nodeID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to get labels"))
		}
		// Convert to map for cleaner API response
		result := make(map[string]string)
		for _, l := range labels {
			result[l.Key] = l.Value
		}
		return c.JSON(http.StatusOK, result)
	}
}

// UpdateMaxReplicasPerModelRequest is the body for the per-node replica cap endpoint.
type UpdateMaxReplicasPerModelRequest struct {
	// Value is the new per-model replica cap on this node. Must be >= 1.
	Value int `json:"value"`
}

// UpdateMaxReplicasPerModelEndpoint sets the per-node cap on how many replicas
// of any one model can be loaded concurrently. The corresponding
// `node.replica-slots` auto-label is refreshed so existing AND-selectors keep
// matching, and any unsatisfiable scheduling cooldowns are cleared so the
// reconciler retries on the next tick.
//
// This is a transient admin override — a worker re-registration restores the
// value the worker was started with (--max-replicas-per-model). For permanent
// fleet changes, change the worker flag.
//
// @Summary Update a node's max replicas per model
// @Tags Nodes
// @Param id path string true "Node ID"
// @Param request body UpdateMaxReplicasPerModelRequest true "New value"
// @Success 200 {object} map[string]int
// @Failure 400 {object} map[string]any "value must be >= 1"
// @Failure 404 {object} map[string]any "node not found"
// @Router /api/nodes/{id}/max-replicas-per-model [put]
func UpdateMaxReplicasPerModelEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		if _, err := registry.Get(ctx, nodeID); err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		var req UpdateMaxReplicasPerModelRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid request body"))
		}
		if req.Value < 1 {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "value must be >= 1"))
		}
		if err := registry.UpdateMaxReplicasPerModel(ctx, nodeID, req.Value); err != nil {
			xlog.Error("Failed to update max_replicas_per_model", "node", nodeID, "value", req.Value, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to update max replicas per model"))
		}
		return c.JSON(http.StatusOK, map[string]int{"max_replicas_per_model": req.Value})
	}
}

// ResetMaxReplicasPerModelEndpoint clears the admin override on a node, so
// the next worker re-registration is allowed to update the value from its
// CLI flag again. The current value is left in place until the worker calls
// register.
//
// @Summary Reset a node's max replicas per model to the worker default
// @Tags Nodes
// @Param id path string true "Node ID"
// @Success 200 {object} map[string]bool
// @Failure 404 {object} map[string]any "node not found"
// @Router /api/nodes/{id}/max-replicas-per-model [delete]
func ResetMaxReplicasPerModelEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		if _, err := registry.Get(ctx, nodeID); err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		if err := registry.ResetMaxReplicasPerModel(ctx, nodeID); err != nil {
			xlog.Error("Failed to reset max_replicas_per_model override", "node", nodeID, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to reset override"))
		}
		return c.JSON(http.StatusOK, map[string]bool{"reset": true})
	}
}

// UpdateVRAMBudgetRequest is the body for the per-node VRAM budget endpoint.
type UpdateVRAMBudgetRequest struct {
	// Value is the VRAM cap ("80%" or "12GB"). Empty string clears the cap.
	Value string `json:"value"`
}

// UpdateVRAMBudgetEndpoint sets a node's VRAM allocation cap as a sticky admin
// override. The value is validated, resolved against the node's total VRAM, and
// applied to available_vram immediately so scheduling reflects it before the
// next heartbeat.
//
// @Summary Update a node's VRAM allocation budget
// @Tags Nodes
// @Param id path string true "Node ID"
// @Param request body UpdateVRAMBudgetRequest true "New value (\"80%\" or \"12GB\"; empty clears)"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]any "invalid budget"
// @Failure 404 {object} map[string]any "node not found"
// @Router /api/nodes/{id}/vram-budget [put]
func UpdateVRAMBudgetEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		if _, err := registry.Get(ctx, nodeID); err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		var req UpdateVRAMBudgetRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid request body"))
		}
		// An empty value clears the cap; only a non-empty value must parse.
		if req.Value != "" {
			if _, err := vrambudget.Parse(req.Value); err != nil {
				return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, fmt.Sprintf("invalid vram budget: %v", err)))
			}
		}
		if err := registry.UpdateVRAMBudget(ctx, nodeID, req.Value); err != nil {
			xlog.Error("Failed to update vram_budget", "node", nodeID, "value", req.Value, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to update vram budget"))
		}
		return c.JSON(http.StatusOK, map[string]string{"vram_budget": req.Value})
	}
}

// ResetVRAMBudgetEndpoint clears the admin override so the worker's
// LOCALAI_VRAM_BUDGET takes over again on next registration.
//
// @Summary Reset a node's VRAM budget to the worker default
// @Tags Nodes
// @Param id path string true "Node ID"
// @Success 200 {object} map[string]bool
// @Failure 404 {object} map[string]any "node not found"
// @Router /api/nodes/{id}/vram-budget [delete]
func ResetVRAMBudgetEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		if _, err := registry.Get(ctx, nodeID); err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		if err := registry.ResetVRAMBudget(ctx, nodeID); err != nil {
			xlog.Error("Failed to reset vram_budget override", "node", nodeID, "error", err)
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to reset vram budget"))
		}
		return c.JSON(http.StatusOK, map[string]bool{"reset": true})
	}
}

// SetNodeLabelsEndpoint replaces all labels for a node.
func SetNodeLabelsEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		if _, err := registry.Get(ctx, nodeID); err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		var labels map[string]string
		if err := c.Bind(&labels); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid request body"))
		}
		if err := registry.SetNodeLabels(ctx, nodeID, labels); err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to set labels"))
		}
		return c.JSON(http.StatusOK, labels)
	}
}

// MergeNodeLabelsEndpoint adds/updates labels without removing existing ones.
func MergeNodeLabelsEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		if _, err := registry.Get(ctx, nodeID); err != nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "node not found"))
		}
		var labels map[string]string
		if err := c.Bind(&labels); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid request body"))
		}
		for k, v := range labels {
			if err := registry.SetNodeLabel(ctx, nodeID, k, v); err != nil {
				return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to merge labels"))
			}
		}
		// Return updated labels
		updated, _ := registry.GetNodeLabels(ctx, nodeID)
		result := make(map[string]string)
		for _, l := range updated {
			result[l.Key] = l.Value
		}
		return c.JSON(http.StatusOK, result)
	}
}

// DeleteNodeLabelEndpoint removes a single label from a node.
func DeleteNodeLabelEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		nodeID := c.Param("id")
		key := c.Param("key")
		if key == "" {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "label key is required"))
		}
		if err := registry.RemoveNodeLabel(ctx, nodeID, key); err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to remove label"))
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// ListSchedulingEndpoint returns all model scheduling configs.
func ListSchedulingEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		configs, err := registry.ListModelSchedulings(ctx)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to list scheduling configs"))
		}
		return c.JSON(http.StatusOK, configs)
	}
}

// GetSchedulingEndpoint returns the scheduling config for a specific model.
func GetSchedulingEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		modelName := c.Param("model")
		config, err := registry.GetModelScheduling(ctx, modelName)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to get scheduling config"))
		}
		if config == nil {
			return c.JSON(http.StatusNotFound, nodeError(http.StatusNotFound, "no scheduling config for model"))
		}
		return c.JSON(http.StatusOK, config)
	}
}

// SetSchedulingRequest is the request body for creating/updating a scheduling config.
//
// The prefix-cache fields are POINTERS so an omitted field is
// distinguishable from an explicit zero. On update, an omitted prefix-cache
// field preserves the model's previously-configured value instead of resetting
// it (see SetSchedulingEndpoint's PATCH-style merge). ModelName, NodeSelector,
// MinReplicas, MaxReplicas and SpreadAll keep their full-replace PUT semantics.
type SetSchedulingRequest struct {
	ModelName           string              `json:"model_name"`
	NodeSelector        map[string]string   `json:"node_selector,omitempty"`
	MinReplicas         int                 `json:"min_replicas"`
	MaxReplicas         int                 `json:"max_replicas"`
	SpreadAll           bool                `json:"spread_all,omitempty"`
	RoutePolicy         *string             `json:"route_policy,omitempty"`
	BalanceAbsThreshold *int                `json:"balance_abs_threshold,omitempty"`
	BalanceRelThreshold *float64            `json:"balance_rel_threshold,omitempty"`
	MinPrefixMatch      *float64            `json:"min_prefix_match,omitempty"`
	ScorerWeights       *map[string]float64 `json:"scorer_weights,omitempty"`
	// UpgradeStrategy is how an upgrade replaces the running replicas:
	// "stop_and_start" (the default) or "canary". Canary is stored but not
	// available yet, so an upgrade still stops and starts. Omitted on an
	// update, the stored value is kept.
	UpgradeStrategy *string `json:"upgrade_strategy,omitempty"`
}

// validateSchedulingRequest enforces the invariants of a scheduling config.
// The prefix-cache bounds are delegated to prefixcache.ValidateThresholds (the
// single source of truth), and are checked against the RESOLVED values passed
// in (provided-or-preserved), so validation only rejects bad values the caller
// actually supplied. It returns nil when valid, or an error with a user-facing
// message describing the first violation.
func validateSchedulingRequest(req SetSchedulingRequest, routePolicy string, absThr int, relThr, minMatch float64) error {
	if req.ModelName == "" {
		return errors.New("model_name is required")
	}
	if req.SpreadAll && (req.MinReplicas != 0 || req.MaxReplicas != 0) {
		return errors.New("spread_all and min_replicas/max_replicas are mutually exclusive")
	}
	if req.MinReplicas < 0 {
		return errors.New("min_replicas must be >= 0")
	}
	if req.MaxReplicas < 0 {
		return errors.New("max_replicas must be >= 0")
	}
	if req.MaxReplicas > 0 && req.MinReplicas > req.MaxReplicas {
		return errors.New("min_replicas must be <= max_replicas")
	}
	if err := prefixcache.ValidateThresholds(routePolicy, absThr, relThr, minMatch); err != nil {
		return err
	}
	if req.ScorerWeights != nil {
		if err := prefixcache.ValidateScorerWeights(*req.ScorerWeights); err != nil {
			return err
		}
	}
	if req.UpgradeStrategy != nil && !nodes.ValidUpgradeStrategy(*req.UpgradeStrategy) {
		return fmt.Errorf("upgrade_strategy must be %q or %q", nodes.UpgradeStrategyStopAndStart, nodes.UpgradeStrategyCanary)
	}
	return nil
}

// SetSchedulingEndpoint creates or updates a model scheduling config.
//
// The registry upsert full-replaces all columns, so a request that omits the
// prefix-cache fields would otherwise wipe a model's previously-configured
// routing settings. To avoid that footgun the prefix-cache fields are
// merged PATCH-style: a non-nil request pointer wins; a nil one preserves the
// existing config's value (or the zero default when no config exists yet). The
// non-prefix fields keep their full-replace PUT behavior.
func SetSchedulingEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		var req SetSchedulingRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid request body"))
		}

		// Fetch the existing config (may be nil) so omitted prefix-cache fields
		// can fall back to the stored value rather than resetting to zero.
		var existing *nodes.ModelSchedulingConfig
		if req.ModelName != "" {
			var err error
			existing, err = registry.GetModelScheduling(ctx, req.ModelName)
			if err != nil {
				return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to load existing scheduling config"))
			}
		}

		// Resolve each prefix-cache field: provided pointer wins, otherwise keep
		// the existing value (zero/default when there is no existing config).
		routePolicy := ""
		absThr := 0
		relThr := 0.0
		minMatch := 0.0
		var scorerWeights map[string]float64
		upgradeStrategy := ""
		if existing != nil {
			routePolicy = existing.RoutePolicy
			absThr = existing.BalanceAbsThreshold
			relThr = existing.BalanceRelThreshold
			minMatch = existing.MinPrefixMatch
			scorerWeights = existing.ScorerWeights
			upgradeStrategy = existing.UpgradeStrategy
		}
		if req.UpgradeStrategy != nil {
			upgradeStrategy = *req.UpgradeStrategy
		}
		if req.RoutePolicy != nil {
			routePolicy = *req.RoutePolicy
		}
		if req.BalanceAbsThreshold != nil {
			absThr = *req.BalanceAbsThreshold
		}
		if req.BalanceRelThreshold != nil {
			relThr = *req.BalanceRelThreshold
		}
		if req.MinPrefixMatch != nil {
			minMatch = *req.MinPrefixMatch
		}
		if req.ScorerWeights != nil {
			scorerWeights = *req.ScorerWeights
		}

		if err := validateSchedulingRequest(req, routePolicy, absThr, relThr, minMatch); err != nil {
			return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, err.Error()))
		}

		// A rule may be keyed by an alias, in which case it governs whatever
		// that alias currently points at. Reject an alias that resolves to
		// nothing, and reject a second rule for a model some other rule already
		// governs, so the operator hears about the clash instead of silently
		// writing a rule that never takes effect.
		target, err := registry.ValidateSchedulingTarget(ctx, req.ModelName)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, nodes.ErrSchedulingConflict) {
				status = http.StatusConflict
			}
			return c.JSON(status, nodeError(status, err.Error()))
		}

		// Serialize node selector to JSON
		var selectorJSON string
		if len(req.NodeSelector) > 0 {
			b, err := json.Marshal(req.NodeSelector)
			if err != nil {
				return c.JSON(http.StatusBadRequest, nodeError(http.StatusBadRequest, "invalid node_selector"))
			}
			selectorJSON = string(b)
		}

		config := &nodes.ModelSchedulingConfig{
			ModelName:           req.ModelName,
			TargetModel:         target,
			NodeSelector:        selectorJSON,
			MinReplicas:         req.MinReplicas,
			MaxReplicas:         req.MaxReplicas,
			SpreadAll:           req.SpreadAll,
			RoutePolicy:         routePolicy,
			BalanceAbsThreshold: absThr,
			BalanceRelThreshold: relThr,
			MinPrefixMatch:      minMatch,
			ScorerWeights:       scorerWeights,
			UpgradeStrategy:     upgradeStrategy,
		}
		if err := registry.SetModelScheduling(ctx, config); err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to set scheduling config"))
		}
		return c.JSON(http.StatusOK, config)
	}
}

// DeleteSchedulingEndpoint removes a model scheduling config.
func DeleteSchedulingEndpoint(registry *nodes.NodeRegistry) echo.HandlerFunc {
	return func(c echo.Context) error {
		ctx := c.Request().Context()
		modelName := c.Param("model")
		if err := registry.DeleteModelScheduling(ctx, modelName); err != nil {
			return c.JSON(http.StatusInternalServerError, nodeError(http.StatusInternalServerError, "failed to delete scheduling config"))
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// workerLogsHost returns the host of the URL of a request to the HTTP server of a
// worker, and false when the node has no HTTP server to ask.
//
// A worker that holds a tunnel registers no address at all: nothing dials it, and
// the dialer opens a stream on its tunnel whatever host the URL names. Its host
// is the reserved name of nodes.WorkerHTTPHost. A worker that registered a
// gRPC address and no HTTP address is an older worker with no log endpoint, and
// the answer for it has not changed.
func workerLogsHost(node *nodes.BackendNode) (string, bool) {
	if node.HTTPAddress == "" && node.Address != "" {
		return "", false
	}
	return nodes.WorkerHTTPHost(node.ID, node.HTTPAddress), true
}

// workerTransport returns the transport that reaches the HTTP server of a worker
// through the dialer of the node.
func workerTransport(dialFor nodes.WorkerNetDialerFor, nodeID, host string) *http.Transport {
	t := httpclient.HardenedTransport()
	t.DialContext = dialFor(nodeID)
	// A worker without an address is reached through a tunnel, and a proxy of the
	// environment cannot carry that: the transport would dial the proxy through
	// the dialer of the worker. The proxy stays for every other worker.
	if nodes.IsTunnelOnlyHost(host) {
		t.Proxy = nil
	}
	return t
}

// proxyHTTPToWorker makes a GET request to a worker's HTTP server with bearer token auth.
// The connection goes through dialFor(nodeID) because the advertised address
// alone does not say how this frontend reaches that worker.
func proxyHTTPToWorker(ctx context.Context, dialFor nodes.WorkerNetDialerFor, nodeID, httpAddress, path, token string) (*http.Response, error) {
	reqURL := fmt.Sprintf("http://%s%s", httpAddress, path)
	// WithoutCancel keeps the request bounded only by the 15s client timeout,
	// as before the dialer change; cancelling on admin disconnect would be a
	// separate, deliberate behaviour change.
	req, err := http.NewRequestWithContext(context.WithoutCancel(ctx), "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	t := workerTransport(dialFor, nodeID, httpAddress)
	client := httpclient.NewWithTimeout(15*time.Second, httpclient.WithTransport(t))
	return client.Do(req)
}
