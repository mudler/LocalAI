package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/internal"
)

// StartChatWorker makes a chat worker (LOCALAI_CHAT_WORKER) serve the chat
// starts that frontends send. It does nothing on any other instance. Call it
// after the HTTP router is built: the turns reach the models through the
// router, as on a frontend, so a start served before it would fail.
//
// State mode: stateless here. The turn claim, its heartbeat and the waiting
// messages are in the shared database, and the frontend leader's reaper ends
// the turns of a worker that died.
func (a *Application) StartChatWorker() error {
	if !a.applicationConfig.Distributed.ChatWorker {
		return nil
	}
	d := a.Distributed()
	if d == nil || d.Nats == nil {
		return errors.New("chat worker: distributed mode with NATS is required")
	}
	if a.studioService == nil {
		return errors.New("chat worker: the chats did not start (see the earlier log, or LOCALAI_DISABLE_CHATS)")
	}
	sub, err := nodes.NewNATSChatStartServer(d.Nats, nodes.WithChatStartTimeout(a.applicationConfig.Distributed.ChatStartTimeout)).Serve(a.studioService.ServeStart)
	if err != nil {
		return fmt.Errorf("chat worker: %w", err)
	}
	a.studioWorkerSub = sub
	if d.Registry != nil {
		node, err := registerChatWorkerNode(a.applicationConfig.Context, d.Registry, chatWorkerNodeName(a.applicationConfig.Distributed), chatWorkerHeartbeat)
		if err != nil {
			// The worker serves starts all the same; it is only missing from
			// the node list.
			xlog.Warn("Chat worker: could not register in the node list", "error", err)
		} else {
			a.studioWorkerNode = node
		}
	}
	xlog.Info("Chat worker ready", "subject", messaging.SubjectChatStart, "queue", messaging.QueueChatWorkers)
	return nil
}

// stopChatWorker stops taking chat starts and marks the worker's node offline.
// Safe when none were taken.
func (a *Application) stopChatWorker() {
	if a.studioWorkerSub != nil {
		if err := a.studioWorkerSub.Unsubscribe(); err != nil {
			xlog.Warn("Chat worker: could not stop taking chat starts", "error", err)
		}
		a.studioWorkerSub = nil
	}
	if a.studioWorkerNode != nil {
		a.studioWorkerNode.stop()
		a.studioWorkerNode = nil
	}
}

// ChatWorkerRole is the node.role label of a chat worker in the node list.
const ChatWorkerRole = "chat-worker"

// chatWorkerHeartbeat is how often a chat worker tells the node registry that
// it is alive, as the other workers do by default.
const chatWorkerHeartbeat = 10 * time.Second

// chatWorkerNodeName names the worker's row in the node list. A stable
// instance ID keeps one row per worker across restarts; a generated one
// changes on every start, so the host name is used then.
func chatWorkerNodeName(c config.DistributedConfig) string {
	if c.InstanceID != "" && !c.InstanceIDGenerated {
		return "chat-" + c.InstanceID
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = fmt.Sprintf("%d", os.Getpid())
	}
	return "chat-" + host
}

// chatWorkerNode is a chat worker's row in the node registry and the loop that
// keeps it alive.
type chatWorkerNode struct {
	reg    *nodes.NodeRegistry
	id     string
	cancel context.CancelFunc
	done   sync.WaitGroup
}

// registerChatWorkerNode registers the chat worker as an agent node with the
// node.role label ChatWorkerRole and beats every interval until stop. The
// registry schedules models only on backend nodes, so a chat worker never
// gets one. It is approved at once: the worker already holds the frontend's
// database and NATS credentials, so an approval would guard nothing.
//
// State mode: stateless. The row is in the shared database, and the health
// monitor of the frontends marks it offline when the beats stop.
func registerChatWorkerNode(ctx context.Context, reg *nodes.NodeRegistry, name string, interval time.Duration) (*chatWorkerNode, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	node := &nodes.BackendNode{Name: name, NodeType: nodes.NodeTypeAgent, Version: internal.Version, Commit: internal.Commit}
	if err := reg.Register(ctx, node, true); err != nil {
		return nil, fmt.Errorf("register node %q: %w", name, err)
	}
	if err := reg.SetNodeLabel(ctx, node.ID, "node.role", ChatWorkerRole); err != nil {
		xlog.Warn("Chat worker: could not label its node", "node", name, "error", err)
	}
	beatCtx, cancel := context.WithCancel(ctx)
	n := &chatWorkerNode{reg: reg, id: node.ID, cancel: cancel}
	n.done.Go(func() { n.beat(beatCtx, interval) })
	xlog.Info("Chat worker registered in the node list", "node", name, "id", node.ID)
	return n, nil
}

func (n *chatWorkerNode) beat(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := n.reg.Heartbeat(ctx, n.id, &nodes.HeartbeatUpdate{}); err != nil && ctx.Err() == nil {
				xlog.Warn("Chat worker: could not record its heartbeat", "error", err)
			}
		}
	}
}

// stop ends the heartbeat and marks the node offline, as a worker that
// deregisters does: the row stays, so a restart finds it again.
func (n *chatWorkerNode) stop() {
	n.cancel()
	n.done.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := n.reg.MarkOffline(ctx, n.id); err != nil {
		xlog.Warn("Chat worker: could not mark its node offline", "error", err)
	}
}

// chatWorkerGrace is how much longer than a run's own limit a chat worker lets
// it run. The frontend that watches the run stops it at the limit and records
// why; the worker acts only when that frontend is gone.
const chatWorkerGrace = time.Minute

// studioWorkerRunLimit is the bound of a chat worker's turn, by the chat's
// origin: the job limit (LOCALAI_MCP_CI_JOB_TIMEOUT) for a job, the trigger
// run limit for a trigger run, plus chatWorkerGrace. A connector turn has no
// limit on a frontend either.
func (a *Application) studioWorkerRunLimit(origin string) time.Duration {
	switch origin {
	case session.OriginJob:
		limit := a.applicationConfig.MCPCIJobTimeout
		if limit <= 0 {
			limit = config.DefaultMCPCIJobTimeout
		}
		return limit + chatWorkerGrace
	case session.OriginTrigger:
		return studio.DefaultRunLimit + chatWorkerGrace
	}
	return 0
}
