package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/dbutil"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
)

// AgentEvent is the NATS message payload for agent SSE events.
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
	Timestamp      int64  `json:"timestamp"`
}

// AgentCancelEvent is the NATS message payload for cancelling agent execution.
type AgentCancelEvent struct {
	AgentName string `json:"agent_name"`
	UserID    string `json:"user_id"`
}

// EventBridge bridges agent events between NATS and SSE connections.
// It enables cross-instance SSE: user connects to Frontend 1, agent runs on Frontend 2.
type EventBridge struct {
	nats       *messaging.Client
	store      *AgentStore
	instanceID string

	// Cancel registry for running agent executions
	cancelRegistry messaging.CancelRegistry

	// Background NATS subscriptions owned by this bridge
	obsPersisterSub messaging.Subscription
}

// NewEventBridge creates a new EventBridge.
func NewEventBridge(nc *messaging.Client, store *AgentStore, instanceID string) *EventBridge {
	return &EventBridge{
		nats:       nc,
		store:      store,
		instanceID: instanceID,
	}
}

// PublishEvent publishes an agent event to NATS for SSE bridging.
func (b *EventBridge) PublishEvent(agentName, userID string, evt AgentEvent) error {
	evt.Timestamp = time.Now().UnixNano()
	subject := messaging.SubjectAgentEvents(agentName, userID)
	return b.nats.Publish(subject, evt)
}

// PersistObservable publishes an observable_update SSE event for real-time UI
// updates and, if a database store is available, writes the record to the DB.
// When the store is nil (e.g. on agent workers), the NATS event is still
// published so the frontend can persist it via StartObservablePersister.
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

	// Always publish NATS event — enables real-time SSE and remote persistence.
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

// PublishMessage publishes a chat message event via NATS for SSE bridging.
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
	return messaging.SubscribeJSON(b.nats, subject, handler)
}

// PublishStreamEvent publishes a stream event (reasoning, content, tool_call, done) via NATS.
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
func (b *EventBridge) CancelExecution(agentName, userID string) error {
	// Try local cancel first
	key := agentName + ":" + userID
	if b.cancelRegistry.Cancel(key) {
		xlog.Info("Cancelled agent execution locally", "agent", agentName, "user", userID)
	}

	// Also publish via NATS for other instances
	return b.nats.Publish(messaging.SubjectAgentCancel(agentName), AgentCancelEvent{
		AgentName: agentName,
		UserID:    userID,
	})
}

// RegisterCancel registers a cancel function for a running agent execution.
func (b *EventBridge) RegisterCancel(agentName, userID string, cancel context.CancelFunc) {
	key := agentName + ":" + userID
	b.cancelRegistry.Register(key, cancel)
}

// DeregisterCancel removes a cancel function from the registry.
func (b *EventBridge) DeregisterCancel(agentName, userID string) {
	key := agentName + ":" + userID
	b.cancelRegistry.Deregister(key)
}

// StartCancelListener subscribes to NATS cancel events (broadcast to all instances).
func (b *EventBridge) StartCancelListener() (messaging.Subscription, error) {
	return messaging.SubscribeJSON(b.nats, messaging.SubjectAgentCancelWildcard, func(evt AgentCancelEvent) {
		key := evt.AgentName + ":" + evt.UserID
		if b.cancelRegistry.Cancel(key) {
			xlog.Info("Cancelled agent via NATS", "agent", evt.AgentName, "user", evt.UserID)
		}
	})
}

// StartObservablePersister subscribes to all agent events via NATS and persists
// observable_update events to the database. This runs on the frontend to capture
// observables published by workers (which have no database access).
// The subscription is stored on the EventBridge and cleaned up when the NATS
// connection closes.
func (b *EventBridge) StartObservablePersister() error {
	if b.store == nil {
		return fmt.Errorf("no store available for observable persistence")
	}
	// Subscribe to all agent events using wildcard: agent.*.events.*
	sub, err := messaging.SubscribeJSON(b.nats, "agent.*.events.*", func(evt AgentEvent) {
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

// HandleSSE bridges NATS agent events to SSE for a specific agent and user.
func (b *EventBridge) HandleSSE(c echo.Context, agentName, userID string) error {
	if agentName == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "agent name required"})
	}
	return b.handleSSEInternal(c, agentName, userID)
}

// SSEHandler returns an Echo handler that bridges NATS agent events to SSE.
// This is the distributed version of the SSE endpoint.
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

	var mu sync.Mutex
	writeSSE := func(event, data string) {
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
	defer sub.Unsubscribe()

	// Wait for client disconnect
	<-c.Request().Context().Done()
	return nil
}
