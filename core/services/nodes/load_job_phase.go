package nodes

import (
	"context"
	"sync"
)

// loadPhaseReporter carries the current phase of a cold load from the code that
// performs it back to the job runner's heartbeat, without threading a job
// handle through every scheduling function.
//
// It rides on the context the same way the cold-load deadline does (see
// load_deadline.go), so the single-host paths and every test that constructs a
// router directly stay untouched: with no reporter on the context, the report
// calls are no-ops.
type loadPhaseReporter struct {
	mu           sync.Mutex
	state        string
	nodeID       string
	nodeName     string
	replicaIndex int
}

type loadPhaseKey struct{}

func newLoadPhaseReporter() *loadPhaseReporter {
	return &loadPhaseReporter{state: LoadJobStatePending}
}

func withLoadPhaseReporter(ctx context.Context, p *loadPhaseReporter) context.Context {
	return context.WithValue(ctx, loadPhaseKey{}, p)
}

// snapshot returns the phase as a job update. Byte counts are filled in by the
// caller from the staging tracker.
func (p *loadPhaseReporter) snapshot() LoadJobUpdate {
	p.mu.Lock()
	defer p.mu.Unlock()
	return LoadJobUpdate{
		State:        p.state,
		NodeID:       p.nodeID,
		NodeName:     p.nodeName,
		ReplicaIndex: p.replicaIndex,
	}
}

func (p *loadPhaseReporter) set(state string, node *BackendNode, replicaIndex int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state = state
	if node != nil {
		p.nodeID, p.nodeName, p.replicaIndex = node.ID, node.Name, replicaIndex
	}
}

// reportLoadPhase records which phase of a cold load is running, so a waiting
// request can be told "staging to nvidia-thor" rather than nothing at all. A
// context without a reporter (non-distributed loads, reconciler scale-ups,
// tests) is a no-op.
func reportLoadPhase(ctx context.Context, state string, node *BackendNode, replicaIndex int) {
	if p, ok := ctx.Value(loadPhaseKey{}).(*loadPhaseReporter); ok {
		p.set(state, node, replicaIndex)
	}
}
