package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// DefaultChatStartTimeout bounds a chat start without a deadline. A start only
// claims the turn, opens the chat and hands the message to the loop, so it
// answers in well under a second; the budget covers a slow database.
const DefaultChatStartTimeout = 30 * time.Second

// NATSChatStarter sends the frontend's chat starts to the chat-workers queue
// group as NATS request/reply. One worker of the group runs the turn.
type NATSChatStarter struct{ bus messaging.MessagingClient }

// NewNATSChatStarter returns a NATSChatStarter on bus.
func NewNATSChatStarter(bus messaging.MessagingClient) *NATSChatStarter {
	return &NATSChatStarter{bus: bus}
}

// StartChat asks one chat worker to start the turn. It returns an error that
// wraps ErrNoRoute when no worker is listening. A timeout is an ordinary
// error: the worker may have started the turn. A decoded reply is returned
// with a nil error even when its Error field holds the worker's refusal. Only
// the deadline of ctx bounds the wait, as for the agent RPC.
func (c *NATSChatStarter) StartChat(ctx context.Context, req workerctl.ChatStartRequest) (*workerctl.ChatStartReply, error) {
	timeout, err := agentRequestTimeout(ctx, DefaultChatStartTimeout)
	if err != nil {
		return nil, err
	}
	return controlRequestJSON[workerctl.ChatStartRequest, workerctl.ChatStartReply](c.bus, messaging.SubjectChatStart, req, timeout)
}

// NATSChatStartServer is the chat worker's end of NATSChatStarter.
type NATSChatStartServer struct{ bus messaging.MessagingClient }

// NewNATSChatStartServer returns a server on bus.
func NewNATSChatStartServer(bus messaging.MessagingClient) *NATSChatStartServer {
	return &NATSChatStartServer{bus: bus}
}

// ChatStartHandler runs one chat start on the worker.
type ChatStartHandler func(ctx context.Context, req workerctl.ChatStartRequest) workerctl.ChatStartReply

// Serve answers chat starts in the chat-workers queue group until the returned
// subscription is unsubscribed. The handler runs inline on the delivery
// goroutine: a start returns once the turn is handed to the loop, and the turn
// itself runs in the background. Its context carries DefaultChatStartTimeout,
// so a stuck store cannot hold the worker's queue forever. An undecodable body
// is answered with an "unmarshal error: " refusal.
func (s *NATSChatStartServer) Serve(h ChatStartHandler) (messaging.Subscription, error) {
	sub, err := s.bus.QueueSubscribeReply(messaging.SubjectChatStart, messaging.QueueChatWorkers, func(data []byte, reply func([]byte)) {
		var req workerctl.ChatStartRequest
		if err := json.Unmarshal(data, &req); err != nil {
			sendAgentReply(reply, workerctl.ChatStartReply{Error: fmt.Sprintf("unmarshal error: %v", err)})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), DefaultChatStartTimeout)
		defer cancel()
		sendAgentReply(reply, h(ctx, req))
	})
	if err != nil {
		return nil, fmt.Errorf("serving chat starts on %s: %w", messaging.SubjectChatStart, err)
	}
	return sub, nil
}
