package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coreTypes "github.com/mudler/LocalAGI/core/types"
	"github.com/mudler/cogito"
	"github.com/mudler/xlog"
	"github.com/sashabaranov/go-openai"
)

const (
	RoleUser   = "user"
	RoleSystem = "system"
	RoleAgent  = "agent"
)

// AgentChatEvent is the NATS message payload for agent chat jobs.
type AgentChatEvent struct {
	AgentName string `json:"agent_name"`
	UserID    string `json:"user_id"`
	Message   string `json:"message"`
	MessageID string `json:"message_id"`
	Role      string `json:"role,omitempty"` // "user" or "system" (for periodic runs)

	// Enriched payload: set by the frontend/scheduler so that the worker
	// does not need direct database access.
	Config *AgentConfig `json:"config,omitempty"` // full agent configuration
	Skills []SkillInfo  `json:"skills,omitempty"` // resolved per-user skills
}

// Dispatcher routes agent chat requests to the executor.
// Two implementations: LocalDispatcher (direct goroutine) and NATSDispatcher (queue).
type Dispatcher interface {
	// Dispatch sends a chat message to an agent and returns immediately.
	// The response is delivered asynchronously via the configured event delivery mechanism.
	Dispatch(userID, agentName, message string) (messageID string, err error)

	// Start initializes the dispatcher (e.g., subscribes to NATS queue).
	Start(ctx context.Context) error
}

// ConfigProvider loads agent configs. Implemented by both file-based and DB-backed stores.
type ConfigProvider interface {
	GetAgentConfig(userID, name string) (*AgentConfig, error)
}

// --- Local Dispatcher (non-distributed) ---

// SSEWriter sends SSE events to a connected client.
type SSEWriter interface {
	SendEvent(event string, data any)
}

// LocalDispatcher executes agent chats directly in a goroutine.
// Events are delivered to the caller's SSE writer.
type LocalDispatcher struct {
	apiURL   string
	apiKey   string
	configs  ConfigProvider
	ssePool  SSEWriterPool // maps agentKey → SSEWriter
}

// SSEWriterPool provides SSE writers for agents.
type SSEWriterPool interface {
	GetWriter(agentKey string) SSEWriter
}

// NewLocalDispatcher creates a dispatcher that executes locally.
func NewLocalDispatcher(apiURL, apiKey string, configs ConfigProvider, ssePool SSEWriterPool) *LocalDispatcher {
	return &LocalDispatcher{
		apiURL:  apiURL,
		apiKey:  apiKey,
		configs: configs,
		ssePool: ssePool,
	}
}

func (d *LocalDispatcher) Start(_ context.Context) error {
	return nil // nothing to start for local mode
}

func (d *LocalDispatcher) Dispatch(userID, agentName, message string) (string, error) {
	messageID := fmt.Sprintf("%d", time.Now().UnixNano())

	cfg, err := d.configs.GetAgentConfig(userID, agentName)
	if err != nil {
		return "", fmt.Errorf("agent config not found: %w", err)
	}

	key := AgentKey(userID, agentName)
	writer := d.ssePool.GetWriter(key)

	// Execute in background goroutine
	go func() {
		ctx := context.Background()
		cb := d.buildLocalCallbacks(writer, messageID)

		// Send user message immediately
		if cb.OnMessage != nil {
			cb.OnMessage(RoleUser, message, messageID+"-user")
		}
		if cb.OnStatus != nil {
			cb.OnStatus("processing")
		}

		_, execErr := ExecuteChat(ctx, d.apiURL, d.apiKey, cfg, message, cb)
		if execErr != nil {
			xlog.Error("Local agent execution failed", "agent", agentName, "error", execErr)
		}
	}()

	return messageID, nil
}

func (d *LocalDispatcher) buildLocalCallbacks(writer SSEWriter, messageID string) Callbacks {
	streamToSSE := func(ev cogito.StreamEvent) {
		if writer == nil {
			return
		}
		data := map[string]any{"timestamp": time.Now().Format(time.RFC3339)}
		switch ev.Type {
		case cogito.StreamEventReasoning:
			data["type"] = "reasoning"
			data["content"] = ev.Content
		case cogito.StreamEventContent:
			data["type"] = "content"
			data["content"] = ev.Content
		case cogito.StreamEventToolCall:
			data["type"] = "tool_call"
			data["tool_name"] = ev.ToolName
			data["tool_args"] = ev.ToolArgs
		case cogito.StreamEventDone:
			data["type"] = "done"
		default:
			return
		}
		writer.SendEvent("stream_event", data)
	}

	return Callbacks{
		OnStream: streamToSSE,
		OnReasoning: func(text string) {
			// Already forwarded via OnStream
		},
		OnToolCall: func(name, args string) {
			// Already forwarded via OnStream
		},
		OnStatus: func(status string) {
			if writer != nil {
				writer.SendEvent("json_message_status", map[string]string{
					"status":    status,
					"timestamp": time.Now().Format(time.RFC3339),
				})
			}
		},
		OnMessage: func(sender, content, msgID string) {
			if writer != nil {
				writer.SendEvent("json_message", map[string]any{
					"sender":    sender,
					"content":   content,
					"message_id": msgID,
					"timestamp": time.Now().UnixMilli(),
				})
			}
		},
	}
}

// --- NATS Dispatcher (distributed) ---

// NATSPublisher publishes messages to NATS and subscribes to queues.
type NATSPublisher interface {
	Publish(subject string, data any) error
}

// NATSSubscriber subscribes to NATS queue groups.
type NATSSubscriber interface {
	QueueSubscribe(subject, queue string, handler func(data []byte)) (NATSSub, error)
}

// NATSSub is a subscription that can be unsubscribed.
type NATSSub interface {
	Unsubscribe() error
}

// NATSClient combines publishing and subscribing for NATS.
type NATSClient interface {
	NATSPublisher
	NATSSubscriber
}

// NATSDispatcher dispatches agent chats via NATS queue group.
type NATSDispatcher struct {
	nats        NATSClient
	eventBridge *EventBridge
	configs     ConfigProvider
	apiURL string
	apiKey      string
	subject     string
	queue       string
}

// NewNATSDispatcher creates a dispatcher that uses NATS for distribution.
func NewNATSDispatcher(nats NATSClient, bridge *EventBridge, configs ConfigProvider, apiURL, apiKey, subject, queue string) *NATSDispatcher {
	return &NATSDispatcher{
		nats:        nats,
		eventBridge: bridge,
		configs:     configs,
		apiURL:      apiURL,
		apiKey:      apiKey,
		subject:     subject,
		queue:       queue,
	}
}

func (d *NATSDispatcher) Start(ctx context.Context) error {
	_, err := d.nats.QueueSubscribe(d.subject, d.queue, func(data []byte) {
		var evt AgentChatEvent
		if err := json.Unmarshal(data, &evt); err != nil {
			xlog.Error("Failed to unmarshal agent chat event", "error", err)
			return
		}
		d.handleJob(ctx, evt)
	})
	if err != nil {
		return fmt.Errorf("subscribing to %s: %w", d.subject, err)
	}
	xlog.Info("NATS agent dispatcher started", "subject", d.subject, "queue", d.queue)
	return nil
}

func (d *NATSDispatcher) Dispatch(userID, agentName, message string) (string, error) {
	messageID := fmt.Sprintf("%d", time.Now().UnixNano())

	// Send user message to SSE immediately
	if d.eventBridge != nil {
		d.eventBridge.PublishMessage(agentName, userID, RoleUser, message, messageID+"-user")
		d.eventBridge.PublishStatus(agentName, userID, "processing")
	}

	evt := AgentChatEvent{
		AgentName: agentName,
		UserID:    userID,
		Message:   message,
		MessageID: messageID,
		Role:      RoleUser,
	}
	if err := d.nats.Publish(d.subject, evt); err != nil {
		return "", fmt.Errorf("failed to dispatch agent chat: %w", err)
	}
	return messageID, nil
}

func (d *NATSDispatcher) handleJob(ctx context.Context, evt AgentChatEvent) {
	xlog.Info("Processing agent chat job", "agent", evt.AgentName, "user", evt.UserID)

	// Prefer config from the enriched payload (no DB needed).
	// Fall back to ConfigProvider for backward compat / local mode.
	cfg := evt.Config
	if cfg == nil && d.configs != nil {
		var err error
		cfg, err = d.configs.GetAgentConfig(evt.UserID, evt.AgentName)
		if err != nil {
			xlog.Error("Failed to load agent config", "agent", evt.AgentName, "error", err)
			if d.eventBridge != nil {
				d.eventBridge.PublishStatus(evt.AgentName, evt.UserID, "error: agent config not found")
			}
			return
		}
	}
	if cfg == nil {
		xlog.Error("No agent config available", "agent", evt.AgentName)
		if d.eventBridge != nil {
			d.eventBridge.PublishStatus(evt.AgentName, evt.UserID, "error: agent config not found")
		}
		return
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Register cancellation
	if d.eventBridge != nil {
		d.eventBridge.RegisterCancel(evt.AgentName, evt.UserID, cancel)
		defer d.eventBridge.DeregisterCancel(evt.AgentName, evt.UserID)
	}

	cb := d.buildNATSCallbacks(evt)

	// Build execution options: skills come from the enriched NATS payload
	// (workers have no database access).
	opts := ExecuteChatOpts{
		UserID: evt.UserID,
	}
	if len(evt.Skills) > 0 {
		opts.SkillProvider = &staticSkillProvider{skills: evt.Skills}
	}

	var response string
	var execErr error

	if evt.Role == RoleSystem {
		// Background/autonomous run — use inner monologue template + permanent goal
		response, execErr = ExecuteBackgroundRun(ctx, d.apiURL, d.apiKey, cfg, cb, opts)
	} else {
		response, execErr = ExecuteChat(ctx, d.apiURL, d.apiKey, cfg, evt.Message, cb, opts)
	}

	if execErr != nil {
		xlog.Error("Distributed agent execution failed", "agent", evt.AgentName, "error", execErr)
		return
	}

	_ = response // already published via callbacks
}

// staticSkillProvider provides skills from an in-memory list (from the NATS payload).
type staticSkillProvider struct {
	skills []SkillInfo
}

func (p *staticSkillProvider) ListSkills() ([]SkillInfo, error) {
	return p.skills, nil
}

func (d *NATSDispatcher) buildNATSCallbacks(evt AgentChatEvent) Callbacks {
	// Observable tracking: build LocalAGI-compatible observable records
	// from cogito callbacks so the UI can render them properly.
	var obsIDCounter atomic.Int32
	var mu sync.Mutex
	var currentToolObs *coreTypes.Observable
	var reasoningBuf strings.Builder

	nextID := func() int32 {
		return obsIDCounter.Add(1)
	}

	// Root observable for this chat job
	rootID := nextID()
	rootObs := &coreTypes.Observable{
		ID:    rootID,
		Agent: evt.AgentName,
		Name:  "chat",
		Icon:  "comment",
		Creation: &coreTypes.Creation{
			ChatCompletionMessage: &openai.ChatCompletionMessage{
				Role:    RoleUser,
				Content: evt.Message,
			},
		},
	}

	return Callbacks{
		OnStream: func(ev cogito.StreamEvent) {
			if d.eventBridge == nil {
				return
			}
			data := map[string]any{"timestamp": time.Now().Format(time.RFC3339)}
			switch ev.Type {
			case cogito.StreamEventReasoning:
				data["type"] = "reasoning"
				data["content"] = ev.Content
				mu.Lock()
			reasoningBuf.WriteString(ev.Content)
			mu.Unlock()
			case cogito.StreamEventContent:
				data["type"] = "content"
				data["content"] = ev.Content
			case cogito.StreamEventToolCall:
				data["type"] = "tool_call"
				data["tool_name"] = ev.ToolName
				data["tool_args"] = ev.ToolArgs

				// Create child observable for the tool call
				obs := &coreTypes.Observable{
					ID:       nextID(),
					ParentID: rootID,
					Agent:    evt.AgentName,
					Name:     "decision",
					Icon:     "brain",
					Creation: &coreTypes.Creation{
						FunctionDefinition: &openai.FunctionDefinition{Name: ev.ToolName},
						FunctionParams:     parseToolArgs(ev.ToolArgs),
					},
				}
				mu.Lock()
				currentToolObs = obs
				mu.Unlock()
			case cogito.StreamEventDone:
				data["type"] = "done"
			default:
				return
			}
			d.eventBridge.PublishStreamEvent(evt.AgentName, evt.UserID, data)
		},
		OnReasoning: func(text string) {
			// Reasoning is buffered via OnStream
		},
		OnToolCall: func(name, args string) {
			// Tool calls tracked via OnStream
		},
		OnToolResult: func(name, result string) {
			// Persist tool result: complete the current tool observable
			mu.Lock()
			obs := currentToolObs
			currentToolObs = nil
			mu.Unlock()
			if obs != nil {
				obs.Completion = &coreTypes.Completion{
					ActionResult: result,
				}
				if d.eventBridge != nil {
					d.eventBridge.PersistObservable(evt.AgentName, evt.UserID, "tool_result", obs)
				}
			}
		},
		OnStatus: func(status string) {
			if d.eventBridge != nil {
				d.eventBridge.PublishStatus(evt.AgentName, evt.UserID, status)
			}
		},
		OnMessage: func(sender, content, msgID string) {
			if d.eventBridge != nil {
				d.eventBridge.PublishMessage(evt.AgentName, evt.UserID, sender, content, msgID)
			}

			// On agent response, persist the root observable with completion
			if sender == RoleAgent && d.eventBridge != nil {
				rootObs.Completion = &coreTypes.Completion{
					ActionResult: content,
				}
				mu.Lock()
			reasoning := reasoningBuf.String()
			mu.Unlock()
			if reasoning != "" {
					rootObs.Completion.ChatCompletionResponse = &openai.ChatCompletionResponse{
						Choices: []openai.ChatCompletionChoice{
							{Message: openai.ChatCompletionMessage{Content: content, ReasoningContent: reasoning}},
						},
					}
				}
				d.eventBridge.PersistObservable(evt.AgentName, evt.UserID, "chat", rootObs)
			}
		},
	}
}

// parseToolArgs attempts to parse a JSON string into ActionParams.
// Falls back to a map with a "raw" key if parsing fails.
func parseToolArgs(s string) coreTypes.ActionParams {
	var params coreTypes.ActionParams
	if err := json.Unmarshal([]byte(s), &params); err != nil {
		return coreTypes.ActionParams{"raw": s}
	}
	return params
}
