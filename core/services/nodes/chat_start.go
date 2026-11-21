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

// ChatStartOption configures a ChatStarter or a ChatStartServer.
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

// ChatStarter sends the frontend's chat starts to one chat worker, over the
// peer link of the processes (messaging.ProcessReplies). The frontend picks the
// worker from the node list (the queue of messaging.SubjectChatStart).
type ChatStarter struct {
	bus     messaging.QueueRequester
	timeout time.Duration
}

// NewChatStarter returns a ChatStarter on bus.
func NewChatStarter(bus messaging.QueueRequester, opts ...ChatStartOption) *ChatStarter {
	return &ChatStarter{bus: bus, timeout: newChatStartOptions(opts).timeout}
}

// StartChat asks one chat worker to start the turn. It returns an error that
// wraps ErrNoRoute when no worker is listening, and one that wraps
// messaging.ErrNotDelivered when the start reached no worker. A timeout is an
// ordinary error: the worker may have started the turn. A decoded reply is
// returned with a nil error even when its Error field holds the worker's
// refusal. The deadline of ctx bounds the wait, as for the agent RPC; without
// one the start's budget (WithChatStartTimeout) does.
//
// A worker that is full (ChatStartReply.Full) has started nothing, and the
// start goes to another one, up to three; the start fails as full only when
// every worker tried is full.
func (c *ChatStarter) StartChat(ctx context.Context, req workerctl.ChatStartRequest) (*workerctl.ChatStartReply, error) {
	timeout, err := agentRequestTimeout(ctx, c.timeout)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}
	raw, err := c.bus.RequestQueue(messaging.SubjectChatStart, data, timeout, workerFull)
	if err != nil {
		return nil, mapRequestError(messaging.SubjectChatStart, err)
	}
	var reply workerctl.ChatStartReply
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("unmarshaling reply from %s: %w", messaging.SubjectChatStart, err)
	}
	return &reply, nil
}

func workerFull(raw []byte) bool {
	var reply workerctl.ChatStartReply
	return json.Unmarshal(raw, &reply) == nil && reply.Full
}

// ChatStartServer is the chat worker's end of ChatStarter.
type ChatStartServer struct {
	bus     messaging.ReplyServer
	timeout time.Duration
}

// NewChatStartServer returns a server on bus.
func NewChatStartServer(bus messaging.ReplyServer, opts ...ChatStartOption) *ChatStartServer {
	return &ChatStartServer{bus: bus, timeout: newChatStartOptions(opts).timeout}
}

// ChatStartHandler runs one chat start on the worker.
type ChatStartHandler func(ctx context.Context, req workerctl.ChatStartRequest) workerctl.ChatStartReply

// Serve answers the chat starts that frontends send to this process until the
// returned subscription is unsubscribed. The handler runs inline on the delivery
// goroutine: a start returns once the turn is handed to the loop, and the turn
// itself runs in the background. Its context carries the start's budget,
// so a stuck store cannot hold the worker's queue forever. An undecodable body
// is answered with an "unmarshal error: " refusal.
func (s *ChatStartServer) Serve(h ChatStartHandler) (messaging.Subscription, error) {
	sub, err := s.bus.SubscribeReply(messaging.SubjectChatStart, func(data []byte, reply func([]byte)) {
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
