// SPDX-License-Identifier: MIT

package application

import (
	"fmt"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/nodes"
)

// newAgentControl builds the frontend's agent control client: the SELECTION
// that decides which agent worker answers a verb, and the control client that
// carries the verb to it.
//
// A named function rather than two more lines in initDistributed, for the
// reason distributedSchedulerOptions is one: initDistributed opens a database
// and a bus, so no unit spec reaches it, and the argument that matters here has
// no symptom when it is wrong. An empty instance id makes every connection read
// report nothing as held by this replica, so every MCP call takes a relay hop
// through a peer even when this replica holds the worker's tunnel itself, and
// nothing anywhere says so: the calls all succeed, just through one more
// process than they need. It is refused here instead of shipped as latency.
//
// The other two refusals are the ordinary kind. A selector with no registry has
// nothing to select from and a client with no transport reaches nobody, and
// both would present as MCP being quietly unavailable in a deployment that
// looks healthy.
//
// This client is also the deployment's agent CANCELLER, which is why the agent
// event bridge takes it: a cancel is a control RPC on the tunnels the workers
// hold, and the reconnect grace it is built with is what decides whether a
// worker that is not connected makes a cancel undelivered or is simply gone.
func newAgentControl(cfg config.DistributedConfig, registry *nodes.NodeRegistry,
	conns nodes.AgentConnectionReader, control *nodes.ControlClient) (*nodes.AgentControlClient, error) {
	if cfg.InstanceID == "" {
		return nil, fmt.Errorf("the agent control client was built with no instance id: every MCP call would relay through a peer even for a worker whose tunnel this replica holds")
	}
	if registry == nil || conns == nil {
		return nil, fmt.Errorf("the agent control client was built with no way to find a connected agent worker")
	}
	if control == nil {
		return nil, fmt.Errorf("the agent control client was built with no control transport to reach an agent worker over")
	}
	return nodes.NewAgentControlClient(nodes.NewAgentSelector(registry, conns, cfg.InstanceID, cfg.WorkerReconnectGrace), control), nil
}
