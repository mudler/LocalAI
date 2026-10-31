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

// ChatStartOption configures a NATSChatStarter or a NATSChatStartServer.
type ChatStartOption func(*chatStartOptions)

type chatStartOptions struct{ timeout time.Duration }

// WithChatStartTimeout sets the budget of a chat start: how long the frontend
// waits for the worker's answer when its context has no deadline, and how long
// the worker gives the start. A frontend and its workers must use the same
// value, or the worker can still start a turn that the frontend already
// reported as failed. Zero or less keeps DefaultChatStartTimeout.
func WithChatStartTimeout(d time.Duration) ChatStartOption {
	return func(o *chatStartOptions) {
		if d > 0 {
			o.timeout = d
		}
	}
}

func newChatStartOptions(opts []ChatStartOption) chatStartOptions {
	o := chatStartOptions{timeout: DefaultChatStartTimeout}
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// NATSChatStarter sends the frontend's chat starts to the chat-workers queue
// group as NATS request/reply. One worker of the group runs the turn.
type NATSChatStarter struct {
	bus     messaging.MessagingClient
	timeout time.Duration
}

// NewNATSChatStarter returns a NATSChatStarter on bus.
func NewNATSChatStarter(bus messaging.MessagingClient, opts ...ChatStartOption) *NATSChatStarter {
	return &NATSChatStarter{bus: bus, timeout: newChatStartOptions(opts).timeout}
}

// StartChat asks one chat worker to start the turn. It returns an error that
// wraps ErrNoRoute when no worker is listening. A timeout is an ordinary
// error: the worker may have started the turn. A decoded reply is returned
// with a nil error even when its Error field holds the worker's refusal. The
// deadline of ctx bounds the wait, as for the agent RPC; without one the
// start's budget (WithChatStartTimeout) does.
func (c *NATSChatStarter) StartChat(ctx context.Context, req workerctl.ChatStartRequest) (*workerctl.ChatStartReply, error) {
	timeout, err := agentRequestTimeout(ctx, c.timeout)
	if err != nil {
		return nil, err
	}
	return controlRequestJSON[workerctl.ChatStartRequest, workerctl.ChatStartReply](c.bus, messaging.SubjectChatStart, req, timeout)
}

// NATSChatStartServer is the chat worker's end of NATSChatStarter.
type NATSChatStartServer struct {
	bus     messaging.MessagingClient
	timeout time.Duration
}

// NewNATSChatStartServer returns a server on bus.
func NewNATSChatStartServer(bus messaging.MessagingClient, opts ...ChatStartOption) *NATSChatStartServer {
	return &NATSChatStartServer{bus: bus, timeout: newChatStartOptions(opts).timeout}
}

// ChatStartHandler runs one chat start on the worker.
type ChatStartHandler func(ctx context.Context, req workerctl.ChatStartRequest) workerctl.ChatStartReply

// Serve answers chat starts in the chat-workers queue group until the returned
// subscription is unsubscribed. The handler runs inline on the delivery
// goroutine: a start returns once the turn is handed to the loop, and the turn
// itself runs in the background. Its context carries the start's budget,
// so a stuck store cannot hold the worker's queue forever. An undecodable body
// is answered with an "unmarshal error: " refusal.
func (s *NATSChatStartServer) Serve(h ChatStartHandler) (messaging.Subscription, error) {
	sub, err := s.bus.QueueSubscribeReply(messaging.SubjectChatStart, messaging.QueueChatWorkers, func(data []byte, reply func([]byte)) {
		var req workerctl.ChatStartRequest
		if err := json.Unmarshal(data, &req); err != nil {
			sendAgentReply(reply, workerctl.ChatStartReply{Error: fmt.Sprintf("unmarshal error: %v", err)})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
		defer cancel()
		sendAgentReply(reply, h(ctx, req))
	})
	if err != nil {
		return nil, fmt.Errorf("serving chat starts on %s: %w", messaging.SubjectChatStart, err)
	}
	return sub, nil
}
