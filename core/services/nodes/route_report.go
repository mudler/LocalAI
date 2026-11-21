package nodes

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

// RouteReport is what a worker says about its routes in a heartbeat: the
// routes it holds and is connected on (Attached), the routes its operator gave
// it (Follow), and why some of them are not held (FollowError). The route names
// are RouteNATS and RouteTunnelID.
type RouteReport struct {
	Attached    []string
	Follow      []string
	FollowError string
	// Tunnel is how the tunnel of the worker travels. It is zero when the
	// worker holds none or does not say.
	Tunnel TunnelRoute
}

// The transports of a worker tunnel.
const (
	TunnelWebSocket = "websocket"
	TunnelP2P       = "p2p"
)

// TunnelRoute is how the tunnel of a worker travels: over a websocket, or over
// P2P with the path that the P2P layer found.
type TunnelRoute struct {
	Transport string
	// Path is direct, hole_punched or relayed, for P2P only.
	Path string
	// Limited says that the relay of a relayed path limits its data or time.
	Limited bool
}

// parseTunnelRoute keeps the values that it knows, so that a column never
// holds what a worker made up.
func parseTunnelRoute(transport, path string, limited bool) TunnelRoute {
	switch transport {
	case TunnelWebSocket:
		return TunnelRoute{Transport: transport}
	case TunnelP2P:
		r := TunnelRoute{Transport: transport}
		switch path {
		case "direct", "hole_punched":
			r.Path = path
		case "relayed":
			r.Path, r.Limited = path, limited
		}
		return r
	}
	return TunnelRoute{}
}

// followErrorMax is the size of the column that holds FollowError.
const followErrorMax = 255

// knownRoute says whether name is a route that a worker can hold.
func knownRoute(name string) bool { return name == RouteNATS || name == RouteTunnelID }

// parseRoutes keeps the names of the routes that the cluster knows, once each.
func parseRoutes(names []string) []string {
	var out []string
	for _, n := range names {
		r := strings.TrimSpace(n)
		if knownRoute(r) && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

// splitRoutes reads a column that holds route names separated by commas.
func splitRoutes(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for part := range strings.SplitSeq(s, ",") {
		if r := strings.TrimSpace(part); knownRoute(r) {
			out = append(out, r)
		}
	}
	return out
}

// SetRouteReport stores what a worker reported about its routes.
func (r *NodeRegistry) SetRouteReport(ctx context.Context, nodeID string, rep RouteReport) error {
	res := r.db.WithContext(ctx).Model(&BackendNode{}).Where("id = ?", nodeID).Updates(map[string]any{
		"attached":         strings.Join(rep.Attached, ","),
		"follow":           strings.Join(rep.Follow, ","),
		"follow_error":     rep.FollowError,
		"tunnel_transport": rep.Tunnel.Transport,
		"tunnel_path":      rep.Tunnel.Path,
		"tunnel_limited":   rep.Tunnel.Limited,
	})
	if res.Error != nil {
		return fmt.Errorf("storing the route report of node %s: %w", nodeID, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("storing the route report of node %s: %w", nodeID, gorm.ErrRecordNotFound)
	}
	return nil
}

// errTunnelWorkerLoopback is the failure of the dial that the guarded factory
// puts in the place of a dial that cannot mean anything. It is not the answer of
// a host about a backend, so nothing that decides whether a backend is dead may
// act on it.
var errTunnelWorkerLoopback = errors.New("the backend of this worker listens on its own loopback address and the worker holds no address that can be dialled, so it cannot be reached on the direct route")

// guardTTL is how long the guarded factory keeps its answer about a worker.
const guardTTL = 10 * time.Second

type guardedDirectFactory struct {
	token      string
	direct     BackendClientFactory
	hasAddress func(nodeID string) (bool, error)

	mu    sync.Mutex
	known map[string]guardEntry
}

type guardEntry struct {
	at  time.Time
	has bool
}

// NewGuardedDirectClientFactory is the direct client factory with one rule. A
// worker that holds a tunnel has no address and binds its backends to loopback.
// Dialled directly, a loopback address names a port on the frontend and not on the
// worker, and the refused connection that follows looks like a dead backend. The
// code that decides about a backend would then reap a model that runs. For such a
// worker the client gets a dial that fails in the transport, which every such
// code reads as "nothing learned". A worker that registered an address is dialled
// as before, so a deployment on NATS behaves as it did.
//
// hasAddress says whether the worker registered an address. When it cannot say,
// the client dials.
func NewGuardedDirectClientFactory(token string, hasAddress func(nodeID string) (bool, error)) BackendClientFactory {
	return &guardedDirectFactory{token: token, direct: NewTokenClientFactory(token), hasAddress: hasAddress, known: map[string]guardEntry{}}
}

func (f *guardedDirectFactory) NewClient(nodeID, address string, parallel bool) grpc.Backend {
	if isLoopbackAddress(address) && f.withoutAddress(nodeID) {
		return grpc.NewClientWithDialer(address, parallel, nil, false, f.token,
			func(context.Context, string) (net.Conn, error) { return nil, errTunnelWorkerLoopback })
	}
	return f.direct.NewClient(nodeID, address, parallel)
}

func (f *guardedDirectFactory) withoutAddress(nodeID string) bool {
	f.mu.Lock()
	hit, ok := f.known[nodeID]
	f.mu.Unlock()
	if ok && time.Since(hit.at) < guardTTL {
		return !hit.has
	}
	has, err := f.hasAddress(nodeID)
	if err != nil {
		return false
	}
	f.mu.Lock()
	f.known[nodeID] = guardEntry{at: time.Now(), has: has}
	f.mu.Unlock()
	return !has
}

func isLoopbackAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
