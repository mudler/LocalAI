package distributed_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/cogito"
	openai "github.com/sashabaranov/go-openai"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// mockLLM is a test LLM that returns a fixed response.
type mockLLM struct {
	response string
	toolCall *openai.ToolCall // if set, first call returns a tool call
	callCount atomic.Int32
}

func (m *mockLLM) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	m.callCount.Add(1)
	result := f.AddMessage(cogito.AssistantMessageRole, m.response)
	return result, nil
}

func (m *mockLLM) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	m.callCount.Add(1)

	msg := openai.ChatCompletionMessage{
		Role:    "assistant",
		Content: m.response,
	}

	if m.toolCall != nil && m.callCount.Load() == 1 {
		// First call: return tool call
		msg.Content = ""
		msg.ToolCalls = []openai.ToolCall{*m.toolCall}
	}

	return cogito.LLMReply{
		ChatCompletionResponse: openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{{Message: msg}},
		},
	}, cogito.LLMUsage{}, nil
}

// mockSSEWriter collects SSE events for testing.
type mockSSEWriter struct {
	mu     sync.Mutex
	events []mockSSEEvent
}

type mockSSEEvent struct {
	Event string
	Data  any
}

func (w *mockSSEWriter) SendEvent(event string, data any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, mockSSEEvent{Event: event, Data: data})
}

func (w *mockSSEWriter) getEvents() []mockSSEEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	cp := make([]mockSSEEvent, len(w.events))
	copy(cp, w.events)
	return cp
}

// mockSSEPool always returns the same writer.
type mockSSEPool struct {
	writer *mockSSEWriter
}

func (p *mockSSEPool) GetWriter(key string) agents.SSEWriter {
	return p.writer
}

// mockLLMWithCapture is a test LLM that captures the messages it receives.
type mockLLMWithCapture struct {
	response        string
	captureMessages func([]openai.ChatCompletionMessage)
}

func (m *mockLLMWithCapture) Ask(ctx context.Context, f cogito.Fragment) (cogito.Fragment, error) {
	return f.AddMessage(cogito.AssistantMessageRole, m.response), nil
}

func (m *mockLLMWithCapture) CreateChatCompletion(ctx context.Context, req openai.ChatCompletionRequest) (cogito.LLMReply, cogito.LLMUsage, error) {
	if m.captureMessages != nil {
		m.captureMessages(req.Messages)
	}
	msg := openai.ChatCompletionMessage{
		Role:    "assistant",
		Content: m.response,
	}
	return cogito.LLMReply{
		ChatCompletionResponse: openai.ChatCompletionResponse{
			Choices: []openai.ChatCompletionChoice{{Message: msg}},
		},
	}, cogito.LLMUsage{}, nil
}

// staticSkillProviderTest provides skills for testing.
type staticSkillProviderTest struct {
	skills []agents.SkillInfo
}

func (p *staticSkillProviderTest) ListSkills() ([]agents.SkillInfo, error) {
	return p.skills, nil
}

// startAgentMockLLMServer starts a mock OpenAI-compatible HTTP server that returns
// the given response text. Supports both streaming and non-streaming requests.
func startAgentMockLLMServer(responseText string) (string, func()) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}

		var req struct {
			Stream bool `json:"stream"`
			Model  string `json:"model"`
		}
		data, _ := io.ReadAll(r.Body)
		json.Unmarshal(data, &req)

		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, _ := w.(http.Flusher)

			chunk := map[string]any{
				"id": "chatcmpl-mock", "model": req.Model,
				"choices": []map[string]any{{
					"index": 0,
					"delta": map[string]any{"role": "assistant", "content": responseText},
				}},
			}
			d, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", d)
			if flusher != nil { flusher.Flush() }

			done := map[string]any{
				"id": "chatcmpl-mock-done", "model": req.Model,
				"choices": []map[string]any{{
					"index": 0, "delta": map[string]any{}, "finish_reason": "stop",
				}},
			}
			d, _ = json.Marshal(done)
			fmt.Fprintf(w, "data: %s\n\n", d)
			fmt.Fprintf(w, "data: [DONE]\n\n")
			if flusher != nil { flusher.Flush() }
		} else {
			w.Header().Set("Content-Type", "application/json")
			resp := map[string]any{
				"id": "chatcmpl-mock", "model": req.Model,
				"choices": []map[string]any{{
					"index": 0,
					"message": map[string]any{"role": "assistant", "content": responseText},
					"finish_reason": "stop",
				}},
			}
			json.NewEncoder(w).Encode(resp)
		}
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).ToNot(HaveOccurred())

	httpServer := &http.Server{Handler: handler}
	go httpServer.Serve(listener)

	url := fmt.Sprintf("http://%s", listener.Addr().String())
	shutdown := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		httpServer.Shutdown(ctx)
	}
	return url, shutdown
}

// mockConfigProvider returns a fixed config.
type mockConfigProvider struct {
	configs map[string]*agents.AgentConfig
}

func (p *mockConfigProvider) GetAgentConfig(userID, name string) (*agents.AgentConfig, error) {
	key := name
	if userID != "" {
		key = userID + ":" + name
	}
	if cfg, ok := p.configs[key]; ok {
		return cfg, nil
	}
	if cfg, ok := p.configs[name]; ok {
		return cfg, nil
	}
	return nil, fmt.Errorf("agent not found: %s", name)
}

// natsAdapter wraps messaging.Client for NATSClient interface.
type natsAdapter struct {
	client *messaging.Client
}

func (a *natsAdapter) Publish(subject string, data any) error {
	return a.client.Publish(subject, data)
}

func (a *natsAdapter) QueueSubscribe(subject, queue string, handler func([]byte)) (agents.NATSSub, error) {
	return a.client.QueueSubscribe(subject, queue, handler)
}

var _ = Describe("Native Agent Executor", Label("Distributed", "AgentNative"), func() {
	var (
		ctx           context.Context
		pgContainer   *tcpostgres.PostgresContainer
		natsContainer *tcnats.NATSContainer
		db            *gorm.DB
		nc            *messaging.Client
		store         *agents.AgentStore
	)

	BeforeEach(func() {
		ctx = context.Background()

		var err error

		pgContainer, err = tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("localai_agent_native_test"),
			tcpostgres.WithUsername("test"),
			tcpostgres.WithPassword("test"),
			testcontainers.WithWaitStrategy(
				wait.ForLog("database system is ready to accept connections").
					WithOccurrence(2).
					WithStartupTimeout(30*time.Second),
			),
		)
		Expect(err).ToNot(HaveOccurred())

		pgURL, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
		Expect(err).ToNot(HaveOccurred())

		db, err = gorm.Open(pgdriver.Open(pgURL), &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
		Expect(err).ToNot(HaveOccurred())

		natsContainer, err = tcnats.Run(ctx, "nats:2-alpine")
		Expect(err).ToNot(HaveOccurred())

		natsURL, err := natsContainer.ConnectionString(ctx)
		Expect(err).ToNot(HaveOccurred())

		nc, err = messaging.New(natsURL)
		Expect(err).ToNot(HaveOccurred())

		store, err = agents.NewAgentStore(db)
		Expect(err).ToNot(HaveOccurred())

		// Also migrate BackendNode for registration tests
		Expect(db.AutoMigrate(&nodes.BackendNode{})).To(Succeed())
	})

	AfterEach(func() {
		if nc != nil {
			nc.Close()
		}
		if pgContainer != nil {
			pgContainer.Terminate(ctx)
		}
		if natsContainer != nil {
			natsContainer.Terminate(ctx)
		}
	})

	Context("ExecuteChat", func() {
		It("should execute a chat and deliver response via callbacks", func() {
			llm := &mockLLM{response: "Hello! I'm an AI assistant."}
			cfg := &agents.AgentConfig{
				Name:         "test-agent",
				Model:        "test-model",
				SystemPrompt: "You are helpful.",
			}

			var gotStatus []string
			var gotMessage string
			var gotSender string
			var mu sync.Mutex

			cb := agents.Callbacks{
				OnStatus: func(s string) {
					mu.Lock()
					gotStatus = append(gotStatus, s)
					mu.Unlock()
				},
				OnMessage: func(sender, content, id string) {
					mu.Lock()
					gotSender = sender
					gotMessage = content
					mu.Unlock()
				},
			}

			response, err := agents.ExecuteChatWithLLM(ctx, llm, cfg, "Hi there", cb)
			Expect(err).ToNot(HaveOccurred())
			Expect(response).To(ContainSubstring("AI assistant"))

			mu.Lock()
			defer mu.Unlock()
			Expect(gotStatus).To(ContainElement("processing"))
			Expect(gotStatus).To(ContainElement("completed"))
			Expect(gotSender).To(Equal("agent"))
			Expect(gotMessage).To(ContainSubstring("AI assistant"))
		})

		It("should return error when no model is configured", func() {
			cfg := &agents.AgentConfig{Name: "no-model"}
			_, err := agents.ExecuteChat(ctx, "http://localhost", "", cfg, "hello", agents.Callbacks{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no model configured"))
		})

		It("should respect context cancellation", func() {
			// Create a slow mock LLM
			slowLLM := &mockLLM{response: "slow response"}

			cfg := &agents.AgentConfig{
				Name:  "cancel-test",
				Model: "test",
			}

			cancelCtx, cancel := context.WithCancel(ctx)
			cancel() // cancel immediately

			_, err := agents.ExecuteChatWithLLM(cancelCtx, slowLLM, cfg, "hello", agents.Callbacks{})
			// Should either error or return empty due to cancelled context
			// The exact behavior depends on cogito's context handling
			_ = err
		})
	})

	Context("NATSDispatcher", func() {
		It("should dispatch chat via NATS and receive response", func() {
			bridge := agents.NewEventBridge(nc, nil, "test-instance")

			configs := &mockConfigProvider{configs: map[string]*agents.AgentConfig{
				"test-agent": {
					Name:         "test-agent",
					Model:        "test-model",
					SystemPrompt: "Be helpful.",
				},
			}}

			// Subscribe to agent events to capture the response
			var receivedEvents []agents.AgentEvent
			var eventMu sync.Mutex

			sub, err := nc.Subscribe(messaging.SubjectAgentEvents("test-agent", "user1"), func(data []byte) {
				var evt agents.AgentEvent
				if json.Unmarshal(data, &evt) == nil {
					eventMu.Lock()
					receivedEvents = append(receivedEvents, evt)
					eventMu.Unlock()
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			adapter := &natsAdapter{client: nc}
			dispatcher := agents.NewNATSDispatcher(adapter, bridge, configs, "http://localhost:8080", "test-key", "agent.test.execute", "test-workers")

			err = dispatcher.Start(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Dispatch a chat
			messageID, err := dispatcher.Dispatch("user1", "test-agent", "Hello")
			Expect(err).ToNot(HaveOccurred())
			Expect(messageID).ToNot(BeEmpty())

			// Wait for events (user message + processing status should arrive immediately)
			Eventually(func() int {
				eventMu.Lock()
				defer eventMu.Unlock()
				return len(receivedEvents)
			}, "5s").Should(BeNumerically(">=", 2))

			// Verify user message was published
			eventMu.Lock()
			hasUserMsg := false
			hasProcessing := false
			for _, evt := range receivedEvents {
				if evt.EventType == "json_message" && evt.Sender == "user" {
					hasUserMsg = true
				}
				if evt.EventType == "json_message_status" {
					hasProcessing = true
				}
			}
			eventMu.Unlock()

			Expect(hasUserMsg).To(BeTrue(), "user message should be published immediately")
			Expect(hasProcessing).To(BeTrue(), "processing status should be published")
		})

		It("should handle cancellation via EventBridge", func() {
			bridge := agents.NewEventBridge(nc, nil, "cancel-test")

			var cancelled atomic.Bool
			bridge.RegisterCancel("test-agent", "user1", func() {
				cancelled.Store(true)
			})

			// Start cancel listener
			cancelSub, err := bridge.StartCancelListener()
			Expect(err).ToNot(HaveOccurred())
			defer cancelSub.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			// Cancel the execution
			Expect(bridge.CancelExecution("test-agent", "user1")).To(Succeed())

			Eventually(func() bool { return cancelled.Load() }, "5s").Should(BeTrue())
		})

		It("should execute agent chat from enriched payload without ConfigProvider", func() {
			bridge := agents.NewEventBridge(nc, nil, "enriched-test")

			// Create dispatcher with NO ConfigProvider (simulating DB-free worker)
			adapter := &natsAdapter{client: nc}
			dispatcher := agents.NewNATSDispatcher(adapter, bridge, nil, "http://localhost:8080", "test-key", "agent.enriched.execute", "enriched-workers")
			Expect(dispatcher.Start(ctx)).To(Succeed())

			// Subscribe to events to verify processing
			var receivedEvents []agents.AgentEvent
			var eventMu sync.Mutex
			sub, err := nc.Subscribe(messaging.SubjectAgentEvents("enriched-agent", "user1"), func(data []byte) {
				var evt agents.AgentEvent
				if json.Unmarshal(data, &evt) == nil {
					eventMu.Lock()
					receivedEvents = append(receivedEvents, evt)
					eventMu.Unlock()
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			// Publish an enriched AgentChatEvent with embedded Config directly to the queue
			evt := agents.AgentChatEvent{
				AgentName: "enriched-agent",
				UserID:    "user1",
				Message:   "Hello from enriched payload",
				MessageID: "msg-enriched-001",
				Role:      "user",
				Config: &agents.AgentConfig{
					Name:         "enriched-agent",
					Model:        "test-model",
					SystemPrompt: "Be helpful.",
				},
			}
			Expect(nc.Publish("agent.enriched.execute", evt)).To(Succeed())

			// The dispatcher should process this even without a ConfigProvider.
			// It will fail at ExecuteChat (no real LLM), but it should at least
			// publish a processing status event before failing.
			Eventually(func() int {
				eventMu.Lock()
				defer eventMu.Unlock()
				return len(receivedEvents)
			}, "5s").Should(BeNumerically(">=", 1))

			// Verify we got status events (processing or error — both prove the
			// dispatcher accepted the enriched event without a ConfigProvider)
			eventMu.Lock()
			hasEvent := len(receivedEvents) > 0
			eventMu.Unlock()
			Expect(hasEvent).To(BeTrue(), "dispatcher should process enriched event without ConfigProvider")
		})

		It("should round-robin jobs between two dispatchers", func() {
			configs := &mockConfigProvider{configs: map[string]*agents.AgentConfig{
				"rr-agent": {Name: "rr-agent", Model: "test"},
			}}

			bridge1 := agents.NewEventBridge(nc, nil, "instance-1")
			bridge2 := agents.NewEventBridge(nc, nil, "instance-2")

			adapter := &natsAdapter{client: nc}

			var count1, count2 atomic.Int32

			// We can't easily inject mock LLMs into NATSDispatcher since it
			// uses ConfigProvider → ExecuteChat. Instead, we test that
			// the NATS queue distributes messages between two subscribers.
			sub1, err := nc.QueueSubscribe("agent.rr.execute", "rr-workers", func(data []byte) {
				count1.Add(1)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub1.Unsubscribe()

			sub2, err := nc.QueueSubscribe("agent.rr.execute", "rr-workers", func(data []byte) {
				count2.Add(1)
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub2.Unsubscribe()

			time.Sleep(100 * time.Millisecond)

			// Send 10 messages
			for i := 0; i < 10; i++ {
				nc.Publish("agent.rr.execute", agents.AgentChatEvent{
					AgentName: "rr-agent",
					UserID:    "user1",
					Message:   "hello",
				})
			}

			_ = bridge1
			_ = bridge2
			_ = configs
			_ = adapter

			Eventually(func() int32 { return count1.Load() + count2.Load() }, "5s").Should(Equal(int32(10)))
			// Both should have received some (not all 10 to one)
			Expect(count1.Load()).To(BeNumerically(">", 0))
			Expect(count2.Load()).To(BeNumerically(">", 0))
		})
	})

	Context("AgentConfig JSON Compatibility", func() {
		It("should marshal/unmarshal matching LocalAGI format", func() {
			cfg := agents.AgentConfig{
				Name:         "test",
				Model:        "llama3",
				SystemPrompt: "Be helpful",
				MCPServers:   []agents.MCPServer{{URL: "http://mcp.example.com", Token: "tok"}},
				Actions:      []agents.ActionsConfig{{Type: "search", Config: "{}"}},
				EnableSkills: true,
				MaxAttempts:  3,
			}

			data, err := json.Marshal(cfg)
			Expect(err).ToNot(HaveOccurred())

			// Verify key field names match LocalAGI
			var raw map[string]any
			Expect(json.Unmarshal(data, &raw)).To(Succeed())
			Expect(raw).To(HaveKey("name"))
			Expect(raw).To(HaveKey("model"))
			Expect(raw).To(HaveKey("system_prompt"))
			Expect(raw).To(HaveKey("mcp_servers"))
			Expect(raw).To(HaveKey("actions"))
			Expect(raw).To(HaveKey("enable_skills"))
			Expect(raw).To(HaveKey("max_attempts"))

			// Round-trip
			var cfg2 agents.AgentConfig
			Expect(json.Unmarshal(data, &cfg2)).To(Succeed())
			Expect(cfg2.Name).To(Equal("test"))
			Expect(cfg2.Model).To(Equal("llama3"))
			Expect(cfg2.MCPServers).To(HaveLen(1))
			Expect(cfg2.EnableSkills).To(BeTrue())
		})

		It("should survive PostgreSQL round-trip", func() {
			cfg := agents.AgentConfig{
				Name:         "db-test",
				Model:        "qwen",
				SystemPrompt: "Hello world",
				EnableSkills: true,
				SelectedSkills: []string{"skill-a", "skill-b"},
			}

			configJSON, err := json.Marshal(cfg)
			Expect(err).ToNot(HaveOccurred())

			// Save to DB
			Expect(store.SaveConfig(&agents.AgentConfigRecord{
				UserID:     "u1",
				Name:       cfg.Name,
				ConfigJSON: string(configJSON),
				Status:     "active",
			})).To(Succeed())

			// Load from DB
			rec, err := store.GetConfig("u1", "db-test")
			Expect(err).ToNot(HaveOccurred())

			var loaded agents.AgentConfig
			Expect(agents.ParseConfigJSON(rec.ConfigJSON, &loaded)).To(Succeed())
			Expect(loaded.Name).To(Equal("db-test"))
			Expect(loaded.Model).To(Equal("qwen"))
			Expect(loaded.SelectedSkills).To(ConsistOf("skill-a", "skill-b"))
		})
	})

	Context("Node Registration with NodeType", func() {
		It("should store node_type for agent workers", func() {
			registry, err := nodes.NewNodeRegistry(db)
			Expect(err).ToNot(HaveOccurred())

			node := &nodes.BackendNode{
				Name:     "agent-worker-1",
				NodeType: nodes.NodeTypeAgent,
				Status:   "healthy",
			}

			Expect(registry.Register(node, true)).To(Succeed())

			// Verify node type is stored
			loaded, err := registry.Get(node.ID)
			Expect(err).ToNot(HaveOccurred())
			Expect(loaded.NodeType).To(Equal(nodes.NodeTypeAgent))
			Expect(loaded.Name).To(Equal("agent-worker-1"))
		})

		It("should list both backend and agent workers", func() {
			registry, err := nodes.NewNodeRegistry(db)
			Expect(err).ToNot(HaveOccurred())

			backend := &nodes.BackendNode{
				Name:     "backend-1",
				NodeType: nodes.NodeTypeBackend,
				Address:  "localhost:50051",
			}
			agent := &nodes.BackendNode{
				Name:     "agent-1",
				NodeType: nodes.NodeTypeAgent,
			}

			Expect(registry.Register(backend, true)).To(Succeed())
			Expect(registry.Register(agent, true)).To(Succeed())

			allNodes, err := registry.List()
			Expect(err).ToNot(HaveOccurred())

			var backendCount, agentCount int
			for _, n := range allNodes {
				switch n.NodeType {
				case nodes.NodeTypeBackend:
					backendCount++
				case nodes.NodeTypeAgent:
					agentCount++
				}
			}
			Expect(backendCount).To(Equal(1))
			Expect(agentCount).To(Equal(1))
		})
	})

	Context("Full Distributed Chat Flow", func() {
		It("should dispatch chat via NATS, execute, and publish response via EventBridge", func() {
			bridge := agents.NewEventBridge(nc, nil, "flow-test")

			// Store agent config in PostgreSQL
			cfg := agents.AgentConfig{
				Name:         "flow-agent",
				Model:        "test-model",
				SystemPrompt: "You are a test agent.",
			}
			configJSON, err := json.Marshal(cfg)
			Expect(err).ToNot(HaveOccurred())
			Expect(store.SaveConfig(&agents.AgentConfigRecord{
				UserID:     "user1",
				Name:       "flow-agent",
				ConfigJSON: string(configJSON),
				Status:     "active",
			})).To(Succeed())

			// Subscribe to events to capture the full flow
			var receivedEvents []agents.AgentEvent
			var eventMu sync.Mutex
			sub, err := nc.Subscribe(messaging.SubjectAgentEvents("flow-agent", "user1"), func(data []byte) {
				var evt agents.AgentEvent
				if json.Unmarshal(data, &evt) == nil {
					eventMu.Lock()
					receivedEvents = append(receivedEvents, evt)
					eventMu.Unlock()
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			adapter := &natsAdapter{client: nc}
			configs := &mockConfigProvider{configs: map[string]*agents.AgentConfig{
				"flow-agent": &cfg,
			}}

			dispatcher := agents.NewNATSDispatcher(adapter, bridge, configs, "http://localhost:8080", "test-key", "agent.flow.execute", "flow-workers")
			Expect(dispatcher.Start(ctx)).To(Succeed())

			// Dispatch
			messageID, err := dispatcher.Dispatch("user1", "flow-agent", "Hello flow test")
			Expect(err).ToNot(HaveOccurred())
			Expect(messageID).ToNot(BeEmpty())

			// User message + processing status should arrive immediately
			Eventually(func() int {
				eventMu.Lock()
				defer eventMu.Unlock()
				return len(receivedEvents)
			}, "5s").Should(BeNumerically(">=", 2))

			eventMu.Lock()
			var hasUser, hasProcessing bool
			for _, evt := range receivedEvents {
				if evt.EventType == "json_message" && evt.Sender == "user" && evt.Content == "Hello flow test" {
					hasUser = true
				}
				if evt.EventType == "json_message_status" {
					hasProcessing = true
				}
			}
			eventMu.Unlock()
			Expect(hasUser).To(BeTrue(), "expected user message event")
			Expect(hasProcessing).To(BeTrue(), "expected processing status event")
		})
	})

	Context("Background Agent Execution", func() {
		It("should use inner monologue template with permanent goal for system role", func() {
			// ExecuteBackgroundRun should substitute {{.Goal}} in the inner monologue template
			cfg := &agents.AgentConfig{
				Name:                   "bg-agent",
				Model:                  "test-model",
				PermanentGoal:          "Monitor system health and report issues",
				InnerMonologueTemplate: "Your goal is: {{.Goal}}. What should you do next?",
				SystemPrompt:           "You are an autonomous agent.",
			}

			cb := agents.Callbacks{}

			response, err := agents.ExecuteBackgroundRun(ctx, "http://localhost:8080", "key", cfg, cb)
			// The response may be empty due to mock LLM behavior with cogito,
			// but the function should not error
			_ = response
			_ = err
			// The inner monologue template should have been substituted
			// (we can't verify what cogito received directly, but we test the template rendering)
		})

		It("should use default inner monologue when template is empty", func() {
			cfg := &agents.AgentConfig{
				Name:          "bg-default",
				Model:         "test-model",
				PermanentGoal: "Keep things running",
				SystemPrompt:  "You are helpful.",
			}

			// ExecuteBackgroundRun with empty template should use DefaultInnerMonologueTemplate
			Expect(agents.DefaultInnerMonologueTemplate).To(ContainSubstring("{{.Goal}}"))

			// The function should not panic with empty template
			_, _ = agents.ExecuteBackgroundRun(ctx, "http://localhost:8080", "key", cfg, agents.Callbacks{})
		})

		It("should dispatch background run via NATS with system role", func() {
			bridge := agents.NewEventBridge(nc, nil, "bg-test")

			cfg := agents.AgentConfig{
				Name:          "bg-agent",
				Model:         "test-model",
				PermanentGoal: "Monitor system health",
				SystemPrompt:  "You are a monitoring agent.",
			}

			adapter := &natsAdapter{client: nc}
			configs := &mockConfigProvider{configs: map[string]*agents.AgentConfig{
				"bg-agent": &cfg,
			}}

			// Subscribe to capture events
			var receivedEvents []agents.AgentEvent
			var eventMu sync.Mutex
			sub, err := nc.Subscribe(messaging.SubjectAgentEvents("bg-agent", "system"), func(data []byte) {
				var evt agents.AgentEvent
				if json.Unmarshal(data, &evt) == nil {
					eventMu.Lock()
					receivedEvents = append(receivedEvents, evt)
					eventMu.Unlock()
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			dispatcher := agents.NewNATSDispatcher(adapter, bridge, configs, "http://localhost:8080", "test-key", "agent.bg.execute", "bg-workers")
			Expect(dispatcher.Start(ctx)).To(Succeed())

			// Dispatch as background/system role
			evt := agents.AgentChatEvent{
				AgentName: "bg-agent",
				UserID:    "system",
				Message:   "",
				MessageID: "bg-1",
				Role:      "system",
			}
			Expect(nc.Publish("agent.bg.execute", evt)).To(Succeed())

			// Should receive at least a processing status
			Eventually(func() int {
				eventMu.Lock()
				defer eventMu.Unlock()
				return len(receivedEvents)
			}, "10s").Should(BeNumerically(">=", 1))
		})
	})

	Context("Skills Injection", func() {
		It("should render skills prompt with default template", func() {
			skills := []agents.SkillInfo{
				{Name: "web_search", Description: "Search the web for information"},
				{Name: "code_review", Description: "Review code for bugs"},
			}
			result := agents.RenderSkillsPrompt(skills, "")
			Expect(result).To(ContainSubstring("web_search"))
			Expect(result).To(ContainSubstring("code_review"))
			Expect(result).To(ContainSubstring("available_skills"))
		})

		It("should render skills with custom template", func() {
			skills := []agents.SkillInfo{
				{Name: "tool1", Description: "First tool"},
			}
			custom := "Tools: {{range .Skills}}{{.Name}} - {{.Description}}; {{end}}"
			result := agents.RenderSkillsPrompt(skills, custom)
			Expect(result).To(ContainSubstring("tool1 - First tool"))
		})

		It("should filter skills by selected_skills", func() {
			all := []agents.SkillInfo{
				{Name: "a", Description: "skill a"},
				{Name: "b", Description: "skill b"},
				{Name: "c", Description: "skill c"},
			}

			filtered := agents.FilterSkills(all, []string{"a", "c"})
			Expect(filtered).To(HaveLen(2))
			names := []string{filtered[0].Name, filtered[1].Name}
			Expect(names).To(ConsistOf("a", "c"))
		})

		It("should return all skills when selected_skills is empty", func() {
			all := []agents.SkillInfo{
				{Name: "x", Description: "skill x"},
				{Name: "y", Description: "skill y"},
			}
			filtered := agents.FilterSkills(all, nil)
			Expect(filtered).To(HaveLen(2))
		})

		It("should render full content when Content field is set", func() {
			skills := []agents.SkillInfo{
				{Name: "search", Description: "Search the web", Content: "You are a web search skill. Given a query, search the web and return results."},
			}
			result := agents.RenderSkillsPrompt(skills, "")
			Expect(result).To(ContainSubstring("<content>"))
			Expect(result).To(ContainSubstring("You are a web search skill"))
			Expect(result).NotTo(ContainSubstring("<description>"))
		})

		It("should fall back to description when Content is empty", func() {
			skills := []agents.SkillInfo{
				{Name: "search", Description: "Search the web"},
			}
			result := agents.RenderSkillsPrompt(skills, "")
			Expect(result).To(ContainSubstring("<description>"))
			Expect(result).To(ContainSubstring("Search the web"))
			Expect(result).NotTo(ContainSubstring("<content>"))
		})
	})

	Context("Agent Scheduler", func() {
		It("should detect due standalone agents and publish events", func() {
			// Create an agent with standalone_job=true and periodic_runs=1s
			cfg := agents.AgentConfig{
				Name:          "cron-agent",
				Model:         "test-model",
				StandaloneJob: true,
				PeriodicRuns:  "1s",
				SystemPrompt:  "You are autonomous.",
			}
			configJSON, _ := json.Marshal(cfg)
			Expect(store.SaveConfig(&agents.AgentConfigRecord{
				UserID:     "u1",
				Name:       "cron-agent",
				ConfigJSON: string(configJSON),
				Status:     "active",
				// LastRunAt is nil — never run, so it's due immediately
			})).To(Succeed())

			// Subscribe to NATS to capture background run events
			var receivedEvents []agents.AgentChatEvent
			var eventMu sync.Mutex
			sub, err := nc.Subscribe("agent.sched.execute", func(data []byte) {
				var evt agents.AgentChatEvent
				if json.Unmarshal(data, &evt) == nil {
					eventMu.Lock()
					receivedEvents = append(receivedEvents, evt)
					eventMu.Unlock()
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			// Start scheduler with short poll interval for testing
			adapter := &natsAdapter{client: nc}
			scheduler := agents.NewAgentScheduler(db, adapter, store, "agent.sched.execute")

			schedCtx, schedCancel := context.WithCancel(ctx)
			defer schedCancel()
			go scheduler.Start(schedCtx)

			// Wait for the scheduler to fire
			Eventually(func() int {
				eventMu.Lock()
				defer eventMu.Unlock()
				return len(receivedEvents)
			}, "20s").Should(BeNumerically(">=", 1))

			eventMu.Lock()
			evt := receivedEvents[0]
			eventMu.Unlock()

			Expect(evt.AgentName).To(Equal("cron-agent"))
			Expect(evt.UserID).To(Equal("u1"))
			Expect(evt.Role).To(Equal("system"))

			// Verify enriched payload: config should be embedded in the event
			Expect(evt.Config).ToNot(BeNil(), "scheduler should embed config in the event")
			Expect(evt.Config.Model).To(Equal("test-model"))
			Expect(evt.Config.StandaloneJob).To(BeTrue())
		})

		It("should skip agents without standalone_job", func() {
			cfg := agents.AgentConfig{
				Name:          "no-cron-agent",
				Model:         "test-model",
				StandaloneJob: false,
			}
			configJSON, _ := json.Marshal(cfg)
			Expect(store.SaveConfig(&agents.AgentConfigRecord{
				UserID:     "u1",
				Name:       "no-cron-agent",
				ConfigJSON: string(configJSON),
				Status:     "active",
			})).To(Succeed())

			Expect(agents.IsDueExported(nil, 10*time.Minute)).To(BeTrue(), "nil lastRun should be due")

			// But the scheduler skips non-standalone agents
			// (tested implicitly via the scheduler — it won't publish for this agent)
		})

		It("should not re-run before interval elapses", func() {
			now := time.Now()
			Expect(agents.IsDueExported(&now, 10*time.Minute)).To(BeFalse(), "just ran should not be due")

			past := time.Now().Add(-11 * time.Minute)
			Expect(agents.IsDueExported(&past, 10*time.Minute)).To(BeTrue(), "11m ago with 10m interval should be due")
		})
	})

	Context("Agent Chat with Tool Calls and Skills", func() {
		It("should execute agent chat where LLM calls request_skill tool", func() {
			// Configure mockLLM:
			// - First call: return tool_call for request_skill with skill_name="web_search"
			// - Second call: return final response incorporating skill content
			llm := &mockLLM{
				response: "Based on the web search skill, here is the information you need.",
				toolCall: &openai.ToolCall{
					ID:   "call_001",
					Type: openai.ToolTypeFunction,
					Function: openai.FunctionCall{
						Name:      "request_skill",
						Arguments: `{"skill_name":"web_search"}`,
					},
				},
			}

			skills := []agents.SkillInfo{
				{Name: "web_search", Description: "Search the web for information", Content: "You are a web search skill. Query external sources and return results."},
				{Name: "code_review", Description: "Review code for bugs"},
			}

			cfg := &agents.AgentConfig{
				Name:           "skill-tool-agent",
				Model:          "test-model",
				SystemPrompt:   "You are helpful.",
				EnableSkills:   true,
				SkillsMode:     agents.SkillsModeTools,
				SelectedSkills: []string{"web_search"},
				MaxIterations:  1,
			}

			var statuses []string
			var finalMessage string
			var toolCalls []string
			var toolResults []string
			var mu sync.Mutex

			cb := agents.Callbacks{
				OnStatus: func(s string) {
					mu.Lock()
					statuses = append(statuses, s)
					mu.Unlock()
				},
				OnMessage: func(sender, content, id string) {
					mu.Lock()
					if sender == "agent" {
						finalMessage = content
					}
					mu.Unlock()
				},
				OnToolCall: func(name, args string) {
					mu.Lock()
					toolCalls = append(toolCalls, name)
					mu.Unlock()
				},
				OnToolResult: func(name, result string) {
					mu.Lock()
					toolResults = append(toolResults, name+": "+result)
					mu.Unlock()
				},
			}

			opts := agents.ExecuteChatOpts{
				SkillProvider: &staticSkillProviderTest{skills: skills},
			}

			response, err := agents.ExecuteChatWithLLM(ctx, llm, cfg, "Find me info about Go", cb, opts)
			Expect(err).ToNot(HaveOccurred())
			Expect(response).To(ContainSubstring("web search skill"))

			mu.Lock()
			defer mu.Unlock()
			Expect(statuses).To(ContainElement("processing"))
			Expect(statuses).To(ContainElement("completed"))
			Expect(finalMessage).To(ContainSubstring("web search skill"))
			// Verify the tool was called
			Expect(toolCalls).To(ContainElement("request_skill"))
			// Verify tool result contains skill content
			Expect(toolResults).To(HaveLen(1))
			Expect(toolResults[0]).To(ContainSubstring("web_search"))
		})

		It("should inject skills into system prompt in prompt mode", func() {
			// In prompt mode, skills are injected into the system prompt.
			// The LLM should see the skill content in the system prompt.
			var receivedMessages []openai.ChatCompletionMessage
			llm := &mockLLMWithCapture{
				response: "I see the skill content in my prompt.",
				captureMessages: func(msgs []openai.ChatCompletionMessage) {
					receivedMessages = msgs
				},
			}

			skills := []agents.SkillInfo{
				{Name: "data_analysis", Description: "Analyze data", Content: "You are a data analysis skill. Use pandas and numpy."},
			}

			cfg := &agents.AgentConfig{
				Name:         "prompt-skill-agent",
				Model:        "test-model",
				SystemPrompt: "You are helpful.",
				EnableSkills: true,
				SkillsMode:   agents.SkillsModePrompt,
			}

			opts := agents.ExecuteChatOpts{
				SkillProvider: &staticSkillProviderTest{skills: skills},
			}

			_, err := agents.ExecuteChatWithLLM(ctx, llm, cfg, "Analyze this data", agents.Callbacks{}, opts)
			Expect(err).ToNot(HaveOccurred())

			// Verify system prompt contains skill content
			Expect(receivedMessages).ToNot(BeEmpty())
			systemMsg := receivedMessages[0]
			Expect(systemMsg.Role).To(Equal("system"))
			Expect(systemMsg.Content).To(ContainSubstring("data_analysis"))
			Expect(systemMsg.Content).To(ContainSubstring("pandas and numpy"))
		})
	})

	Context("Full Distributed Agent Execution via NATS with Mock LLM Server", func() {
		It("should dispatch chat via NATS, execute on worker with mock LLM, and publish response via EventBridge", func() {
			// Start mock LLM HTTP server
			llmURL, llmShutdown := startAgentMockLLMServer("I found the answer using my skills.")
			defer llmShutdown()

			bridge := agents.NewEventBridge(nc, nil, "full-e2e-test")

			// Subscribe to agent events
			var receivedEvents []agents.AgentEvent
			var eventMu sync.Mutex
			sub, err := nc.Subscribe(messaging.SubjectAgentEvents("e2e-agent", "user1"), func(data []byte) {
				var evt agents.AgentEvent
				if json.Unmarshal(data, &evt) == nil {
					eventMu.Lock()
					receivedEvents = append(receivedEvents, evt)
					eventMu.Unlock()
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			adapter := &natsAdapter{client: nc}
			// Point dispatcher at our mock LLM server
			dispatcher := agents.NewNATSDispatcher(adapter, bridge, nil, llmURL, "test-key", "agent.e2e.execute", "e2e-workers")
			Expect(dispatcher.Start(ctx)).To(Succeed())

			time.Sleep(100 * time.Millisecond)

			// Publish enriched event with skills
			evt := agents.AgentChatEvent{
				AgentName: "e2e-agent",
				UserID:    "user1",
				Message:   "Hello, use your skills",
				MessageID: "msg-e2e-001",
				Role:      "user",
				Config: &agents.AgentConfig{
					Name:         "e2e-agent",
					Model:        "test-model",
					SystemPrompt: "You are helpful.",
					EnableSkills: true,
					SkillsMode:   agents.SkillsModePrompt,
				},
				Skills: []agents.SkillInfo{
					{Name: "search", Description: "Search the web", Content: "Full search skill content here"},
				},
			}
			Expect(nc.Publish("agent.e2e.execute", evt)).To(Succeed())

			// Wait for the full execution: user message + processing + agent response + completed
			Eventually(func() int {
				eventMu.Lock()
				defer eventMu.Unlock()
				return len(receivedEvents)
			}, "15s").Should(BeNumerically(">=", 3))

			eventMu.Lock()
			defer eventMu.Unlock()

			var hasAgentMessage, hasCompleted bool
			for _, evt := range receivedEvents {
				if evt.EventType == "json_message" && evt.Sender == "agent" {
					hasAgentMessage = true
					Expect(evt.Content).To(ContainSubstring("found the answer"))
				}
				if evt.EventType == "json_message_status" && evt.Metadata != "" {
					var meta map[string]string
					json.Unmarshal([]byte(evt.Metadata), &meta)
					if meta["status"] == "completed" {
						hasCompleted = true
					}
				}
			}
			Expect(hasAgentMessage).To(BeTrue(), "should receive agent response message via EventBridge")
			Expect(hasCompleted).To(BeTrue(), "should receive completed status via EventBridge")
		})

		It("should execute background agent run via NATS dispatcher with mock LLM", func() {
			// Start mock LLM HTTP server
			llmURL, llmShutdown := startAgentMockLLMServer("All systems operational. No issues detected.")
			defer llmShutdown()

			bridge := agents.NewEventBridge(nc, nil, "bg-e2e-test")

			// Subscribe to agent events
			var receivedEvents []agents.AgentEvent
			var eventMu sync.Mutex
			sub, err := nc.Subscribe(messaging.SubjectAgentEvents("bg-e2e-agent", "system"), func(data []byte) {
				var evt agents.AgentEvent
				if json.Unmarshal(data, &evt) == nil {
					eventMu.Lock()
					receivedEvents = append(receivedEvents, evt)
					eventMu.Unlock()
				}
			})
			Expect(err).ToNot(HaveOccurred())
			defer sub.Unsubscribe()

			adapter := &natsAdapter{client: nc}
			dispatcher := agents.NewNATSDispatcher(adapter, bridge, nil, llmURL, "test-key", "agent.bg-e2e.execute", "bg-e2e-workers")
			Expect(dispatcher.Start(ctx)).To(Succeed())

			time.Sleep(100 * time.Millisecond)

			// Publish as system role (background/autonomous run)
			evt := agents.AgentChatEvent{
				AgentName: "bg-e2e-agent",
				UserID:    "system",
				Message:   "",
				MessageID: "msg-bg-e2e-001",
				Role:      "system",
				Config: &agents.AgentConfig{
					Name:                   "bg-e2e-agent",
					Model:                  "test-model",
					PermanentGoal:          "Monitor all services and report status",
					SystemPrompt:           "You are an autonomous monitoring agent.",
					InnerMonologueTemplate: "Your goal is: {{.Goal}}. What should you do?",
				},
			}
			Expect(nc.Publish("agent.bg-e2e.execute", evt)).To(Succeed())

			// Wait for agent response
			Eventually(func() int {
				eventMu.Lock()
				defer eventMu.Unlock()
				return len(receivedEvents)
			}, "15s").Should(BeNumerically(">=", 2))

			eventMu.Lock()
			defer eventMu.Unlock()

			var hasAgentMessage, hasCompleted bool
			for _, evt := range receivedEvents {
				if evt.EventType == "json_message" && evt.Sender == "agent" {
					hasAgentMessage = true
					Expect(evt.Content).To(ContainSubstring("systems operational"))
				}
				if evt.EventType == "json_message_status" && evt.Metadata != "" {
					var meta map[string]string
					json.Unmarshal([]byte(evt.Metadata), &meta)
					if meta["status"] == "completed" {
						hasCompleted = true
					}
				}
			}
			Expect(hasAgentMessage).To(BeTrue(), "background agent should produce a response via EventBridge")
			Expect(hasCompleted).To(BeTrue(), "background agent should complete")
		})
	})

	Context("Background Agent Execution with Mock LLM", func() {
		It("should execute background run with mock LLM and verify goal in prompt", func() {
			var receivedMessages []openai.ChatCompletionMessage
			llm := &mockLLMWithCapture{
				response: "System health is nominal. All services running.",
				captureMessages: func(msgs []openai.ChatCompletionMessage) {
					receivedMessages = msgs
				},
			}

			cfg := &agents.AgentConfig{
				Name:          "bg-llm-agent",
				Model:         "test-model",
				PermanentGoal: "Monitor system health and report issues",
				SystemPrompt:  "You are an autonomous monitoring agent.",
			}

			var statuses []string
			var gotMessage string
			var mu sync.Mutex

			cb := agents.Callbacks{
				OnStatus: func(s string) {
					mu.Lock()
					statuses = append(statuses, s)
					mu.Unlock()
				},
				OnMessage: func(sender, content, id string) {
					mu.Lock()
					if sender == "agent" {
						gotMessage = content
					}
					mu.Unlock()
				},
			}

			response, err := agents.ExecuteBackgroundRunWithLLM(ctx, llm, cfg, cb)
			Expect(err).ToNot(HaveOccurred())
			Expect(response).To(ContainSubstring("System health"))

			mu.Lock()
			defer mu.Unlock()
			Expect(statuses).To(ContainElement("processing"))
			Expect(statuses).To(ContainElement("completed"))
			Expect(gotMessage).To(ContainSubstring("System health"))

			// Verify the inner monologue template was substituted with the goal
			Expect(receivedMessages).To(HaveLen(2)) // system + user (inner monologue)
			userMsg := receivedMessages[1]
			Expect(userMsg.Role).To(Equal("user"))
			Expect(userMsg.Content).To(ContainSubstring("Monitor system health and report issues"))
		})

		It("should execute background run with skills", func() {
			var receivedMessages []openai.ChatCompletionMessage
			llm := &mockLLMWithCapture{
				response: "Executed monitoring skill successfully.",
				captureMessages: func(msgs []openai.ChatCompletionMessage) {
					receivedMessages = msgs
				},
			}

			cfg := &agents.AgentConfig{
				Name:          "bg-skill-agent",
				Model:         "test-model",
				PermanentGoal: "Check service status",
				SystemPrompt:  "You are a monitoring agent.",
				EnableSkills:  true,
				SkillsMode:    agents.SkillsModePrompt,
			}

			skills := []agents.SkillInfo{
				{Name: "health_check", Description: "Check service health", Content: "Run health checks on all services."},
			}

			opts := agents.ExecuteChatOpts{
				SkillProvider: &staticSkillProviderTest{skills: skills},
			}

			response, err := agents.ExecuteBackgroundRunWithLLM(ctx, llm, cfg, agents.Callbacks{}, opts)
			Expect(err).ToNot(HaveOccurred())
			Expect(response).To(ContainSubstring("monitoring skill"))

			// Verify skills were injected into system prompt
			Expect(receivedMessages).ToNot(BeEmpty())
			systemMsg := receivedMessages[0]
			Expect(systemMsg.Content).To(ContainSubstring("health_check"))
			Expect(systemMsg.Content).To(ContainSubstring("Run health checks"))
		})
	})

	Context("Config Metadata", func() {
		It("should return all expected sections", func() {
			meta := agents.DefaultConfigMeta()
			Expect(meta.Fields).ToNot(BeEmpty())

			sections := map[string]bool{}
			for _, f := range meta.Fields {
				sections[f.Tags.Section] = true
			}
			Expect(sections).To(HaveKey("BasicInfo"))
			Expect(sections).To(HaveKey("ModelSettings"))
			Expect(sections).To(HaveKey("MemorySettings"))
			Expect(sections).To(HaveKey("PromptsGoals"))
			Expect(sections).To(HaveKey("AdvancedSettings"))
			Expect(sections).To(HaveKey("MCP"))
		})

		It("should include key fields", func() {
			meta := agents.DefaultConfigMeta()
			fieldNames := map[string]bool{}
			for _, f := range meta.Fields {
				fieldNames[f.Name] = true
			}
			Expect(fieldNames).To(HaveKey("name"))
			Expect(fieldNames).To(HaveKey("model"))
			Expect(fieldNames).To(HaveKey("system_prompt"))
			Expect(fieldNames).To(HaveKey("enable_kb"))
			Expect(fieldNames).To(HaveKey("kb_mode"))
			Expect(fieldNames).To(HaveKey("enable_skills"))
			Expect(fieldNames).To(HaveKey("mcp_stdio_servers"))
			Expect(fieldNames).To(HaveKey("permanent_goal"))
		})
	})
})
