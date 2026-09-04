// SPDX-License-Identifier: MIT

package application

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/nodes"
	"gorm.io/gorm"
)

// startJobDispatchLoop builds AND STARTS the loop that takes queued work off the
// job store and drives it on an agent worker.
//
// One function rather than a construction here and a Start somewhere else, and
// that is the point rather than tidiness. A loop that is built and never
// started is a replica that writes claim rows and takes none, so every job in
// the deployment is accepted and none is ever run, and nothing anywhere says
// so. As a separate statement in the start-up path that line's loss has no
// symptom and no spec reaches it: initDistributed opens a database and a bus.
// Fused here, the loop cannot exist without running.
//
// The rest is a named function for the reason newAgentControl is one: two of
// these arguments are silent when they are wrong.
//
// The BROADCASTER is the one worth naming. It is what re-publishes the progress
// and result lines a worker asks for, and it is checked against the allow list
// for that worker's node type. A loop built without one dispatches work
// perfectly well and every SSE stream in the deployment goes quiet: the job
// runs, the answer is persisted, and the user watching it sees nothing until
// they reload. That is a whole feature lost to a nil field, with no error
// anywhere, so it is refused here.
//
// The re-broadcaster is taken already built, from newFanoutBridges, and is a
// *nodes.Rebroadcaster rather than the jobs.ProgressBroadcaster interface the
// loop stores it as. Both of those are deliberate. Taking it built leaves ONE
// expression in the tree that decides which carrier job and agent fan-out goes
// on, next to the dispatcher and the bridge that must read the same one, so
// there is no separate line here to point at a carrier nobody subscribes to:
// that mis-wiring publishes successfully, returns true, reddens no spec in any
// package, and shows up only as an SSE stream with no progress in it. Naming
// the concrete type is what makes the refusal below fire, too: widened to the
// interface, a nil re-broadcaster is a non-nil value holding a nil pointer.
//
// The SELECTOR is built here rather than borrowed from newAgentControl, and
// deliberately: nodes.AgentSelector holds no per-caller state, and sharing one
// would couple the dispatch loop's lifetime to MCP's for nothing.
func startJobDispatchLoop(ctx context.Context, cfg config.DistributedConfig, db *gorm.DB, store *jobs.JobStore,
	registry *nodes.NodeRegistry, conns nodes.AgentConnectionReader,
	control *nodes.ControlClient, broadcast *nodes.Rebroadcaster) (*jobs.DispatchLoop, error) {
	if cfg.InstanceID == "" {
		return nil, fmt.Errorf("the job dispatch loop was built with no instance id: its claims could not be told from ones a dead replica left")
	}
	if registry == nil || conns == nil {
		return nil, fmt.Errorf("the job dispatch loop was built with no way to find a connected agent worker")
	}
	if broadcast == nil {
		return nil, fmt.Errorf("the job dispatch loop was built with no broadcaster: every job would run with its progress and its result reaching no SSE stream in the deployment")
	}
	loop, err := jobs.NewDispatchLoop(jobs.DispatchConfig{
		DB:       db,
		Owner:    cfg.InstanceID,
		Selector: nodes.NewAgentSelector(registry, conns, cfg.InstanceID),
		Control:  control,
		// The allow list lives in nodes and is keyed on the worker's node type;
		// nothing here decides what a worker may broadcast on.
		Broadcast: broadcast,
		Store:     store,
	})
	if err != nil {
		return nil, err
	}
	if err := loop.Start(ctx); err != nil {
		return nil, err
	}
	return loop, nil
}
