package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/concurrency"

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

// AgentChatEvent is the payload of an agent-run claim, and the request body of
// the agent execution control verb that carries it to a worker.
type AgentChatEvent struct {
	AgentName string `json:"agent_name"`
	UserID    string `json:"user_id"`
	Message   string `json:"message"`
	MessageID string `json:"message_id"`
	Role      string `json:"role,omitempty"` // "user" or "system" (for periodic runs)

	// Enriched payload: set by the frontend/scheduler so that the worker,
	// which has no database, needs no database access.
	Config *AgentConfig `json:"config,omitempty"` // full agent configuration
	Skills []SkillInfo  `json:"skills,omitempty"` // resolved per-user skills
}

// Dispatcher routes agent chat requests to the executor.
// The standalone implementation is LocalDispatcher (direct goroutine); in
// distributed mode the frontend writes a claim row instead (see
// agentpool.dispatchChat) and a worker runs it through WorkerExecutor.
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
	ctx      context.Context
	cancelMu sync.Mutex
	cancels  map[string]context.CancelFunc
}

// SSEWriterPool provides SSE writers for agents.
type SSEWriterPool interface {
	GetWriter(agentKey string) SSEWriter
}

// NewLocalDispatcher creates a dispatcher that executes locally.
func NewLocalDispatcher(ctx context.Context, apiURL, apiKey string, configs ConfigProvider, ssePool SSEWriterPool) *LocalDispatcher {
	return &LocalDispatcher{
		apiURL:  apiURL,
		apiKey:  apiKey,
		configs: configs,
		ssePool: ssePool,
		ctx:     ctx,
		cancels: make(map[string]context.CancelFunc),
	}
}

func (d *LocalDispatcher) Start(_ context.Context) error {
	return nil // nothing to start for local mode
}

// Cancel cancels a running agent chat by message ID.
func (d *LocalDispatcher) Cancel(messageID string) {
	d.cancelMu.Lock()
	cancel, ok := d.cancels[messageID]
	if ok {
		delete(d.cancels, messageID)
	}
	d.cancelMu.Unlock()
	if ok {
		cancel()
	}
}

func (d *LocalDispatcher) Dispatch(userID, agentName, message string) (string, error) {
	messageID := uuid.New().String()

	cfg, err := d.configs.GetAgentConfig(userID, agentName)
	if err != nil {
		return "", fmt.Errorf("agent config not found: %w", err)
	}

	key := AgentKey(userID, agentName)
	writer := d.ssePool.GetWriter(key)

	// Execute in background goroutine
	ctx, cancel := context.WithCancel(d.ctx)
	d.cancelMu.Lock()
	d.cancels[messageID] = cancel
	d.cancelMu.Unlock()

	concurrency.SafeGo(func() {
		defer func() {
			d.cancelMu.Lock()
			delete(d.cancels, messageID)
			d.cancelMu.Unlock()
		}()
		defer cancel()

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
	})

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
			if isInternalCogitoTool(ev.ToolName) {
				return
			}
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
		OnToolResult: func(name, result string) {
			if writer != nil {
				writer.SendEvent("stream_event", map[string]any{
					"type":        "tool_result",
					"tool_name":   name,
					"tool_result": result,
					"timestamp":   time.Now().Format(time.RFC3339),
				})
			}
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
					"sender":     sender,
					"content":    content,
					"message_id": msgID,
					"timestamp":  time.Now().UnixMilli(),
				})
			}
		},
	}
}

// --- Worker-side agent executor (distributed) ---

// WorkerExecutor runs agent chats on an agent worker.
//
// It used to be a NATS queue-group subscriber, which is where the name
// NATSDispatcher came from and why it had a subject and a queue. It has
// neither now: a queue group only ever SELECTED one consumer, the frontend
// makes that selection itself (nodes.AgentSelector), and what reaches this
// worker is a streaming control RPC carrying one claim. So this type no longer
// dispatches anything; it executes what it is handed and writes everything it
// produces onto the response body of that RPC.
type WorkerExecutor struct {
	eventBridge *EventBridge
	configs     ConfigProvider
	apiURL      string
	apiKey      string
}

// NewWorkerExecutor creates the executor an agent worker serves
// workerctl.PathAgentExecute with.
func NewWorkerExecutor(bridge *EventBridge, configs ConfigProvider, apiURL, apiKey string) *WorkerExecutor {
	return &WorkerExecutor{
		eventBridge: bridge,
		configs:     configs,
		apiURL:      apiURL,
		apiKey:      apiKey,
	}
}

// Execute runs one agent chat and answers the control verb.
//
// pub is where every event this run produces goes: the agent's stream events,
// its status changes, its tool results and its messages. On the control plane
// that is the response body the claiming replica is already reading, so a
// caller cannot miss an event by having subscribed too late, and the terminal
// answer below cannot be published to nobody.
//
// A returned ERROR means this worker could not serve the verb at all, and the
// claiming replica must not read it as a verdict about the work: it releases
// the claim. Everything this worker LEARNED by running the agent, including a
// failure, comes back as the reply below.
func (d *WorkerExecutor) Execute(ctx context.Context, raw json.RawMessage, pub messaging.Publisher) (json.RawMessage, error) {
	var evt AgentChatEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		return nil, fmt.Errorf("reading an agent execution request: %w", err)
	}
	bridge := d.eventBridge.WithPublisher(pub)
	status, errMsg := d.handleJob(ctx, evt, bridge)
	// The reply names no job: an agent run has no job row, and its output has
	// already travelled as events. What it carries is the fact that this worker
	// ran it to a conclusion, which is what lets the claim be completed rather
	// than retried on another worker.
	return json.Marshal(map[string]string{"status": status, "error": errMsg})
}

// Cancel answers workerctl.PathAgentCancel on an agent worker.
//
// The reply is this worker's OWN ANSWER and travels as bytes on a 200:
// cancelled true when this process was running the named execution and its
// context has now been cancelled, false when it was not. False says nothing
// about any other worker and nothing about whether the run exists, and the
// frontend that fans the cancel out is the only thing that may assemble those
// answers into a verdict.
//
// A body this worker cannot read is the one thing here that is NOT an answer:
// it has learned nothing about any execution, so it is returned as an error,
// becomes a non-2xx over the tunnel, and is counted by the caller as a cancel
// it could not deliver rather than as one that found nothing.
func (d *WorkerExecutor) Cancel(_ context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var req messaging.AgentCancelRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("reading an agent cancel request: %w", err)
	}
	cancelled := d.eventBridge.CancelLocalExecution(req.MessageID)
	if cancelled {
		xlog.Info("Cancelled an agent execution on this worker after a control cancel",
			"agent", req.AgentName, "user", req.UserID, "messageID", req.MessageID)
	}
	return json.Marshal(messaging.AgentCancelReply{Cancelled: cancelled})
}

func (d *WorkerExecutor) handleJob(ctx context.Context, evt AgentChatEvent, bridge *EventBridge) (status, errMsg string) {
	xlog.Info("Processing agent chat job", "agent", evt.AgentName, "user", evt.UserID)

	// Prefer config from the enriched payload (no DB needed).
	// Fall back to ConfigProvider for backward compat / local mode.
	cfg := evt.Config
	if cfg == nil && d.configs != nil {
		var err error
		cfg, err = d.configs.GetAgentConfig(evt.UserID, evt.AgentName)
		if err != nil {
			xlog.Error("Failed to load agent config", "agent", evt.AgentName, "error", err)
			dropped(bridge.PublishStatus(evt.AgentName, evt.UserID, "error: agent config not found"), "status", evt.AgentName)
			return "failed", "agent config not found"
		}
	}
	if cfg == nil {
		xlog.Error("No agent config available", "agent", evt.AgentName)
		dropped(bridge.PublishStatus(evt.AgentName, evt.UserID, "error: agent config not found"), "status", evt.AgentName)
		return "failed", "agent config not found"
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Register cancellation on the SHARED registry, which the cancel control
	// verb also reads: a cancel arrives on this worker's tunnel and has to
	// reach an execution that is publishing onto a control stream.
	bridge.RegisterCancel(evt.MessageID, cancel)
	defer bridge.DeregisterCancel(evt.MessageID)

	cb := d.buildCallbacks(evt, bridge)

	// Build execution options: skills come from the enriched payload (workers
	// have no database access).
	opts := ExecuteChatOpts{
		UserID:    evt.UserID,
		MessageID: evt.MessageID,
	}
	if len(evt.Skills) > 0 {
		opts.SkillProvider = &staticSkillProvider{skills: evt.Skills}
	}

	var execErr error

	if evt.Role == RoleSystem {
		// Background/autonomous run — use inner monologue template + permanent goal
		_, execErr = ExecuteBackgroundRun(ctx, d.apiURL, d.apiKey, cfg, cb, opts)
	} else {
		_, execErr = ExecuteChat(ctx, d.apiURL, d.apiKey, cfg, evt.Message, cb, opts)
	}

	if execErr != nil {
		xlog.Error("Distributed agent execution failed", "agent", evt.AgentName, "error", execErr)
		dropped(bridge.PublishStatus(evt.AgentName, evt.UserID, "error"), "status", evt.AgentName)
		dropped(bridge.PublishMessage(evt.AgentName, evt.UserID, RoleAgent,
			fmt.Sprintf("Agent execution failed: %v", execErr), evt.MessageID+"-error"), "message", evt.AgentName)
		// An agent that RAN and failed is this worker's own answer, not a
		// failure to serve the verb: the claiming replica must complete the
		// claim rather than offer the same run to another worker.
		return "failed", execErr.Error()
	}

	// The response itself has already travelled as events.
	return "completed", ""
}

// dropped logs an agent event that could not be published, and never returns it.
//
// An event is a NOTIFICATION about a run. A failure to publish one says nothing
// about whether the run succeeded, and turning it into the verb's error would
// report a finished agent run as a failure the claiming replica must retry.
func dropped(err error, event, agentName string) {
	if err != nil {
		xlog.Debug("An agent event could not be published", "event", event, "agent", agentName, "error", err)
	}
}

// staticSkillProvider provides skills from an in-memory list (from the NATS payload).
type staticSkillProvider struct {
	skills []SkillInfo
}

func (p *staticSkillProvider) ListSkills() ([]SkillInfo, error) {
	return p.skills, nil
}

func (d *WorkerExecutor) buildCallbacks(evt AgentChatEvent, bridge *EventBridge) Callbacks {
	// Observable tracking: build LocalAGI-compatible observable records
	// from cogito callbacks so the UI can render them properly.
	//
	// IDs must be globally unique (not just per-job) because the UI's buildTree
	// uses them as map keys. We use a random base + counter so IDs are unique
	// across jobs while parent–child relationships still work within a job.
	idBase := rand.Int32N(1<<30) + 1 // random base, avoids collisions across jobs
	var obsIDCounter atomic.Int32
	var mu sync.Mutex
	var currentToolObs *coreTypes.Observable
	var reasoningBuf strings.Builder

	nextID := func() int32 {
		return idBase + obsIDCounter.Add(1)
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
			if bridge == nil {
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
				if isInternalCogitoTool(ev.ToolName) {
					return
				}
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
			dropped(bridge.PublishStreamEvent(evt.AgentName, evt.UserID, data), "stream", evt.AgentName)
		},
		OnReasoning: func(text string) {
			// Reasoning is buffered via OnStream
		},
		OnToolCall: func(name, args string) {
			// Tool calls tracked via OnStream
		},
		OnToolResult: func(name, result string) {
			// Emit tool_result stream event for real-time UI display
			if bridge != nil {
				dropped(bridge.PublishStreamEvent(evt.AgentName, evt.UserID, map[string]any{
					"type":        "tool_result",
					"tool_name":   name,
					"tool_result": result,
					"timestamp":   time.Now().Format(time.RFC3339),
				}), "tool_result", evt.AgentName)
			}
			// Persist tool result: complete the current tool observable
			mu.Lock()
			obs := currentToolObs
			currentToolObs = nil
			mu.Unlock()
			if obs != nil {
				obs.Completion = &coreTypes.Completion{
					ActionResult: result,
				}
				if bridge != nil {
					bridge.PersistObservable(evt.AgentName, evt.UserID, "tool_result", obs)
				}
			}
		},
		OnStatus: func(status string) {
			if bridge != nil {
				dropped(bridge.PublishStatus(evt.AgentName, evt.UserID, status), "status", evt.AgentName)
			}
		},
		OnMessage: func(sender, content, msgID string) {
			if bridge != nil {
				dropped(bridge.PublishMessage(evt.AgentName, evt.UserID, sender, content, msgID), "message", evt.AgentName)
			}

			// On agent response, persist the root observable with completion
			if sender == RoleAgent && bridge != nil {
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
				bridge.PersistObservable(evt.AgentName, evt.UserID, "chat", rootObs)
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
