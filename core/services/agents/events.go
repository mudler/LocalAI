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

// AgentCancelEvent is the broadcast payload for cancelling agent execution.
type AgentCancelEvent struct {
	AgentName string `json:"agent_name"`
	UserID    string `json:"user_id"`
	MessageID string `json:"message_id,omitempty"`
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

	// cancelBus is where agent.<name>.cancel is published and heard, and it is
	// a SEPARATE field from bus because in this deployment the two ends of that
	// family cannot be on the same carrier yet.
	//
	// Every other family here has both ends on a frontend replica, so both move
	// to the broadcast carrier together. This one does not: its only subscriber
	// is the agent WORKER (core/cli/agent_worker.go), the cancel has to reach
	// the worker actually running the execution, and a worker has no database
	// and so cannot join the PostgreSQL carrier at all. Publishing a cancel
	// where no worker is listening would report a cancel that reached nobody as
	// a cancel the execution declined, which is the one thing this whole
	// programme may not do.
	//
	// So it is named, and it is separate, and it stays on the carrier the
	// worker reads until a cancel rides the worker's tunnel instead. Folding it
	// back into bus is not a simplification: it is silent loss of every cancel
	// for every worker-run agent.
	cancelBus messaging.Broadcaster
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
//
// Cancels go on that same carrier unless WithCancelCarrier says otherwise,
// which is right for the agent worker, where there is only one carrier to be
// on, and wrong for a frontend replica, which reads fan-out from PostgreSQL and
// has to reach workers that cannot.
func NewEventBridge(bus messaging.Broadcaster, store *AgentStore, instanceID string) *EventBridge {
	return &EventBridge{
		bus:            bus,
		cancelBus:      bus,
		pub:            bus,
		store:          store,
		instanceID:     instanceID,
		cancelRegistry: &messaging.CancelRegistry{},
	}
}

// WithCancelCarrier puts agent.<name>.cancel on carrier instead of on the
// fan-out bus, and returns the receiver so it can be written as one expression
// with the constructor.
//
// A frontend replica needs it and an agent worker does not. The cancel has to
// reach the worker running the execution; a worker has no database and cannot
// join the PostgreSQL carrier; so a frontend that published its cancels there
// would publish them where no worker listens, and a cancel that reached nobody
// is not a cancel that was refused.
//
// A nil carrier leaves the bridge on the fan-out bus rather than on nothing,
// because a bridge that publishes cancels nowhere is the failure this exists to
// prevent.
func (b *EventBridge) WithCancelCarrier(carrier messaging.Broadcaster) *EventBridge {
	if b == nil || carrier == nil {
		return b
	}
	b.cancelBus = carrier
	return b
}

// WithPublisher returns a view of this bridge whose events go to pub.
//
// Everything else is SHARED with the receiver, the cancel registry above all: a
// cancel arriving on the bus must reach an execution that is publishing onto a
// stream, and a bridge that copied the registry would register the cancel where
// nothing looks for it.
//
// A nil pub returns the receiver unchanged rather than a bridge that publishes
// nowhere, because a handler that was given no writer still has the bus.
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

// CancelExecution publishes a cancel event and also checks the local registry.
func (b *EventBridge) CancelExecution(agentName, userID, messageID string) error {
	// Try local cancel first
	if b.cancelRegistry.Cancel(messageID) {
		xlog.Info("Cancelled agent execution locally", "agent", agentName, "user", userID, "messageID", messageID)
	}

	// Broadcast so the replica that actually holds the execution can act on it.
	//
	// The error says whether the request was PUBLISHED and nothing more. This
	// carrier is at-most-once with no replay, so a cancel that reached nobody
	// and a cancel an execution declined are different facts that cannot be
	// told apart from here, and neither may be reported as the other.
	return b.cancelBus.Publish(messaging.SubjectAgentCancel(agentName), AgentCancelEvent{
		AgentName: agentName,
		UserID:    userID,
		MessageID: messageID,
	})
}

// RegisterCancel registers a cancel function for a running agent execution.
func (b *EventBridge) RegisterCancel(key string, cancel context.CancelFunc) {
	b.cancelRegistry.Register(key, cancel)
}

// DeregisterCancel removes a cancel function from the registry.
func (b *EventBridge) DeregisterCancel(key string) {
	b.cancelRegistry.Deregister(key)
}

// StartCancelListener subscribes to the cancel broadcasts every replica sees.
func (b *EventBridge) StartCancelListener() (messaging.Subscription, error) {
	return messaging.SubscribeJSON(b.cancelBus, messaging.SubjectAgentCancelWildcard, func(evt AgentCancelEvent) {
		if evt.MessageID != "" {
			if b.cancelRegistry.Cancel(evt.MessageID) {
				xlog.Info("Cancelled an agent execution on this replica after a broadcast cancel", "agent", evt.AgentName, "user", evt.UserID, "messageID", evt.MessageID)
			}
		}
	})
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
