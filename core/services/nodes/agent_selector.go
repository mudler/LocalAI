package nodes

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"

	"github.com/mudler/LocalAI/core/services/cluster"
)

// ErrNoAgentWorker reports that no agent worker currently holds a tunnel that a
// live replica can reach. It is a fact about this moment and says nothing about
// any worker, so it wraps ErrNoRoute and nothing else: no worker is marked
// unhealthy or removed because of it.
var ErrNoAgentWorker = fmt.Errorf("no agent worker holds a tunnel to this cluster: %w", ErrNoRoute)

// AgentConnectionReader is the part of cluster.Registry that picking needs. It
// is a port so that a spec can decide which nodes are connected without staging
// the connection rows.
type AgentConnectionReader interface {
	ConnectedAmong(ctx context.Context, nodeIDs []string, owner string) (held []string, heldByOwner []string, err error)
}

var _ AgentConnectionReader = (*cluster.Registry)(nil)

// AgentSelector picks the agent worker that gets a request.
//
// A queue group of a broker did one thing here: it chose one subscriber out of
// several. Choosing is a query. The selector asks which agent nodes may take
// work and which of them hold a tunnel, and it prefers a worker whose tunnel
// this replica holds, because the call to it needs no relay.
//
// It answers a routing question and never an absence question. A worker that is
// not connected cannot be picked, whatever the reason.
type AgentSelector struct {
	registry *NodeRegistry
	conns    AgentConnectionReader
	// self is the id of this replica, as the connection rows record an owner.
	self string
}

// NewAgentSelector returns the selector for the agent nodes that registry lists,
// reading connections through conns, for the replica self.
func NewAgentSelector(registry *NodeRegistry, conns AgentConnectionReader, self string) *AgentSelector {
	return &AgentSelector{registry: registry, conns: conns, self: self}
}

// PickConnected returns the id and the node type of an agent node whose tunnel a
// live replica holds. The type comes back because the caller that publishes the
// events of a run needs it for every line, and the selector has read the node
// rows already.
func (s *AgentSelector) PickConnected(ctx context.Context) (nodeID, nodeType string, err error) {
	return s.pickConnectedExcluding(ctx, nil)
}

// pickConnectedExcluding is PickConnected without the nodes in tried. A retry
// that could pick the worker that has just failed would be the same call again.
func (s *AgentSelector) pickConnectedExcluding(ctx context.Context, tried map[string]bool) (string, string, error) {
	if s == nil || s.registry == nil || s.conns == nil {
		return "", "", fmt.Errorf("this deployment has no agent selector: %w", ErrNoAgentWorker)
	}
	agents, err := s.registry.selectableAgentNodes(ctx)
	if err != nil {
		// Plain on purpose. A database that does not answer says nothing about a
		// worker, so this is neither ErrNoRoute nor ErrNoAgentWorker.
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
	held, heldByOwner, err := s.conns.ConnectedAmong(ctx, ids, s.self)
	if err != nil {
		return "", "", fmt.Errorf("reading which agent workers are connected: %w", err)
	}
	candidates := heldByOwner
	if len(candidates) == 0 {
		candidates = held
	}
	if len(candidates) == 0 {
		return "", "", fmt.Errorf("of %d agent workers registered, none is connected: %w", len(ids), ErrNoAgentWorker)
	}
	// Random and not round robin: a counter on one replica says nothing about the
	// load, and several replicas keep several counters that agree on nothing.
	// #nosec G404 -- spreads work across equivalent workers; the choice is no secret.
	picked := candidates[rand.IntN(len(candidates))]
	nodeType := typeOf[picked]
	if nodeType == "" {
		return "", "", errors.New("the connection read named an agent worker that the node read did not: " + picked)
	}
	return picked, nodeType, nil
}

// selectableAgentNodes lists the agent nodes that a request may go to.
//
// A node that waits for approval is out, and so is a node that was told to stop
// taking work. Every other status stays in, on purpose: unhealthy and offline are
// written by the health monitor on its own clock, and whether a request can reach
// the worker now is what the connection read answers. Filtering on a health
// verdict here would refuse a worker that is connected and answering because a
// probe was late.
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
