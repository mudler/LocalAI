package worker

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// natsControlServer serves control verbs on this node's NATS subjects.
type natsControlServer struct {
	bus    messaging.MessagingClient
	nodeID string
}

func newNATSControlServer(bus messaging.MessagingClient, nodeID string) *natsControlServer {
	return &natsControlServer{bus: bus, nodeID: nodeID}
}

func (n *natsControlServer) subject(v controlVerb) (string, error) {
	switch v {
	case verbBackendInstall:
		return messaging.SubjectNodeBackendInstall(n.nodeID), nil
	case verbBackendUpgrade:
		return messaging.SubjectNodeBackendUpgrade(n.nodeID), nil
	case verbBackendStop:
		return messaging.SubjectNodeBackendStop(n.nodeID), nil
	case verbBackendDelete:
		return messaging.SubjectNodeBackendDelete(n.nodeID), nil
	case verbBackendList:
		return messaging.SubjectNodeBackendList(n.nodeID), nil
	case verbModelsRunning:
		return messaging.SubjectNodeModelsRunning(n.nodeID), nil
	case verbModelUnload:
		return messaging.SubjectNodeModelUnload(n.nodeID), nil
	case verbModelStop:
		return messaging.SubjectNodeModelStop(n.nodeID), nil
	case verbModelDelete:
		return messaging.SubjectNodeModelDelete(n.nodeID), nil
	case verbNodeStop:
		return messaging.SubjectNodeStop(n.nodeID), nil
	case verbFilesEnsure:
		return messaging.SubjectNodeFilesEnsure(n.nodeID), nil
	case verbFilesStage:
		return messaging.SubjectNodeFilesStage(n.nodeID), nil
	case verbFilesTemp:
		return messaging.SubjectNodeFilesTemp(n.nodeID), nil
	case verbFilesListDir:
		return messaging.SubjectNodeFilesListDir(n.nodeID), nil
	case verbFilesRelease:
		return messaging.SubjectNodeFilesRelease(n.nodeID), nil
	}
	return "", fmt.Errorf("no NATS subject for control verb %q", v)
}

// handle runs h inside the subscription callback, so NATS delivers one request
// of the verb at a time, as it did before the verbs had a carrier seam. The
// undecodable error is dropped because reply already carries the typed
// refusal the requester expects. A panic is deliberately not recovered: the
// worker exits and goes unhealthy instead of leaving the requester to time out.
func (n *natsControlServer) handle(v controlVerb, h controlHandler) error {
	subject, err := n.subject(v)
	if err != nil {
		return fmt.Errorf("serving %s: %w", v, err)
	}
	if _, err := n.bus.SubscribeReply(subject, func(data []byte, reply func([]byte)) {
		if r, _ := h(context.Background(), data); r != nil {
			replyJSON(reply, r)
		}
	}); err != nil {
		return fmt.Errorf("serving %s: %w", v, err)
	}
	return nil
}

// handleWithProgress spawns a goroutine per request so a multi-minute install
// does not hold up the next request on the same subscription.
func (n *natsControlServer) handleWithProgress(v controlVerb, h progressControlHandler) error {
	subject, err := n.subject(v)
	if err != nil {
		return fmt.Errorf("serving %s: %w", v, err)
	}
	progress := func(ev workerctl.BackendInstallProgressEvent) {
		// A lost progress event only delays the UI bar; the terminal reply is
		// what the requester acts on.
		_ = n.bus.Publish(messaging.SubjectNodeBackendInstallProgress(n.nodeID, ev.OpID), ev)
	}
	if _, err := n.bus.SubscribeReply(subject, func(data []byte, reply func([]byte)) {
		go func() {
			if r, _ := h(context.Background(), data, progress); r != nil {
				replyJSON(reply, r)
			}
		}()
	}); err != nil {
		return fmt.Errorf("serving %s: %w", v, err)
	}
	return nil
}
