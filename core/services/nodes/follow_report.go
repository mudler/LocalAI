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
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/services/cluster"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
)

// CarrierReport is what a worker says about itself in a heartbeat: the carriers
// it is attached to, the epoch of the cluster row it saw, the carriers it can
// attach to, and why it cannot attach to another one.
type CarrierReport struct {
	Attached      []cluster.Carrier
	AttachedEpoch int64
	Follow        []cluster.Carrier
	FollowError   string
}

// followErrorMax is the size of the column that holds FollowError.
const followErrorMax = 255

// parseCarriers keeps the names of the carriers that the cluster knows, once each.
func parseCarriers(names []string) []cluster.Carrier {
	var out []cluster.Carrier
	for _, n := range names {
		c := cluster.Carrier(strings.TrimSpace(n))
		if (c == cluster.CarrierNATS || c == cluster.CarrierTunnel) && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// truncateRunes cuts s to at most n bytes without splitting a character.
func truncateRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func joinCarriers(cs []cluster.Carrier) string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = string(c)
	}
	return strings.Join(out, ",")
}

func splitCarriers(s string) []cluster.Carrier {
	if s == "" {
		return nil
	}
	var out []cluster.Carrier
	for _, part := range strings.Split(s, ",") {
		if c := cluster.Carrier(strings.TrimSpace(part)); c == cluster.CarrierNATS || c == cluster.CarrierTunnel {
			out = append(out, c)
		}
	}
	return out
}

// SetCarrierReport stores what a worker reported about its carriers.
func (r *NodeRegistry) SetCarrierReport(ctx context.Context, nodeID string, rep CarrierReport) error {
	res := r.db.WithContext(ctx).Model(&BackendNode{}).Where("id = ?", nodeID).Updates(map[string]any{
		"attached":       joinCarriers(rep.Attached),
		"attached_epoch": rep.AttachedEpoch,
		"follow":         joinCarriers(rep.Follow),
		"follow_error":   rep.FollowError,
	})
	if res.Error != nil {
		return fmt.Errorf("storing the carrier report of node %s: %w", nodeID, res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("storing the carrier report of node %s: %w", nodeID, gorm.ErrRecordNotFound)
	}
	return nil
}

// PresenceReader says whether a worker holds a tunnel. *cluster.Registry is one.
type PresenceReader interface {
	Presence(ctx context.Context, nodeID string, grace time.Duration) (cluster.Presence, error)
}

// SwitchWorkers answers the questions that a change of carrier asks about the
// workers: which are alive, which carriers each is attached to, and which it can
// follow. A worker that reports its carriers is believed. One that does not is a
// worker that predates carrier switching: it holds a tunnel, or it is on NATS.
type SwitchWorkers struct {
	reg      *NodeRegistry
	presence PresenceReader
	grace    time.Duration
	stale    time.Duration
}

// NewSwitchWorkers returns the reader. A worker whose heartbeat is older than
// stale is not alive for this purpose; grace is the reconnect grace of the
// tunnel.
func NewSwitchWorkers(reg *NodeRegistry, presence PresenceReader, grace, stale time.Duration) *SwitchWorkers {
	return &SwitchWorkers{reg: reg, presence: presence, grace: grace, stale: stale}
}

var _ cluster.WorkerSource = (*SwitchWorkers)(nil)

func (s *SwitchWorkers) attached(ctx context.Context, n *BackendNode) ([]cluster.Carrier, error) {
	if reported := splitCarriers(n.Attached); len(reported) > 0 {
		return reported, nil
	}
	p, err := s.presence.Presence(ctx, n.ID, s.grace)
	if err != nil {
		return nil, err
	}
	if p == cluster.PresenceConnected || p == cluster.PresenceReconnecting {
		return []cluster.Carrier{cluster.CarrierTunnel}, nil
	}
	return []cluster.Carrier{cluster.CarrierNATS}, nil
}

// Workers lists the workers that are alive.
func (s *SwitchWorkers) Workers(ctx context.Context) ([]cluster.WorkerInfo, error) {
	list, err := s.reg.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []cluster.WorkerInfo
	for i := range list {
		n := &list[i]
		if n.Status == StatusPending || n.Status == StatusOffline || time.Since(n.LastHeartbeat) > s.stale {
			continue
		}
		attached, err := s.attached(ctx, n)
		if err != nil {
			return nil, fmt.Errorf("finding the carriers of node %s: %w", n.Name, err)
		}
		follow := splitCarriers(n.Follow)
		out = append(out, cluster.WorkerInfo{
			ID: n.ID, Name: n.Name, Type: n.NodeType,
			Attached: attached, Reports: len(follow) > 0, Follow: follow, FollowError: n.FollowError,
		})
	}
	return out, nil
}

// AttachedCarriers returns the carriers one worker is attached to.
func (s *SwitchWorkers) AttachedCarriers(ctx context.Context, nodeID string) ([]cluster.Carrier, error) {
	n, err := s.reg.Get(ctx, nodeID)
	if err != nil {
		return nil, err
	}
	return s.attached(ctx, n)
}

// AgentsAttached reports whether an agent worker that is alive is attached to c.
func (s *SwitchWorkers) AgentsAttached(ctx context.Context, c cluster.Carrier) (bool, error) {
	list, err := s.reg.List(ctx)
	if err != nil {
		return false, err
	}
	for i := range list {
		n := &list[i]
		if n.NodeType != NodeTypeAgent || n.Status == StatusPending || n.Status == StatusOffline || time.Since(n.LastHeartbeat) > s.stale {
			continue
		}
		attached, err := s.attached(ctx, n)
		if err != nil {
			return false, err
		}
		for _, a := range attached {
			if a == c {
				return true, nil
			}
		}
	}
	return false, nil
}

// errTunnelWorkerLoopback is the failure of the dial that the guarded factory
// puts in the place of a dial that cannot mean anything. It is not the answer of
// a host about a backend, so nothing that decides whether a backend is dead may
// act on it.
var errTunnelWorkerLoopback = errors.New("the backend of this worker listens on its own loopback address and the worker holds no address that can be dialled, so it cannot be reached on this carrier")

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
