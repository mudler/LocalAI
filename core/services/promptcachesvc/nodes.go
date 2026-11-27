package promptcachesvc

import (
	"context"

	pc "github.com/mudler/LocalAI/pkg/promptcache"
)

// NodeReport is the usage of one node.
type NodeReport struct {
	// Node names the node; "local" for the node that answers.
	Node string `json:"node"`
	// Enabled is false when the tier is off on the node.
	Enabled bool `json:"enabled"`
	// OffReason says why the tier is off on the node (see the Off
	// constants); empty when it is on or the node did not say.
	OffReason string `json:"off_reason,omitempty"`
	// Error says why the node gave no usage (offline, no answer).
	Error string `json:"error,omitempty"`
	pc.NodeUsage
}

// Nodes is where the frontend sends a purge and asks for the usage. The
// single-mode implementation (Local) acts on this node. A distributed
// implementation sends the purge to every worker and gathers their reports;
// it only has to satisfy this interface.
type Nodes interface {
	Purge(ctx context.Context, s Scope) (pc.Result, error)
	Usage(ctx context.Context) ([]NodeReport, error)
}

// LocalNodeName names the node that answers in a report.
const LocalNodeName = "local"

// Local is Nodes for this node only.
type Local struct{ Node *Node }

// Purge deletes the files of the scope on this node.
func (l Local) Purge(ctx context.Context, s Scope) (pc.Result, error) {
	if l.Node == nil {
		return pc.Result{}, nil
	}
	return l.Node.Purge(ctx, s)
}

// Usage reports this node.
func (l Local) Usage(ctx context.Context) ([]NodeReport, error) {
	if l.Node == nil || l.Node.Root() == "" {
		return nil, nil
	}
	u, err := l.Node.Usage(ctx)
	if err != nil {
		return nil, err
	}
	return []NodeReport{{Node: LocalNodeName, Enabled: l.Node.Enabled(), OffReason: l.Node.OffReason(), NodeUsage: u}}, nil
}
