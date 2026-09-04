// SPDX-License-Identifier: MIT

package nodes

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/xlog"
)

// ErrNoAgentWorker reports that no agent worker in this deployment currently
// holds a tunnel any live replica can reach.
//
// It is a deployment fact about this moment and NOT a verdict about any worker,
// which is why it is deliberately its own sentinel: it does not wrap
// ErrWorkerUnroutable, and cluster.IsWorkerAnswer does not accept it. Nothing
// was asked of any worker and nothing was learned about one, so no reap guard
// may act on it and nothing may be marked unhealthy because of it.
//
// The umbrella matters more than it looks. ErrWorkerUnroutable is what every
// path that DELETES a node_models row matches on, and an empty agent fleet is
// not evidence about a single node.
var ErrNoAgentWorker = errors.New("nodes: no agent worker holds a tunnel to this cluster")

// AgentConnectionReader is the narrow port onto cluster.Registry.ConnectedAmong.
//
// An interface rather than the concrete registry so a spec can drive the
// selection by deciding what is connected, without also having to stage the
// claim rows and instance heartbeats that would produce that answer. The real
// read is pinned by its own specs, against the real database, in
// core/services/cluster.
type AgentConnectionReader interface {
	ConnectedAmong(ctx context.Context, nodeIDs []string, owner string) (held []string, heldByOwner []string, err error)

	// Presence is what separates a worker that is GONE from one that is
	// RECONNECTING, and a fan-out verb needs that split where a pick does not.
	//
	// A pick only ever asks who can be reached now. A cancel has to say what
	// happened to the workers it could NOT reach, and those are two different
	// answers: a departure older than the grace is a routing fact the caller
	// may disregard, and a departure inside it is an absent connection nobody
	// may act on, including by reporting that the run was not found.
	Presence(ctx context.Context, nodeID string, grace time.Duration) (cluster.Presence, error)
}

// AgentSelector picks an agent worker to send a control RPC to.
//
// It is what replaces a NATS queue group. The queue group was never a broker
// feature this design has to reproduce: what it did was SELECT one subscriber
// out of a set, and selection is a query. Asking it here is strictly better
// than letting a broker balance, for one concrete reason: this replica can
// prefer an agent whose tunnel it holds itself and skip the relay hop
// altogether, which hidden balancing could never do.
//
// It answers a ROUTING question and never an absence one. See ErrNoAgentWorker.
type AgentSelector struct {
	registry *NodeRegistry
	conns    AgentConnectionReader
	// selfInstanceID is this replica's id, the one the connection rows record
	// as an owner. An empty one is not an error the selector can report: every
	// call would simply take a relay hop with nothing saying so. It is refused
	// where the selector is built instead.
	selfInstanceID string
	// grace is how long a lost tunnel is read as reconnecting rather than gone.
	// It is the operator's trade (DistributedConfig.WorkerReconnectGrace) and
	// is only consulted by Reachable; a pick never needs it, because a worker
	// that is not connected cannot be picked whatever the reason.
	grace time.Duration
}

// NewAgentSelector returns the selector for the agent workers registry knows
// about, reading presence through conns and preferring the tunnels
// selfInstanceID holds.
// grace is the window a departure must outlive before Reachable calls a worker
// gone; a non-positive one takes config.DefaultWorkerReconnectGrace rather than
// zero, because a zero grace would call every worker that lost its tunnel a
// millisecond ago GONE and quietly drop the cancels addressed to it.
func NewAgentSelector(registry *NodeRegistry, conns AgentConnectionReader, selfInstanceID string, grace time.Duration) *AgentSelector {
	return &AgentSelector{
		registry:       registry,
		conns:          conns,
		selfInstanceID: selfInstanceID,
		grace:          cmp.Or(grace, config.DefaultWorkerReconnectGrace),
	}
}

// AgentReach is what this deployment can say about its agent workers right now,
// and it is deliberately two lists rather than one plus a count.
//
// Connected are the workers a LIVE replica holds a tunnel to. A verb may be
// issued to each of them, relayed by the peer mesh when the holder is not this
// replica.
//
// Absent are the registered agent workers that no live replica holds AND whose
// departure has not outlived the grace, plus those whose presence could not be
// read at all. Nothing may be asked of them and, far more importantly, nothing
// may be CONCLUDED from their silence: a fan-out that ignored them would report
// "no worker is running that execution" about a fleet it had not finished
// asking.
//
// Workers whose departure IS older than the grace appear in neither list. That
// is the one routing fact this system lets a caller act on, and leaving them in
// Absent would make every deployment that has ever retired an agent worker
// report every cancel as undelivered for ever.
type AgentReach struct {
	Connected []string
	Absent    []string
}

// PickConnected returns the id AND the node type of an agent node whose tunnel
// a live replica holds, preferring one THIS replica owns so the call skips the
// relay.
//
// The type is returned rather than looked up again because the caller that
// re-broadcasts a resulting progress stream needs it for every line, and the
// selector has ALREADY read the node rows to build the candidate list, so it
// costs nothing here. Resolving it through the registry per line would be a
// database read per progress line and, worse, a SECOND place in the tree that
// decides what a node's type is.
//
// It is always NodeTypeAgent today. It is returned as a value rather than
// asserted as a constant because the day a backend worker takes a dispatched
// claim, the caller that must not guess is this one.
func (s *AgentSelector) PickConnected(ctx context.Context) (nodeID, nodeType string, err error) {
	return s.pickConnectedExcluding(ctx, nil)
}

// pickConnectedExcluding is PickConnected with a set of ids already tried.
//
// The exclusion exists for the retry in AgentControlClient and is not exposed:
// a retry that could re-pick the worker that just failed to answer is not a
// retry, it is the same call again, and with a small fleet it would be the
// same call most of the time.
func (s *AgentSelector) pickConnectedExcluding(ctx context.Context, tried map[string]bool) (string, string, error) {
	if s == nil || s.registry == nil || s.conns == nil {
		// Not an error about any worker. A deployment with no selector has
		// nothing to select from, which is the same answer as an empty fleet.
		return "", "", fmt.Errorf("this deployment has no agent selector: %w", ErrNoAgentWorker)
	}
	agents, err := s.registry.selectableAgentNodes(ctx)
	if err != nil {
		// Deliberately plain. A database that would not answer says nothing
		// about any worker, so this must carry neither ErrWorkerUnroutable nor
		// anything cluster.IsWorkerAnswer accepts.
		return "", "", fmt.Errorf("listing the agent workers of this deployment: %w", err)
	}
	typeOf := make(map[string]string, len(agents))
	ids := make([]string, 0, len(agents))
	for _, a := range agents {
		if tried[a.ID] {
			continue
		}
		typeOf[a.ID] = a.NodeType
		ids = append(ids, a.ID)
	}
	held, heldByOwner, err := s.conns.ConnectedAmong(ctx, ids, s.selfInstanceID)
	if err != nil {
		return "", "", fmt.Errorf("reading which agent workers are connected: %w", err)
	}
	// heldByOwner first because that call takes no relay hop. Falling back to
	// the rest rather than stopping there is what keeps a replica that holds no
	// agent tunnel able to reach the fleet at all.
	candidates := heldByOwner
	if len(candidates) == 0 {
		candidates = held
	}
	if len(candidates) == 0 {
		return "", "", fmt.Errorf("of %d agent workers registered, none is connected: %w", len(ids), ErrNoAgentWorker)
	}
	// Random rather than round robin. A per-replica counter is per-replica
	// state that says nothing about load, and with several replicas the
	// counters agree on nothing anyway.
	picked := candidates[rand.IntN(len(candidates))]
	nodeType := typeOf[picked]
	if nodeType == "" {
		// A candidate the connection read named that the node read did not is
		// this selector contradicting itself. Refused rather than returned with
		// an empty type: an unknown node type is denied by every allow list
		// downstream, and the only symptom of that is work that silently never
		// happens.
		return "", "", fmt.Errorf("agent worker %q was reported connected but has no node type: %w", picked, ErrNoAgentWorker)
	}
	return picked, nodeType, nil
}

// Reachable splits this deployment's agent workers into the ones a verb can be
// issued to and the ones whose silence proves nothing.
//
// It exists for a cancel, which is a fan-out and not a pick: the frontend does
// not know which worker holds a given execution, so it asks every worker it can
// reach and assembles their answers. What it must never do is assemble an
// answer out of a fleet it only partly asked, which is why the workers it could
// not reach come back named rather than dropped.
func (s *AgentSelector) Reachable(ctx context.Context) (AgentReach, error) {
	if s == nil || s.registry == nil || s.conns == nil {
		return AgentReach{}, fmt.Errorf("this deployment has no agent selector: %w", ErrNoAgentWorker)
	}
	agents, err := s.registry.cancellableAgentNodes(ctx)
	if err != nil {
		return AgentReach{}, fmt.Errorf("listing the agent workers of this deployment: %w", err)
	}
	ids := make([]string, 0, len(agents))
	for _, a := range agents {
		ids = append(ids, a.ID)
	}
	held, _, err := s.conns.ConnectedAmong(ctx, ids, s.selfInstanceID)
	if err != nil {
		return AgentReach{}, fmt.Errorf("reading which agent workers are connected: %w", err)
	}
	connected := make(map[string]bool, len(held))
	for _, id := range held {
		connected[id] = true
	}
	reach := AgentReach{Connected: held}
	for _, id := range ids {
		if connected[id] {
			continue
		}
		p, err := s.conns.Presence(ctx, id, s.grace)
		if err != nil {
			// A read that FAILED is not an answer about this worker, so it
			// cannot license the caller to conclude anything. Counted absent,
			// which is the value nobody may act on, rather than gone, which is
			// the one value that would let a cancel be reported as a run that
			// does not exist.
			xlog.Warn("Could not read an agent worker's presence; counting it as one this cancel could not reach",
				"nodeID", id, "error", err)
			reach.Absent = append(reach.Absent, id)
			continue
		}
		if p == cluster.PresenceGone {
			continue
		}
		reach.Absent = append(reach.Absent, id)
	}
	return reach, nil
}

// cancellableAgentNodes returns the agent nodes a fan-out verb may be OFFERED
// to, which is not the same set as the ones work may be PLACED on.
//
// It differs from selectableAgentNodes in exactly one status, and the
// difference is the whole reason it exists. A DRAINING worker may take no new
// work, which is the operator's decision, but it is still finishing the runs it
// already holds. A cancel is not new work: it is a request about a run that is
// executing right now, and excluding a draining worker would report the cancel
// of a live execution as a run that no worker is running.
//
// StatusPending stays excluded. An unapproved node is refused by the tunnel
// route on every dial, so it can hold no execution to cancel; counting it would
// make every cancel in a deployment with one unapproved node report undelivered
// for ever.
func (r *NodeRegistry) cancellableAgentNodes(ctx context.Context) ([]BackendNode, error) {
	var agents []BackendNode
	if err := r.db.WithContext(ctx).
		Where("node_type = ? AND status <> ?", NodeTypeAgent, StatusPending).
		Order("id").
		Find(&agents).Error; err != nil {
		return nil, fmt.Errorf("listing agent nodes for a fan-out verb: %w", err)
	}
	return agents, nil
}

// selectableAgentNodes returns the agent nodes a control RPC may be sent to.
//
// Two statuses are excluded and no more. StatusPending is a node an admin has
// not approved, which cannot hold a tunnel anyway and must never be handed
// work; StatusDraining is one that has been asked to stop taking work, which is
// the operator's decision and not a routing fact.
//
// Every other status is left in ON PURPOSE, and this is where the invariant
// bites. StatusUnhealthy and StatusOffline are written by the health monitor
// and by shutdown, on their own clocks; whether a request can reach this worker
// RIGHT NOW is what ConnectedAmong answers, and it answers it from the
// connection rows. Filtering here on a health verdict would make the selection
// refuse a worker that is connected and answering because a probe was late,
// which is an absence read leaking into a routing one.
func (r *NodeRegistry) selectableAgentNodes(ctx context.Context) ([]BackendNode, error) {
	var agents []BackendNode
	if err := r.db.WithContext(ctx).
		Where("node_type = ? AND status NOT IN ?", NodeTypeAgent, []string{StatusPending, StatusDraining}).
		Order("id").
		Find(&agents).Error; err != nil {
		return nil, fmt.Errorf("listing agent nodes: %w", err)
	}
	return agents, nil
}

// The real reader is the cluster registry, asserted here so a drift between
// its signature and this port fails to COMPILE. Without it the only thing that
// would notice is the wiring in core/application, which opens a database and a
// bus and so has no unit spec at all.
var _ AgentConnectionReader = (*cluster.Registry)(nil)
