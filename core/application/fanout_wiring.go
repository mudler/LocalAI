// SPDX-License-Identifier: MIT

package application

import (
	"fmt"

	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/pgbus"
	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// newFanoutBridges builds the three surfaces whose cross-replica traffic is job
// and agent fan-out, on the deployment's ONE broadcast carrier.
//
// One function rather than two constructor calls in the start-up path, and the
// parameter type is the reason. jobs.NewDispatcher and agents.NewEventBridge
// both take a messaging.Broadcaster, which they must: neither may know which
// carrier a deployment runs, and their specs publish through a double. But that
// also means *messaging.Client satisfies them, so wiring either of them to the
// NATS client instead of the carrier COMPILES, passes every unit spec in both
// packages, and presents only as an SSE stream that stays empty while the work
// it is watching runs to completion on the other side of a carrier nobody is
// subscribed to. Naming *pgbus.Bus here is what makes that a build failure
// rather than a silent one, and it is why this exists as a function instead of
// as two lines and a comment asking the reader to be careful.
//
// The observable persister is STARTED here for the reason startJobDispatchLoop
// starts its loop: a bridge that is built and never subscribed captures nothing
// a worker publishes, and the only symptom is observables that quietly stop
// being written on a deployment that has workers.
//
// The re-broadcaster is built HERE and handed to the dispatch loop rather than
// built there from a carrier of its own, and that is the whole reason this
// function returns three things. It is the surface a reviewer skips: it is what
// turns an agent worker's progress line into a broadcast, so pointing it at a
// carrier nobody subscribes to leaves every unit spec in every package passing.
// The re-broadcaster publishes, the publish succeeds, Handle returns true, and
// the only symptom in the deployment is an SSE stream with no progress in it.
// Written as one expression shared with the dispatcher and the bridge, that
// mis-wiring stops being a line a spec has to guess at: there is no second
// carrier in scope to point it at.
// cancelCarrier is NOT the same carrier and must not be folded into bus. Every
// family this function wires has both of its ends on a frontend replica and so
// moves to the broadcast carrier whole, with one exception: the only subscriber
// to agent.<name>.cancel is the agent WORKER, which has no database and cannot
// join the PostgreSQL carrier at all. A cancel published on bus would therefore
// reach no worker, and every cancel of a worker-run agent would be lost while
// reporting success. It stays on the carrier the worker reads until a cancel
// rides the worker's tunnel instead of a broadcast.
func newFanoutBridges(bus *pgbus.Bus, cancelCarrier messaging.Broadcaster,
	jobStore *jobs.JobStore, agentStore *agents.AgentStore,
	db *gorm.DB, instanceID string) (*jobs.Dispatcher, *agents.EventBridge, *nodes.Rebroadcaster, error) {
	// A nil check on the CONCRETE pointer, before it is widened. Once it is a
	// messaging.Broadcaster a nil *pgbus.Bus is a non-nil interface holding a
	// nil pointer, so every guard downstream reads it as a carrier that is
	// present and every publish through it panics on a request instead.
	if bus == nil {
		return nil, nil, nil, fmt.Errorf("the job and agent fan-out bridges were built with no broadcast carrier: every job's progress and every agent's events would reach no SSE stream in the deployment")
	}

	if cancelCarrier == nil {
		return nil, nil, nil, fmt.Errorf("the agent event bridge was built with no carrier for agent cancels: every cancel of a worker-run agent would be published where no worker listens and reported as sent")
	}

	dispatcher := jobs.NewDispatcher(jobStore, bus, db, instanceID)
	bridge := agents.NewEventBridge(bus, agentStore, instanceID).WithCancelCarrier(cancelCarrier)

	// Warned rather than refused, and deliberately: the persister needs a store
	// and a deployment without one still serves live SSE correctly. What it
	// loses is the durable copy of a worker's observables, which is degraded
	// rather than broken.
	if err := bridge.StartObservablePersister(); err != nil {
		xlog.Warn("Failed to start observable persister", "error", err)
	} else {
		xlog.Info("Observable persister started")
	}

	return dispatcher, bridge, nodes.NewRebroadcaster(bus), nil
}
