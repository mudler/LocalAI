package application

import (
	"errors"
	"fmt"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
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
	xlog.Info("Chat worker ready", "subject", messaging.SubjectChatStart, "queue", messaging.QueueChatWorkers)
	return nil
}

// stopChatWorker stops taking chat starts. Safe when none were taken.
func (a *Application) stopChatWorker() {
	if a.studioWorkerSub == nil {
		return
	}
	if err := a.studioWorkerSub.Unsubscribe(); err != nil {
		xlog.Warn("Chat worker: could not stop taking chat starts", "error", err)
	}
	a.studioWorkerSub = nil
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
