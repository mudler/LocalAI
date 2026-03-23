package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/jobs"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
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
}

func (cmd *AgentWorkerCMD) Run(ctx *cliContext.Context) error {
	xlog.Info("Starting agent worker", "nats", cmd.NatsURL, "register_to", cmd.RegisterTo)

	// Resolve API URL
	apiURL := cmd.APIURL
	if apiURL == "" {
		apiURL = strings.TrimRight(cmd.RegisterTo, "/")
	}

	// Register with frontend
	nodeID, apiToken, err := cmd.registerWithFrontend()
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	xlog.Info("Registered with frontend", "nodeID", nodeID, "frontend", cmd.RegisterTo)

	// Use provisioned API token if none was set
	if cmd.APIToken == "" {
		cmd.APIToken = apiToken
	}

	// Start heartbeat
	heartbeatInterval, _ := time.ParseDuration(cmd.HeartbeatInterval)
	if heartbeatInterval == 0 {
		heartbeatInterval = 10 * time.Second
	}
	go cmd.heartbeatLoop(nodeID, heartbeatInterval)

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
		&natsClientAdapter{natsClient},
		eventBridge,
		nil, // no ConfigProvider: config comes in the enriched NATS payload
		apiURL, cmd.APIToken,
		cmd.Subject, cmd.Queue,
	)

	if err := dispatcher.Start(nil); err != nil {
		return fmt.Errorf("starting dispatcher: %w", err)
	}

	// Subscribe to MCP tool execution requests (load-balanced across workers).
	// The frontend routes model-level MCP tool calls here via NATS request-reply.
	natsClient.QueueSubscribeReply(messaging.SubjectMCPToolExecute, messaging.QueueAgentWorkers, func(data []byte, reply func([]byte)) {
		handleMCPToolRequest(data, reply)
	})

	// Subscribe to MCP discovery requests (load-balanced across workers).
	natsClient.QueueSubscribeReply(messaging.SubjectMCPDiscovery, messaging.QueueAgentWorkers, func(data []byte, reply func([]byte)) {
		handleMCPDiscoveryRequest(data, reply)
	})

	// Subscribe to MCP CI job execution (load-balanced across agent workers).
	// In distributed mode, MCP CI jobs are routed here because the frontend
	// cannot create MCP sessions (e.g., stdio servers using docker).
	natsClient.QueueSubscribe(messaging.SubjectJobsNew, messaging.QueueWorkers, func(data []byte) {
		handleMCPCIJob(data, apiURL, cmd.APIToken, natsClient)
	})

	xlog.Info("Agent worker ready, waiting for jobs", "subject", cmd.Subject, "queue", cmd.Queue)

	// Wait for shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	xlog.Info("Shutting down agent worker")
	cmd.deregister(nodeID)
	return nil
}

// registerWithFrontend registers the agent worker with the frontend.
// Returns the assigned node ID and an auto-provisioned API token.
func (cmd *AgentWorkerCMD) registerWithFrontend() (string, string, error) {
	nodeName := cmd.NodeName
	if nodeName == "" {
		hostname, _ := os.Hostname()
		nodeName = "agent-" + hostname
	}

	body := map[string]any{
		"name":      nodeName,
		"node_type": "agent",
	}
	if cmd.RegistrationToken != "" {
		body["token"] = cmd.RegistrationToken
	}

	const maxRetries = 10
	backoff := 2 * time.Second
	maxBackoff := 30 * time.Second

	var nodeID, apiToken string
	var err error

	for attempt := 1; attempt <= maxRetries; attempt++ {
		nodeID, apiToken, err = cmd.doRegister(body)
		if err == nil {
			return nodeID, apiToken, nil
		}
		if attempt == maxRetries {
			return "", "", fmt.Errorf("failed after %d attempts: %w", maxRetries, err)
		}
		xlog.Warn("Registration failed, retrying", "attempt", attempt, "next_retry", backoff, "error", err)
		time.Sleep(backoff)
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
	return nodeID, apiToken, err
}

func (cmd *AgentWorkerCMD) doRegister(body map[string]any) (string, string, error) {
	jsonBody, _ := json.Marshal(body)
	url := strings.TrimRight(cmd.RegisterTo, "/") + "/api/node/register"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if cmd.RegistrationToken != "" {
		req.Header.Set("Authorization", "Bearer "+cmd.RegistrationToken)
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", "", fmt.Errorf("posting to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("registration failed with status %d", resp.StatusCode)
	}

	var result struct {
		ID       string `json:"id"`
		APIToken string `json:"api_token,omitempty"` // auto-provisioned for agent workers
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("decoding response: %w", err)
	}
	return result.ID, result.APIToken, nil
}

// heartbeatLoop sends periodic heartbeats to the frontend.
func (cmd *AgentWorkerCMD) heartbeatLoop(nodeID string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	url := strings.TrimRight(cmd.RegisterTo, "/") + "/api/node/" + nodeID + "/heartbeat"
	client := &http.Client{Timeout: 5 * time.Second}

	for range ticker.C {
		body, _ := json.Marshal(map[string]any{})
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if cmd.RegistrationToken != "" {
			req.Header.Set("Authorization", "Bearer "+cmd.RegistrationToken)
		}
		resp, err := client.Do(req)
		if err != nil {
			xlog.Warn("Heartbeat failed", "error", err)
			continue
		}
		resp.Body.Close()
	}
}

// deregister removes the agent worker from the frontend registry.
func (cmd *AgentWorkerCMD) deregister(nodeID string) {
	url := strings.TrimRight(cmd.RegisterTo, "/") + "/api/node/" + nodeID + "/deregister"
	body, _ := json.Marshal(map[string]any{})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if req != nil {
		req.Header.Set("Content-Type", "application/json")
		if cmd.RegistrationToken != "" {
			req.Header.Set("Authorization", "Bearer "+cmd.RegistrationToken)
		}
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			xlog.Warn("Failed to deregister", "error", err)
			return
		}
		resp.Body.Close()
	}
}

// handleMCPToolRequest handles a NATS request-reply for MCP tool execution.
// The worker creates/caches MCP sessions from the serialized config and executes the tool.
func handleMCPToolRequest(data []byte, reply func([]byte)) {
	var req mcpRemote.MCPToolRequest
	if err := json.Unmarshal(data, &req); err != nil {
		sendMCPToolReply(reply, "", fmt.Sprintf("unmarshal error: %v", err))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 360*time.Second)
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

// natsClientAdapter wraps messaging.Client to satisfy NATSClient interface.
type natsClientAdapter struct {
	client *messaging.Client
}

func (a *natsClientAdapter) Publish(subject string, data any) error {
	return a.client.Publish(subject, data)
}

func (a *natsClientAdapter) QueueSubscribe(subject, queue string, handler func(data []byte)) (agents.NATSSub, error) {
	return a.client.QueueSubscribe(subject, queue, handler)
}

// handleMCPCIJob processes an MCP CI job on the agent worker.
// The agent worker can create MCP sessions (has docker) and call the LocalAI API for inference.
func handleMCPCIJob(data []byte, apiURL, apiToken string, natsClient *messaging.Client) {
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
		json.Unmarshal([]byte(task.CronParametersJSON), &params)
		for k, v := range params {
			prompt = strings.ReplaceAll(prompt, "{{."+k+"}}", v)
		}
	}
	if job.ParametersJSON != "" {
		var params map[string]string
		json.Unmarshal([]byte(job.ParametersJSON), &params)
		for k, v := range params {
			prompt = strings.ReplaceAll(prompt, "{{."+k+"}}", v)
		}
	}

	// Create LLM client pointing back to the frontend API
	llm := clients.NewLocalAILLM(task.Model, apiToken, apiURL)

	// Build cogito options
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
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

func publishJobStatus(nc *messaging.Client, jobID, status, message string) {
	nc.Publish(messaging.SubjectJobResult(jobID), jobs.JobResultEvent{
		JobID:  jobID,
		Status: status,
	})
	nc.Publish(messaging.SubjectJobProgress(jobID), jobs.ProgressEvent{
		JobID: jobID, Status: status, Message: message,
	})
}

func publishJobResult(nc *messaging.Client, jobID, status, result, errMsg string) {
	nc.Publish(messaging.SubjectJobResult(jobID), jobs.JobResultEvent{
		JobID:  jobID,
		Status: status,
		Result: result,
		Error:  errMsg,
	})
	nc.Publish(messaging.SubjectJobProgress(jobID), jobs.ProgressEvent{
		JobID:   jobID,
		Status:  status,
		Message: errMsg,
	})
}

