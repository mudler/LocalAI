package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/core/config"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/cli/workerregistry"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/jobs"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/pkg/sanitize"
	"github.com/mudler/cogito"
	"github.com/mudler/cogito/clients"
	"github.com/mudler/xlog"
)

// AgentWorkerCMD starts a dedicated agent worker process for distributed mode.
// It registers with the frontend, subscribes to the NATS agent execution queue,
// and executes agent chats using cogito. The worker is a pure executor — it
// receives the full agent config and skills in the NATS job payload, so it
// does not need direct database access.
//
// Usage:
//
//	localai agent-worker --nats-url nats://... --register-to http://localai:8080
type AgentWorkerCMD struct {
	// NATS (required)
	NatsURL string `env:"LOCALAI_NATS_URL" required:"" help:"NATS server URL" group:"distributed"`

	// Registration (required)
	RegisterTo        string `env:"LOCALAI_REGISTER_TO" required:"" help:"Frontend URL for registration" group:"registration"`
	NodeName          string `env:"LOCALAI_NODE_NAME" help:"Node name for registration (defaults to hostname)" group:"registration"`
	RegistrationToken string `env:"LOCALAI_REGISTRATION_TOKEN" help:"Token for authenticating with the frontend" group:"registration"`
	HeartbeatInterval string `env:"LOCALAI_HEARTBEAT_INTERVAL" default:"10s" help:"Interval between heartbeats" group:"registration"`

	// API access
	APIURL   string `env:"LOCALAI_API_URL" help:"LocalAI API URL for inference (auto-derived from RegisterTo if not set)" group:"api"`
	APIToken string `env:"LOCALAI_API_TOKEN" help:"API token for LocalAI inference (auto-provisioned during registration if not set)" group:"api"`

	// NATS subjects
	Subject string `env:"LOCALAI_AGENT_SUBJECT" default:"agent.execute" help:"NATS subject for agent execution" group:"distributed"`
	Queue   string `env:"LOCALAI_AGENT_QUEUE" default:"agent-workers" help:"NATS queue group name" group:"distributed"`

	// Timeouts
	MCPCIJobTimeout string `env:"LOCALAI_MCP_CI_JOB_TIMEOUT" default:"10m" help:"Timeout for MCP CI job execution" group:"distributed"`
}

func (cmd *AgentWorkerCMD) Run(ctx *cliContext.Context) error {
	xlog.Info("Starting agent worker", "nats", sanitize.URL(cmd.NatsURL), "register_to", cmd.RegisterTo)

	// Resolve API URL
	apiURL := cmp.Or(cmd.APIURL, strings.TrimRight(cmd.RegisterTo, "/"))

	// Register with frontend
	regClient := &workerregistry.RegistrationClient{
		FrontendURL:       cmd.RegisterTo,
		RegistrationToken: cmd.RegistrationToken,
	}

	nodeName := cmd.NodeName
	if nodeName == "" {
		hostname, _ := os.Hostname()
		nodeName = "agent-" + hostname
	}
	registrationBody := map[string]any{
		"name":      nodeName,
		"node_type": "agent",
	}
	if cmd.RegistrationToken != "" {
		registrationBody["token"] = cmd.RegistrationToken
	}

	nodeID, apiToken, err := regClient.RegisterWithRetry(context.Background(), registrationBody, 10)
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	xlog.Info("Registered with frontend", "nodeID", nodeID, "frontend", cmd.RegisterTo)

	// Use provisioned API token if none was set
	if cmd.APIToken == "" {
		cmd.APIToken = apiToken
	}

	// Start heartbeat
	heartbeatInterval, err := time.ParseDuration(cmd.HeartbeatInterval)
	if err != nil && cmd.HeartbeatInterval != "" {
		xlog.Warn("invalid heartbeat interval, using default 10s", "input", cmd.HeartbeatInterval, "error", err)
	}
	heartbeatInterval = cmp.Or(heartbeatInterval, 10*time.Second)
	// Context cancelled on shutdown — used by heartbeat and other background goroutines
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	go regClient.HeartbeatLoop(shutdownCtx, nodeID, heartbeatInterval, func() map[string]any { return map[string]any{} })

	// Connect to NATS
	natsClient, err := messaging.New(cmd.NatsURL)
	if err != nil {
		return fmt.Errorf("connecting to NATS: %w", err)
	}
	defer natsClient.Close()

	// Create event bridge for publishing results back via NATS
	eventBridge := agents.NewEventBridge(natsClient, nil, "agent-worker-"+nodeID)

	// Start cancel listener
	cancelSub, err := eventBridge.StartCancelListener()
	if err != nil {
		xlog.Warn("Failed to start cancel listener", "error", err)
	} else {
		defer cancelSub.Unsubscribe()
	}

	// Create and start the NATS dispatcher.
	// No ConfigProvider or SkillStore needed — config and skills arrive in the job payload.
	dispatcher := agents.NewNATSDispatcher(
		natsClient,
		eventBridge,
		nil, // no ConfigProvider: config comes in the enriched NATS payload
		apiURL, cmd.APIToken,
		cmd.Subject, cmd.Queue,
		0, // no concurrency limit (CLI worker)
	)

	if err := dispatcher.Start(shutdownCtx); err != nil {
		return fmt.Errorf("starting dispatcher: %w", err)
	}

	// Subscribe to MCP tool execution requests (load-balanced across workers).
	// The frontend routes model-level MCP tool calls here via NATS request-reply.
	if _, err := natsClient.QueueSubscribeReply(messaging.SubjectMCPToolExecute, messaging.QueueAgentWorkers, func(data []byte, reply func([]byte)) {
		handleMCPToolRequest(data, reply)
	}); err != nil {
		return fmt.Errorf("subscribing to %s: %w", messaging.SubjectMCPToolExecute, err)
	}

	// Subscribe to MCP discovery requests (load-balanced across workers).
	if _, err := natsClient.QueueSubscribeReply(messaging.SubjectMCPDiscovery, messaging.QueueAgentWorkers, func(data []byte, reply func([]byte)) {
		handleMCPDiscoveryRequest(data, reply)
	}); err != nil {
		return fmt.Errorf("subscribing to %s: %w", messaging.SubjectMCPDiscovery, err)
	}

	// Subscribe to MCP CI job execution (load-balanced across agent workers).
	// In distributed mode, MCP CI jobs are routed here because the frontend
	// cannot create MCP sessions (e.g., stdio servers using docker).
	mcpCIJobTimeout, err := time.ParseDuration(cmd.MCPCIJobTimeout)
	if err != nil && cmd.MCPCIJobTimeout != "" {
		xlog.Warn("invalid MCP CI job timeout, using default 10m", "input", cmd.MCPCIJobTimeout, "error", err)
	}
	mcpCIJobTimeout = cmp.Or(mcpCIJobTimeout, config.DefaultMCPCIJobTimeout)

	if _, err := natsClient.QueueSubscribe(messaging.SubjectMCPCIJobsNew, messaging.QueueWorkers, func(data []byte) {
		handleMCPCIJob(shutdownCtx, data, apiURL, cmd.APIToken, natsClient, mcpCIJobTimeout)
	}); err != nil {
		return fmt.Errorf("subscribing to %s: %w", messaging.SubjectMCPCIJobsNew, err)
	}

	// Subscribe to backend stop events to clean up cached MCP sessions.
	// In the main application this is done via ml.OnModelUnload, but the agent
	// worker has no model loader — we listen for the NATS stop event instead.
	if _, err := natsClient.Subscribe(messaging.SubjectNodeBackendStop(nodeID), func(data []byte) {
		var req struct {
			Backend string `json:"backend"`
		}
		if json.Unmarshal(data, &req) == nil && req.Backend != "" {
			mcpTools.CloseMCPSessions(req.Backend)
		}
	}); err != nil {
		return fmt.Errorf("subscribing to %s: %w", messaging.SubjectNodeBackendStop(nodeID), err)
	}

	xlog.Info("Agent worker ready, waiting for jobs", "subject", cmd.Subject, "queue", cmd.Queue)

	// Wait for shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	xlog.Info("Shutting down agent worker")
	dispatcher.Stop()
	mcpTools.CloseAllMCPSessions()
	regClient.GracefulDeregister(nodeID)
	return nil
}

// handleMCPToolRequest handles a NATS request-reply for MCP tool execution.
// The worker creates/caches MCP sessions from the serialized config and executes the tool.
func handleMCPToolRequest(data []byte, reply func([]byte)) {
	var req mcpRemote.MCPToolRequest
	if err := json.Unmarshal(data, &req); err != nil {
		sendMCPToolReply(reply, "", fmt.Sprintf("unmarshal error: %v", err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.DefaultMCPToolTimeout)
	defer cancel()

	// Create/cache named MCP sessions from the provided config
	namedSessions, err := mcpTools.NamedSessionsFromMCPConfig(req.ModelName, req.RemoteServers, req.StdioServers, nil)
	if err != nil {
		sendMCPToolReply(reply, "", fmt.Sprintf("session error: %v", err))
		return
	}

	// Discover tools to find the right session
	tools, err := mcpTools.DiscoverMCPTools(ctx, namedSessions)
	if err != nil {
		sendMCPToolReply(reply, "", fmt.Sprintf("discovery error: %v", err))
		return
	}

	// Execute the tool
	argsJSON, _ := json.Marshal(req.Arguments)
	result, err := mcpTools.ExecuteMCPToolCall(ctx, tools, req.ToolName, string(argsJSON))
	if err != nil {
		sendMCPToolReply(reply, "", err.Error())
		return
	}

	sendMCPToolReply(reply, result, "")
}

func sendMCPToolReply(reply func([]byte), result, errMsg string) {
	resp := mcpRemote.MCPToolResponse{Result: result, Error: errMsg}
	data, _ := json.Marshal(resp)
	reply(data)
}

// handleMCPDiscoveryRequest handles a NATS request-reply for MCP tool/prompt/resource discovery.
func handleMCPDiscoveryRequest(data []byte, reply func([]byte)) {
	var req mcpRemote.MCPDiscoveryRequest
	if err := json.Unmarshal(data, &req); err != nil {
		sendMCPDiscoveryReply(reply, nil, nil, fmt.Sprintf("unmarshal error: %v", err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), config.DefaultMCPDiscoveryTimeout)
	defer cancel()

	// Create/cache named MCP sessions
	namedSessions, err := mcpTools.NamedSessionsFromMCPConfig(req.ModelName, req.RemoteServers, req.StdioServers, nil)
	if err != nil {
		sendMCPDiscoveryReply(reply, nil, nil, fmt.Sprintf("session error: %v", err))
		return
	}

	// List servers with their tools/prompts/resources
	serverInfos, err := mcpTools.ListMCPServers(ctx, namedSessions)
	if err != nil {
		sendMCPDiscoveryReply(reply, nil, nil, fmt.Sprintf("list error: %v", err))
		return
	}

	// Also get tool function schemas for the frontend
	tools, _ := mcpTools.DiscoverMCPTools(ctx, namedSessions)
	var toolDefs []mcpRemote.MCPToolDef
	for _, t := range tools {
		toolDefs = append(toolDefs, mcpRemote.MCPToolDef{
			ServerName: t.ServerName,
			ToolName:   t.ToolName,
			Function:   t.Function,
		})
	}

	// Convert server infos
	var servers []mcpRemote.MCPServerInfo
	for _, s := range serverInfos {
		servers = append(servers, mcpRemote.MCPServerInfo{
			Name:      s.Name,
			Type:      s.Type,
			Tools:     s.Tools,
			Prompts:   s.Prompts,
			Resources: s.Resources,
		})
	}

	sendMCPDiscoveryReply(reply, servers, toolDefs, "")
}

func sendMCPDiscoveryReply(reply func([]byte), servers []mcpRemote.MCPServerInfo, tools []mcpRemote.MCPToolDef, errMsg string) {
	resp := mcpRemote.MCPDiscoveryResponse{Servers: servers, Tools: tools, Error: errMsg}
	data, _ := json.Marshal(resp)
	reply(data)
}

// handleMCPCIJob processes an MCP CI job on the agent worker.
// The agent worker can create MCP sessions (has docker) and call the LocalAI API for inference.
func handleMCPCIJob(shutdownCtx context.Context, data []byte, apiURL, apiToken string, natsClient messaging.MessagingClient, jobTimeout time.Duration) {
	var evt jobs.JobEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		xlog.Error("Failed to unmarshal job event", "error", err)
		return
	}

	job := evt.Job
	task := evt.Task
	if job == nil || task == nil {
		xlog.Error("MCP CI job missing enriched data", "jobID", evt.JobID)
		publishJobResult(natsClient, evt.JobID, "failed", "", "job or task data missing from NATS event")
		return
	}

	modelCfg := evt.ModelConfig
	if modelCfg == nil {
		publishJobResult(natsClient, evt.JobID, "failed", "", "model config missing from job event")
		return
	}

	xlog.Info("Processing MCP CI job", "jobID", evt.JobID, "taskID", evt.TaskID, "model", task.Model)

	// Publish running status
	natsClient.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
		JobID: evt.JobID, Status: "running", Message: "Job started on agent worker",
	})

	// Parse MCP config
	if modelCfg.MCP.Servers == "" && modelCfg.MCP.Stdio == "" {
		publishJobResult(natsClient, evt.JobID, "failed", "", "no MCP servers configured for model")
		return
	}

	remote, stdio, err := modelCfg.MCP.MCPConfigFromYAML()
	if err != nil {
		publishJobResult(natsClient, evt.JobID, "failed", "", fmt.Sprintf("failed to parse MCP config: %v", err))
		return
	}

	// Create MCP sessions locally (agent worker has docker)
	sessions, err := mcpTools.SessionsFromMCPConfig(modelCfg.Name, remote, stdio)
	if err != nil || len(sessions) == 0 {
		errMsg := "no working MCP servers found"
		if err != nil {
			errMsg = fmt.Sprintf("failed to create MCP sessions: %v", err)
		}
		publishJobResult(natsClient, evt.JobID, "failed", "", errMsg)
		return
	}

	// Build prompt from template
	prompt := task.Prompt
	if task.CronParametersJSON != "" {
		var params map[string]string
		if err := json.Unmarshal([]byte(task.CronParametersJSON), &params); err != nil {
			xlog.Warn("Failed to unmarshal parameters", "error", err)
		}
		for k, v := range params {
			prompt = strings.ReplaceAll(prompt, "{{."+k+"}}", v)
		}
	}
	if job.ParametersJSON != "" {
		var params map[string]string
		if err := json.Unmarshal([]byte(job.ParametersJSON), &params); err != nil {
			xlog.Warn("Failed to unmarshal parameters", "error", err)
		}
		for k, v := range params {
			prompt = strings.ReplaceAll(prompt, "{{."+k+"}}", v)
		}
	}

	// Create LLM client pointing back to the frontend API
	llm := clients.NewLocalAILLM(task.Model, apiToken, apiURL)

	// Build cogito options
	ctx, cancel := context.WithTimeout(shutdownCtx, jobTimeout)
	defer cancel()

	// Update job status to running in DB
	publishJobStatus(natsClient, evt.JobID, "running", "")

	// Buffer stream tokens and flush as complete blocks
	var reasoningBuf, contentBuf strings.Builder
	var lastStreamType cogito.StreamEventType

	flushStreamBuf := func() {
		if reasoningBuf.Len() > 0 {
			natsClient.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
				JobID: evt.JobID, TraceType: "reasoning", TraceContent: reasoningBuf.String(),
			})
			reasoningBuf.Reset()
		}
		if contentBuf.Len() > 0 {
			natsClient.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
				JobID: evt.JobID, TraceType: "content", TraceContent: contentBuf.String(),
			})
			contentBuf.Reset()
		}
	}

	cogitoOpts := modelCfg.BuildCogitoOptions()
	cogitoOpts = append(cogitoOpts,
		cogito.WithContext(ctx),
		cogito.WithMCPs(sessions...),
		cogito.WithStatusCallback(func(status string) {
			flushStreamBuf()
			natsClient.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
				JobID: evt.JobID, TraceType: "status", TraceContent: status,
			})
		}),
		cogito.WithToolCallResultCallback(func(t cogito.ToolStatus) {
			flushStreamBuf()
			natsClient.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
				JobID: evt.JobID, TraceType: "tool_result", TraceContent: fmt.Sprintf("%s: %s", t.Name, t.Result),
			})
		}),
		cogito.WithStreamCallback(func(ev cogito.StreamEvent) {
			// Flush if stream type changed (e.g., reasoning → content)
			if ev.Type != lastStreamType {
				flushStreamBuf()
				lastStreamType = ev.Type
			}
			switch ev.Type {
			case cogito.StreamEventReasoning:
				reasoningBuf.WriteString(ev.Content)
			case cogito.StreamEventContent:
				contentBuf.WriteString(ev.Content)
			case cogito.StreamEventToolCall:
				natsClient.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
					JobID: evt.JobID, TraceType: "tool_call", TraceContent: fmt.Sprintf("%s(%s)", ev.ToolName, ev.ToolArgs),
				})
			}
		}),
	)

	// Execute via cogito
	fragment := cogito.NewEmptyFragment()
	fragment = fragment.AddMessage("user", prompt)

	f, err := cogito.ExecuteTools(llm, fragment, cogitoOpts...)
	flushStreamBuf() // flush any remaining buffered tokens

	if err != nil {
		publishJobResult(natsClient, evt.JobID, "failed", "", fmt.Sprintf("cogito execution failed: %v", err))
		return
	}

	result := ""
	if msg := f.LastMessage(); msg != nil {
		result = msg.Content
	}
	publishJobResult(natsClient, evt.JobID, "completed", result, "")
	xlog.Info("MCP CI job completed", "jobID", evt.JobID, "resultLen", len(result))
}

func publishJobStatus(nc messaging.MessagingClient, jobID, status, message string) {
	jobs.PublishJobProgress(nc, jobID, status, message)
}

func publishJobResult(nc messaging.MessagingClient, jobID, status, result, errMsg string) {
	jobs.PublishJobResult(nc, jobID, status, result, errMsg)
}

