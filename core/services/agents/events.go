package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/dbutil"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
)

// AgentEvent is the broadcast payload for agent SSE events.
type AgentEvent struct {
	AgentName      string `json:"agent_name"`
	UserID         string `json:"user_id"`
	EventType      string `json:"event_type"`                // "message", "status", "error", "observable"
	EventSubType   string `json:"event_sub_type,omitempty"`  // e.g. "chat", "tool_result" for observable_update
	SourceInstance string `json:"source_instance,omitempty"` // instance ID that published the event (for dedup)
	Sender         string `json:"sender,omitempty"`
	Content        string `json:"content,omitempty"`
	MessageID      string `json:"message_id,omitempty"`
	Metadata       string `json:"metadata,omitempty"` // JSON metadata
	Timestamp      int64  `json:"timestamp"`          // Unix milliseconds (set by PublishEvent)
}

// AgentWorkerCanceller carries a cancel to the agent workers of this
// deployment, over the tunnels they hold.
//
// A narrow port rather than the control client itself, because this package
// must not import core/services/nodes, and because the only thing a cancel
// needs from the frontend's control plane is this one verb.
//
// Its error vocabulary is the caller's whole answer and each value means
// something different: nil is a worker's own answer that it cancelled the run,
// nodes.ErrAgentCancelUndelivered is a cancel that may have reached nobody, and
// nodes.ErrAgentRunNotOnAnyWorker is every reachable worker answering that it
// is not running that execution. Nothing here may collapse them.
//
// The real implementation is *nodes.AgentControlClient, asserted where both
// packages are already imported (core/application), so a signature drift is a
// build failure rather than a nil field.
type AgentWorkerCanceller interface {
	CancelAgentRun(ctx context.Context, req messaging.AgentCancelRequest) error
}

// EventBridge bridges agent events between the broadcast carrier and SSE
// connections. It enables cross-instance SSE: a user connects to Frontend 1
// while the agent runs on Frontend 2.
type EventBridge struct {
	// bus is the fan-out carrier this bridge subscribes on. A Broadcaster and
	// not a MessagingClient because fan-out is all a bridge needs: an agent's
	// events go to whoever is watching, and there is nothing here to request or
	// to queue.
	bus messaging.Broadcaster

	// workers is how a cancel reaches an agent WORKER, and it is a separate
	// field from bus because the two travel on different carriers on purpose.
	//
	// Every other family here has both of its ends on a frontend replica, so
	// both live on the broadcast carrier. This one does not: the process that
	// holds a worker-run execution's cancel function is the agent worker, and a
	// worker has no database and so cannot join the PostgreSQL carrier at all.
	// It holds an outward-dialled tunnel instead, and a cancel is an ordinary
	// control RPC on it, addressed to the workers a live replica can reach.
	//
	// It is nil on an agent worker, which has nobody to forward a cancel to:
	// there, a cancel ARRIVES as that control verb and is applied to the
	// registry below.
	workers AgentWorkerCanceller
	// pub is where the events this bridge produces GO, which is not always the
	// bus. On an agent worker running a dispatched claim it is the response
	// body of the control RPC the claiming replica is reading, so the events
	// travel back on the same stream as the result rather than on a subject
	// nobody may be subscribed to yet. See WithPublisher.
	pub        messaging.Publisher
	store      *AgentStore
	instanceID string

	// Cancel registry for running agent executions.
	//
	// A POINTER, because WithPublisher hands out a second view of this bridge
	// and both views must reach the SAME registry: the cancel listener runs on
	// the bus-backed bridge and the execution it has to reach runs on the
	// stream-backed one. A copied sync.Map would swallow every cancel.
	cancelRegistry *messaging.CancelRegistry

	// The process-lifetime subscription this bridge owns. The per-request one
	// SubscribeEvents opens belongs to the HTTP handler that opened it.
	obsPersisterSub messaging.Subscription
}

// NewEventBridge creates a new EventBridge on the deployment's fan-out carrier.
// A cancel it cannot apply itself is forwarded to the agent workers through
// workers.
//
// The canceller is a CONSTRUCTOR PARAMETER and not a builder call, and that is
// the whole reason it is spelled here. As a WithWorkerCanceller line it is one
// statement whose loss compiles, passes every suite in this package, and turns
// every cancel of a worker-run agent into a cancel that reached nobody and
// reported nothing: the local registry has no entry, and there is no longer
// anywhere for the cancel to go.
//
// A nil workers is legitimate on an agent worker, which forwards nothing. See
// NewWorkerEventBridge, which is how a worker builds one.
func NewEventBridge(bus messaging.Broadcaster, store *AgentStore, instanceID string, workers AgentWorkerCanceller) *EventBridge {
	return &EventBridge{
		bus:            bus,
		pub:            bus,
		workers:        workers,
		store:          store,
		instanceID:     instanceID,
		cancelRegistry: &messaging.CancelRegistry{},
	}
}

// NewWorkerEventBridge returns the bridge an AGENT WORKER runs on.
//
// It joins no carrier, because there is none for it to join: every event it
// produces is written onto the response body of the control RPC that asked for
// the work (see WithPublisher), and every cancel it must act on arrives as a
// control verb rather than as a broadcast. What it keeps is the cancel
// registry, which is the one piece of state a worker's bridge exists for.
//
// The publisher it holds until a verb hands it a stream REFUSES rather than
// drops. A worker that publishes with no stream to write to has produced an
// event that reached nobody, and a no-op default would make that indetectable.
func NewWorkerEventBridge(instanceID string) *EventBridge {
	return NewEventBridge(unroutedCarrier{}, nil, instanceID, nil)
}

// unroutedCarrier is the carrier an agent worker's bridge holds: there is none.
//
// Both methods fail rather than silently succeeding, because both would
// otherwise be undetectable. A dropped publish is an agent event nobody sees; a
// subscription that never delivers is a listener that never fires.
type unroutedCarrier struct{}

func (unroutedCarrier) Publish(subject string, _ any) error {
	return fmt.Errorf("agents: an agent worker tried to publish %q with no control stream to write it to: a worker joins no carrier, so this event would have reached nobody", subject)
}

func (unroutedCarrier) Subscribe(subject string, _ func([]byte)) (messaging.Subscription, error) {
	return nil, fmt.Errorf("agents: an agent worker tried to subscribe to %q: a worker joins no carrier, so nothing would ever be delivered", subject)
}

// WithPublisher returns a view of this bridge whose events go to pub.
//
// Everything else is SHARED with the receiver, the cancel registry above all: a
// cancel arriving as a control verb must reach an execution that is publishing
// onto a stream, and a bridge that copied the registry would register the
// cancel where nothing looks for it.
//
// A nil pub returns the receiver unchanged rather than a bridge that publishes
// nowhere: on a frontend that leaves the events on the fan-out carrier, and on
// a worker it leaves them on the carrier that refuses loudly.
func (b *EventBridge) WithPublisher(pub messaging.Publisher) *EventBridge {
	if b == nil || pub == nil {
		return b
	}
	view := *b
	view.pub = pub
	return &view
}

// PublishEvent broadcasts an agent event for SSE bridging.
//
// Timestamp is emitted in Unix milliseconds to match the local dispatcher's
// json_message events (see dispatcher.go) and the React UI, which feeds the
// value straight into `new Date(ts)`. Milliseconds also stay within JS's
// safe-integer range, whereas nanoseconds (~1.7e18) do not and lose precision
// when parsed as a JSON number.
func (b *EventBridge) PublishEvent(agentName, userID string, evt AgentEvent) error {
	evt.Timestamp = time.Now().UnixMilli()
	subject := messaging.SubjectAgentEvents(agentName, userID)
	return b.pub.Publish(subject, evt)
}

// PersistObservable publishes an observable_update SSE event for real-time UI
// updates and, if a database store is available, writes the record to the DB.
// When the store is nil (e.g. on agent workers), the event is still published so
// the frontend can persist it via StartObservablePersister.
func (b *EventBridge) PersistObservable(agentName, userID, eventType string, obs any) {
	payload := dbutil.MarshalJSON(obs)
	recordID := uuid.New().String()

	// Persist locally if we have a store (frontend instances)
	if b.store != nil {
		b.store.AppendObservable(&AgentObservableRecord{
			ID:          recordID,
			AgentName:   AgentKey(userID, agentName),
			EventType:   eventType,
			PayloadJSON: payload,
			CreatedAt:   time.Now(),
		})
	}

	// Always broadcast, which is what enables real-time SSE and remote persistence.
	b.PublishEvent(agentName, userID, AgentEvent{
		AgentName:      agentName,
		UserID:         userID,
		EventType:      "observable_update",
		EventSubType:   eventType,
		SourceInstance: b.instanceID,
		MessageID:      recordID,
		Metadata:       payload,
	})
}

// PublishMessage broadcasts a chat message event for SSE bridging.
// Uses "json_message" event type to match the React UI's expected SSE format.
// Conversation history is managed client-side (browser localStorage), not server-side.
func (b *EventBridge) PublishMessage(agentName, userID, sender, content, messageID string) error {
	return b.PublishEvent(agentName, userID, AgentEvent{
		AgentName: agentName,
		UserID:    userID,
		EventType: "json_message",
		Sender:    sender,
		Content:   content,
		MessageID: messageID,
	})
}

// PublishStatus publishes a status event (processing, completed, error).
// Uses "json_message_status" event type to match the React UI's expected SSE format.
// The status value is sent in the Metadata field as {"status": value} so the React UI
// can read it as data.status (the UI reads data.status, not data.content).
func (b *EventBridge) PublishStatus(agentName, userID, status string) error {
	statusJSON, err := json.Marshal(map[string]string{
		"status":    status,
		"timestamp": time.Now().Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("marshaling status JSON: %w", err)
	}
	return b.PublishEvent(agentName, userID, AgentEvent{
		AgentName: agentName,
		UserID:    userID,
		EventType: "json_message_status",
		Metadata:  string(statusJSON),
	})
}

// SubscribeEvents subscribes to agent events for a specific agent+user.
func (b *EventBridge) SubscribeEvents(agentName, userID string, handler func(AgentEvent)) (messaging.Subscription, error) {
	subject := messaging.SubjectAgentEvents(agentName, userID)
	return messaging.SubscribeJSON(b.bus, subject, handler)
}

// PublishStreamEvent broadcasts a stream event (reasoning, content, tool_call, done).
// These are forwarded as "stream_event" SSE events matching the React UI's expected format.
func (b *EventBridge) PublishStreamEvent(agentName, userID string, data map[string]any) error {
	return b.PublishEvent(agentName, userID, AgentEvent{
		AgentName: agentName,
		UserID:    userID,
		EventType: "stream_event",
		Metadata:  dbutil.MarshalJSON(data),
	})
}

// CancelExecution stops one agent execution wherever in the deployment it is
// running, and reports which of three different things happened.
//
// The three, because a caller that cannot tell them apart is the defect this
// family has been held back for:
//
//   - nil means the execution was cancelled. Either it was running in THIS
//     process, or an agent worker answered that it had cancelled it.
//   - nodes.ErrAgentCancelUndelivered means the cancel may have reached nobody:
//     an agent worker that might be running it could not be reached. It is not
//     a refusal and it is not a missing task.
//   - nodes.ErrAgentRunNotOnAnyWorker means every agent worker this deployment
//     could reach answered that it is not running that execution.
//
// The local registry is tried FIRST and short-circuits. A message id names
// exactly one execution, so a local hit is this process's own answer about it,
// and there is nothing a worker could add.
func (b *EventBridge) CancelExecution(ctx context.Context, agentName, userID, messageID string) error {
	if b.cancelRegistry.Cancel(messageID) {
		xlog.Info("Cancelled agent execution locally", "agent", agentName, "user", userID, "messageID", messageID)
		return nil
	}

	if b.workers == nil {
		// Not a cancel that was refused and not a task that does not exist:
		// this process has nowhere to send the cancel, which is a fact about
		// its own wiring and says nothing about the run.
		return fmt.Errorf("cancelling agent %q for user %q: this process holds no way to reach an agent worker, so the cancel of message %q was sent nowhere",
			agentName, userID, messageID)
	}

	return b.workers.CancelAgentRun(ctx, messaging.AgentCancelRequest{
		AgentName: agentName,
		UserID:    userID,
		MessageID: messageID,
	})
}

// CancelLocalExecution cancels an execution running in THIS process and reports
// whether it found one.
//
// It is what an agent worker's cancel control verb applies, and the bool is
// that worker's whole answer: true is "I cancelled it", false is "I am not
// running it". False is deliberately not an error, because it is not one: a
// deployment fans a cancel out to every worker it can reach and all but one of
// them are expected to say no.
func (b *EventBridge) CancelLocalExecution(messageID string) bool {
	if b == nil || messageID == "" {
		return false
	}
	return b.cancelRegistry.Cancel(messageID)
}

// RegisterCancel registers a cancel function for a running agent execution.
func (b *EventBridge) RegisterCancel(key string, cancel context.CancelFunc) {
	b.cancelRegistry.Register(key, cancel)
}

// DeregisterCancel removes a cancel function from the registry.
func (b *EventBridge) DeregisterCancel(key string) {
	b.cancelRegistry.Deregister(key)
}

// StartObservablePersister subscribes to every agent's events and persists the
// observable_update ones to the database. This runs on the frontend, to capture
// observables published by workers, which have no database access.
//
// The subscription lives for the life of this bridge and is one per replica, not
// one per request.
func (b *EventBridge) StartObservablePersister() error {
	if b.store == nil {
		return fmt.Errorf("no store available for observable persistence")
	}
	// The filter is the constant next to the builder it has to match. It used
	// to be a literal here, four tokens spelled by hand three files from
	// SubjectAgentEvents, and a filter one token short of its subject matches
	// nothing at all with no error anywhere.
	sub, err := messaging.SubscribeJSON(b.bus, messaging.SubjectAgentEventsWildcard, func(evt AgentEvent) {
		if evt.EventType != "observable_update" {
			return
		}
		// Skip events we published ourselves (already persisted locally in PersistObservable)
		if evt.SourceInstance == b.instanceID {
			return
		}
		if evt.Metadata == "" {
			return
		}
		// Use the record ID from the event to ensure idempotency — if the same
		// observable is somehow delivered twice, the primary key prevents duplicates.
		recordID := evt.MessageID
		if recordID == "" {
			recordID = uuid.New().String()
		}
		if err := b.store.AppendObservable(&AgentObservableRecord{
			ID:          recordID,
			AgentName:   AgentKey(evt.UserID, evt.AgentName),
			EventType:   evt.EventSubType,
			PayloadJSON: evt.Metadata,
			CreatedAt:   time.Now(),
		}); err != nil {
			// Primary key conflict is expected for duplicate events — ignore silently
			xlog.Debug("Observable persist skipped (likely duplicate)", "id", recordID, "agent", evt.AgentName, "error", err)
		}
	})
	if err != nil {
		return err
	}
	b.obsPersisterSub = sub
	return nil
}

// HandleSSE bridges an agent's event broadcasts to SSE for one agent and user.
func (b *EventBridge) HandleSSE(c echo.Context, agentName, userID string) error {
	if agentName == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "agent name required"})
	}
	return b.handleSSEInternal(c, agentName, userID)
}

// SSEHandler returns an Echo handler that bridges an agent's event broadcasts
// to SSE. This is the distributed version of the SSE endpoint.
func (b *EventBridge) SSEHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		agentName := c.Param("name")
		userID := c.QueryParam("user_id")
		if agentName == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "agent name required"})
		}
		return b.handleSSEInternal(c, agentName, userID)
	}
}

func (b *EventBridge) handleSSEInternal(c echo.Context, agentName, userID string) error {
	xlog.Debug("SSE connection opened (distributed)", "agent", agentName, "user", userID)

	// Check flusher support before writing any headers
	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported")
	}

	// Set SSE headers
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")
	c.Response().WriteHeader(http.StatusOK)

	var closed atomic.Bool
	var mu sync.Mutex
	writeSSE := func(event, data string) {
		if closed.Load() {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(c.Response(), "event: %s\ndata: %s\n\n", event, data)
		flusher.Flush()
	}

	sendEvent := func(event string, data any) {
		jsonData, err := json.Marshal(data)
		if err != nil {
			return
		}
		writeSSE(event, string(jsonData))
	}

	// Conversation history is managed client-side (browser localStorage).
	// No server-side replay needed.

	// Subscribe to live events
	sub, err := b.SubscribeEvents(agentName, userID, func(evt AgentEvent) {
		switch evt.EventType {
		case "json_message_status":
			// Send the metadata JSON directly — React UI expects {status, timestamp}
			if evt.Metadata != "" {
				writeSSE(evt.EventType, evt.Metadata)
			}
		case "stream_event", "observable_update":
			// Send the metadata JSON directly — React UI expects {type, content, ...}
			if evt.Metadata != "" {
				writeSSE(evt.EventType, evt.Metadata)
			}
		default:
			sendEvent(evt.EventType, evt)
		}
	})
	if err != nil {
		xlog.Error("Failed to subscribe to agent events", "agent", agentName, "user", userID, "error", err)
		writeSSE("json_error", `{"error":"failed to subscribe to agent events"}`)
		return nil
	}
	// Deferred, not called on the way out. This subscription is opened and
	// closed PER HTTP REQUEST, and on the PostgreSQL carrier only the first
	// subscriber of a channel issues a LISTEN while every later one registers
	// an in-process filter, so a return that skipped this would leave a replica
	// running one extra closure per notification for every stream it has ever
	// served, and nothing in the tree would fail.
	defer func() {
		closed.Store(true)
		if uerr := sub.Unsubscribe(); uerr != nil {
			xlog.Warn("Failed to close an agent event subscription", "agent", agentName, "user", userID, "error", uerr)
		}
	}()

	// Wait for client disconnect
	<-c.Request().Context().Done()
	return nil
}
