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

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/agents"
	"github.com/mudler/LocalAI/core/services/jobs"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/internal"
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
	Subject string `env:"LOCALAI_AGENT_SUBJECT" default:"agent.execute" help:"NATS subject for agent execution. Must be a served subject (use the agent root, for example agent.execute); an unserved root is refused at startup" group:"distributed"`
	Queue   string `env:"LOCALAI_AGENT_QUEUE" default:"agent-workers" help:"NATS queue group name" group:"distributed"`

	NatsJWT         string `env:"LOCALAI_NATS_JWT" help:"NATS user JWT override (defaults to nats_jwt from registration)" group:"distributed"`
	NatsUserSeed    string `env:"LOCALAI_NATS_USER_SEED" help:"NATS user seed override (defaults to nats_user_seed from registration)" group:"distributed"`
	NatsServiceJWT  string `env:"LOCALAI_NATS_SERVICE_JWT" help:"Fallback NATS service JWT when registration does not mint agent JWT" group:"distributed"`
	NatsServiceSeed string `env:"LOCALAI_NATS_SERVICE_SEED" help:"Fallback NATS service seed paired with LOCALAI_NATS_SERVICE_JWT" group:"distributed"`
	NatsRequireAuth bool   `env:"LOCALAI_NATS_REQUIRE_AUTH" default:"false" help:"Require NATS JWT+seed to connect" group:"distributed"`
	// DistributedRequireAuth is the umbrella switch; for the agent worker (which
	// has no file-transfer server) it implies NATS auth is required.
	DistributedRequireAuth bool   `env:"LOCALAI_DISTRIBUTED_REQUIRE_AUTH" default:"false" help:"Umbrella switch implying --nats-require-auth (agent workers have no file-transfer server)" group:"distributed"`
	NatsTLSCA              string `env:"LOCALAI_NATS_TLS_CA" type:"existingfile" help:"PEM file for NATS server CA (private PKI)" group:"distributed"`
	NatsTLSCert            string `env:"LOCALAI_NATS_TLS_CERT" type:"existingfile" help:"Client certificate for NATS mTLS" group:"distributed"`
	NatsTLSKey             string `env:"LOCALAI_NATS_TLS_KEY" type:"existingfile" help:"Client private key for NATS mTLS" group:"distributed"`

	// Timeouts
	MCPCIJobTimeout string `env:"LOCALAI_MCP_CI_JOB_TIMEOUT" default:"10m" help:"Timeout for MCP CI job execution" group:"distributed"`
}

// natsAuthRequired reports whether NATS JWT credentials must be present — the
// granular flag or the umbrella (LOCALAI_DISTRIBUTED_REQUIRE_AUTH).
func (cmd *AgentWorkerCMD) natsAuthRequired() bool {
	return cmd.NatsRequireAuth || cmd.DistributedRequireAuth
}

// validateAgentSubject refuses a subject no carrier serves before the worker
// registers or connects. The messaging client refuses it anyway at subscribe
// time, but by then the worker has registered and the error does not name the
// setting the operator has to change.
func validateAgentSubject(subject string) error {
	if err := messaging.ValidateSubject(subject); err != nil {
		return fmt.Errorf("LOCALAI_AGENT_SUBJECT %q must be a served subject (use the agent root, for example %s): %w",
			subject, messaging.SubjectAgentExecute, err)
	}
	return nil
}

func (cmd *AgentWorkerCMD) Run(ctx *cliContext.Context) error {
	if err := validateAgentSubject(cmd.Subject); err != nil {
		return err
	}
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
		"version":   internal.Version,
		"commit":    internal.Commit,
	}
	if cmd.RegistrationToken != "" {
		registrationBody["token"] = cmd.RegistrationToken
	}

	// Context cancelled on shutdown — used by registration waits, heartbeat, and
	// other background goroutines.
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	// Acquire credentials via (re)registration. When the bus requires auth and no
	// static fallback is configured, wait through admin approval until the
	// frontend mints credentials rather than starting unauthenticated.
	credMgr := workerregistry.NewCredentialManager(
		func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
			return regClient.RegisterFull(ctx, registrationBody)
		},
		cmd.natsAuthRequired() && cmd.NatsJWT == "" && cmd.NatsServiceJWT == "",
	)
	res, err := credMgr.Acquire(shutdownCtx)
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	nodeID := res.ID
	xlog.Info("Registered with frontend", "nodeID", nodeID, "frontend", cmd.RegisterTo)

	// Use provisioned API token if none was set
	if cmd.APIToken == "" {
		cmd.APIToken = res.APIToken
	}

	// Start heartbeat
	heartbeatInterval, err := time.ParseDuration(cmd.HeartbeatInterval)
	if err != nil && cmd.HeartbeatInterval != "" {
		xlog.Warn("invalid heartbeat interval, using default 10s", "input", cmd.HeartbeatInterval, "error", err)
	}
	heartbeatInterval = cmp.Or(heartbeatInterval, 10*time.Second)

	go regClient.HeartbeatLoop(shutdownCtx, nodeID, heartbeatInterval, func() map[string]any { return map[string]any{} })

	// Resolve NATS credentials with precedence: explicit env override, then
	// frontend-minted (auto-refreshed before expiry), then service fallback.
	// Each static source must supply JWT and seed together.
	natsTLS := messaging.TLSFiles{CA: cmd.NatsTLSCA, Cert: cmd.NatsTLSCert, Key: cmd.NatsTLSKey}
	var natsOpts []messaging.Option
	switch {
	case cmd.NatsJWT != "" || cmd.NatsUserSeed != "":
		if (cmd.NatsJWT == "") != (cmd.NatsUserSeed == "") {
			return fmt.Errorf("LOCALAI_NATS_JWT and LOCALAI_NATS_USER_SEED must be set together")
		}
		natsOpts = append(natsOpts, messaging.WithUserJWT(cmd.NatsJWT, cmd.NatsUserSeed))
	case credMgr.HasCredentials():
		natsOpts = append(natsOpts, messaging.WithUserJWTProvider(credMgr.Provider()))
		go func() {
			if err := credMgr.RefreshLoop(shutdownCtx); err != nil {
				xlog.Error("NATS credential refresh permanently failed; shutting down agent worker", "error", err)
				shutdownCancel()
			}
		}()
	case cmd.NatsServiceJWT != "" || cmd.NatsServiceSeed != "":
		if (cmd.NatsServiceJWT == "") != (cmd.NatsServiceSeed == "") {
			return fmt.Errorf("LOCALAI_NATS_SERVICE_JWT and LOCALAI_NATS_SERVICE_SEED must be set together")
		}
		natsOpts = append(natsOpts, messaging.WithUserJWT(cmd.NatsServiceJWT, cmd.NatsServiceSeed))
	case cmd.natsAuthRequired():
		return fmt.Errorf("NATS JWT+seed required: enable frontend minting or set LOCALAI_NATS_* env vars")
	}
	if natsTLS.Enabled() {
		natsOpts = append(natsOpts, messaging.WithTLS(natsTLS))
	}
	natsClient, err := messaging.New(cmd.NatsURL, natsOpts...)
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

	// One consumer serves both queued kinds; the route option only moves the
	// agent-run subject and group, which operators may set.
	work := messaging.NewNATSWorkConsumer(natsClient, messaging.WithAgentRunRoute(cmd.Subject, cmd.Queue))

	// Create and start the NATS dispatcher.
	// No ConfigProvider or SkillStore needed — config and skills arrive in the job payload.
	dispatcher := agents.NewNATSDispatcher(
		work,
		eventBridge,
		nil, // no ConfigProvider: config comes in the enriched NATS payload
		apiURL, cmd.APIToken,
		0, // no concurrency limit (CLI worker)
	)

	if err := dispatcher.Start(shutdownCtx); err != nil {
		return fmt.Errorf("starting dispatcher: %w", err)
	}

	var rpc agentRPCServer = nodes.NewNATSAgentRPCServer(natsClient, nodeID)

	// Serve MCP tool execution requests (load-balanced across workers).
	// The frontend routes model-level MCP tool calls here.
	if err := rpc.ServeMCPTool(handleMCPToolRequest); err != nil {
		return err
	}

	// Serve MCP discovery requests (load-balanced across workers).
	if err := rpc.ServeMCPDiscovery(handleMCPDiscoveryRequest); err != nil {
		return err
	}

	// Subscribe to MCP CI job execution (load-balanced across agent workers).
	// In distributed mode, MCP CI jobs are routed here because the frontend
	// cannot create MCP sessions (e.g., stdio servers using docker).
	mcpCIJobTimeout, err := time.ParseDuration(cmd.MCPCIJobTimeout)
	if err != nil && cmd.MCPCIJobTimeout != "" {
		xlog.Warn("invalid MCP CI job timeout, using default 10m", "input", cmd.MCPCIJobTimeout, "error", err)
	}
	mcpCIJobTimeout = cmp.Or(mcpCIJobTimeout, config.DefaultMCPCIJobTimeout)

	if _, err := startMCPCIConsumer(shutdownCtx, work, apiURL, cmd.APIToken, mcpCIJobTimeout); err != nil {
		return err
	}

	// Listen for backend stop events to clean up cached MCP sessions.
	// In the main application this is done via ml.OnModelUnload, but the agent
	// worker has no model loader, so it listens for the stop event instead.
	if err := rpc.ServeBackendStop(func(backend string) {
		if backend != "" {
			mcpTools.CloseMCPSessions(backend)
		}
	}); err != nil {
		return err
	}

	xlog.Info("Agent worker ready, waiting for jobs", "subject", cmd.Subject, "queue", cmd.Queue)

	// Wait for an OS signal or an internal fatal condition (e.g. NATS
	// credentials became unrenewable), so the worker restarts and re-acquires
	// rather than lingering unable to serve.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	var runErr error
	select {
	case <-sigCh:
	case <-shutdownCtx.Done():
		runErr = fmt.Errorf("agent worker shutting down: NATS credentials unavailable")
		xlog.Error("Internal shutdown requested", "error", runErr)
	}

	xlog.Info("Shutting down agent worker")
	shutdownCancel() // stop heartbeat loop immediately
	dispatcher.Stop()
	mcpTools.CloseAllMCPSessions()
	regClient.GracefulDeregister(nodeID)
	return runErr
}

// startMCPCIConsumer serves MCP CI jobs with maxInFlight 1, which keeps them
// one at a time per worker, run inline on the delivery, as they always were.
func startMCPCIConsumer(ctx context.Context, consumer messaging.WorkConsumer, apiURL, apiToken string, jobTimeout time.Duration) (messaging.Subscription, error) {
	return consumer.Consume(ctx, messaging.WorkMCPCI, 1, func(ctx context.Context, data []byte, events messaging.Publisher) error {
		return handleMCPCIJob(ctx, data, apiURL, apiToken, events, jobTimeout)
	})
}

// agentRPCServer is how the agent worker serves the frontend's MCP requests
// and hears the node's backend stop events.
type agentRPCServer interface {
	ServeMCPTool(h mcpRemote.ToolHandler) error
	ServeMCPDiscovery(h mcpRemote.DiscoveryHandler) error
	ServeBackendStop(h func(backend string)) error
}

// handleMCPToolRequest executes one MCP tool call. The worker creates/caches
// MCP sessions from the serialized config and executes the tool.
func handleMCPToolRequest(parent context.Context, req mcpRemote.MCPToolRequest) mcpRemote.MCPToolResponse {
	ctx, cancel := context.WithTimeout(parent, config.DefaultMCPToolTimeout)
	defer cancel()

	// Create/cache named MCP sessions from the provided config
	namedSessions, err := mcpTools.NamedSessionsFromMCPConfig(req.ModelName, req.RemoteServers, req.StdioServers, nil)
	if err != nil {
		return mcpRemote.MCPToolResponse{Error: fmt.Sprintf("session error: %v", err)}
	}

	// Discover tools to find the right session
	tools, err := mcpTools.DiscoverMCPTools(ctx, namedSessions)
	if err != nil {
		return mcpRemote.MCPToolResponse{Error: fmt.Sprintf("discovery error: %v", err)}
	}

	// Execute the tool
	argsJSON, _ := json.Marshal(req.Arguments)
	result, err := mcpTools.ExecuteMCPToolCall(ctx, tools, req.ToolName, string(argsJSON))
	if err != nil {
		return mcpRemote.MCPToolResponse{Error: err.Error()}
	}

	return mcpRemote.MCPToolResponse{Result: result}
}

// handleMCPDiscoveryRequest lists a model's MCP tools, prompts and resources.
func handleMCPDiscoveryRequest(parent context.Context, req mcpRemote.MCPDiscoveryRequest) mcpRemote.MCPDiscoveryResponse {
	ctx, cancel := context.WithTimeout(parent, config.DefaultMCPDiscoveryTimeout)
	defer cancel()

	// Create/cache named MCP sessions
	namedSessions, err := mcpTools.NamedSessionsFromMCPConfig(req.ModelName, req.RemoteServers, req.StdioServers, nil)
	if err != nil {
		return mcpRemote.MCPDiscoveryResponse{Error: fmt.Sprintf("session error: %v", err)}
	}

	// List servers with their tools/prompts/resources
	serverInfos, err := mcpTools.ListMCPServers(ctx, namedSessions)
	if err != nil {
		return mcpRemote.MCPDiscoveryResponse{Error: fmt.Sprintf("list error: %v", err)}
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
			Error:     s.Error,
		})
	}

	return mcpRemote.MCPDiscoveryResponse{Servers: servers, Tools: toolDefs}
}

// handleMCPCIJob processes an MCP CI job on the agent worker.
// The agent worker can create MCP sessions (has docker) and call the LocalAI API for inference.
// Every outcome, failures included, is reported on events or logged, so it
// always returns nil: a carrier that redelivers on error would only repeat it.
func handleMCPCIJob(shutdownCtx context.Context, data []byte, apiURL, apiToken string, events messaging.Publisher, jobTimeout time.Duration) error {
	var evt jobs.JobEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		xlog.Error("Failed to unmarshal job event", "error", err)
		return nil
	}

	job := evt.Job
	task := evt.Task
	if job == nil || task == nil {
		xlog.Error("MCP CI job missing enriched data", "jobID", evt.JobID)
		publishJobResult(events, evt.JobID, "failed", "", "job or task data missing from NATS event")
		return nil
	}

	modelCfg := evt.ModelConfig
	if modelCfg == nil {
		publishJobResult(events, evt.JobID, "failed", "", "model config missing from job event")
		return nil
	}

	xlog.Info("Processing MCP CI job", "jobID", evt.JobID, "taskID", evt.TaskID, "model", task.Model)

	// Publish running status
	publishJobTrace(events, jobs.ProgressEvent{
		JobID: evt.JobID, Status: "running", Message: "Job started on agent worker",
	})

	// Parse MCP config
	if modelCfg.MCP.Servers == "" && modelCfg.MCP.Stdio == "" {
		publishJobResult(events, evt.JobID, "failed", "", "no MCP servers configured for model")
		return nil
	}

	remote, stdio, err := modelCfg.MCP.MCPConfigFromYAML()
	if err != nil {
		publishJobResult(events, evt.JobID, "failed", "", fmt.Sprintf("failed to parse MCP config: %v", err))
		return nil
	}

	// Create MCP sessions locally (agent worker has docker)
	sessions, err := mcpTools.SessionsFromMCPConfig(modelCfg.Name, remote, stdio)
	if err != nil || len(sessions) == 0 {
		errMsg := "no working MCP servers found"
		if err != nil {
			errMsg = fmt.Sprintf("failed to create MCP sessions: %v", err)
		}
		publishJobResult(events, evt.JobID, "failed", "", errMsg)
		return nil
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
	publishJobStatus(events, evt.JobID, "running", "")

	// Buffer stream tokens and flush as complete blocks
	var reasoningBuf, contentBuf strings.Builder
	var lastStreamType cogito.StreamEventType

	flushStreamBuf := func() {
		if reasoningBuf.Len() > 0 {
			publishJobTrace(events, jobs.ProgressEvent{
				JobID: evt.JobID, TraceType: "reasoning", TraceContent: reasoningBuf.String(),
			})
			reasoningBuf.Reset()
		}
		if contentBuf.Len() > 0 {
			publishJobTrace(events, jobs.ProgressEvent{
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
			publishJobTrace(events, jobs.ProgressEvent{
				JobID: evt.JobID, TraceType: "status", TraceContent: status,
			})
		}),
		cogito.WithToolCallResultCallback(func(t cogito.ToolStatus) {
			flushStreamBuf()
			publishJobTrace(events, jobs.ProgressEvent{
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
				publishJobTrace(events, jobs.ProgressEvent{
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
		publishJobResult(events, evt.JobID, "failed", "", fmt.Sprintf("cogito execution failed: %v", err))
		return nil
	}

	result := ""
	if msg := f.LastMessage(); msg != nil {
		result = msg.Content
	}
	publishJobResult(events, evt.JobID, "completed", result, "")
	xlog.Info("MCP CI job completed", "jobID", evt.JobID, "resultLen", len(result))
	return nil
}

func publishJobStatus(events messaging.Publisher, jobID, status, message string) {
	jobs.PublishJobProgress(events, jobID, status, message)
}

func publishJobResult(events messaging.Publisher, jobID, status, result, errMsg string) {
	jobs.PublishJobResult(events, jobID, status, result, errMsg)
}

// publishJobTrace sends a progress or trace line; a lost line must not fail
// the job, so the error is only logged.
func publishJobTrace(events messaging.Publisher, ev jobs.ProgressEvent) {
	if err := events.Publish(messaging.SubjectJobProgress(ev.JobID), ev); err != nil {
		xlog.Error("Failed to publish job progress", "jobID", ev.JobID, "error", err)
	}
}
