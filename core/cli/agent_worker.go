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
	"github.com/mudler/LocalAI/core/services/agentworker"
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
// It also holds one tunnel to the frontend, so the frontend can reach its MCP
// verbs by RPC without the worker opening an inbound port. The tunnel is an
// ADDITION: --nats-url is still required, and every job, every fan-out event
// and the node backend.stop subject still travel on the bus.
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

	// Context cancelled on shutdown — used by registration waits, heartbeat, and
	// other background goroutines.
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	// Acquire credentials via (re)registration. When the bus requires auth and no
	// static fallback is configured, wait through admin approval until the
	// frontend mints credentials rather than starting unauthenticated.
	credMgr := workerregistry.NewNATSCredentialManager(
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

	// The tunnel, and the loopback control plane behind it.
	//
	// This is now the ONLY way MCP tool execution and discovery reach this
	// worker: their queue-group subjects are gone, because a queue group was
	// only ever a way of SELECTING a worker and the frontend makes that
	// selection itself (nodes.AgentSelector). The remaining verbs below still
	// arrive on NATS and will until the tasks that move them land.
	//
	// The worker opens no inbound port for any of it: it dials out and the
	// control plane rides the tunnel it holds.
	//
	// The credential is read through credMgr rather than captured from res,
	// because every re-registration the manager performs ROTATES it and a
	// captured value would lock this worker out of its own tunnel at the first
	// JWT refresh.
	//
	// It is started AFTER registration, which is what supplies both the node
	// identity the dial names and the credential it presents, and BEFORE the
	// remaining NATS subscriptions, so that a frontend that reaches this worker
	// over the tunnel finds its verbs mounted rather than a 404 it would read
	// as a version skew.
	agentCtl, err := agentworker.Start(shutdownCtx, agentworker.Options{
		FrontendURL:  cmd.RegisterTo,
		NodeID:       nodeID,
		TunnelToken:  credMgr.TunnelToken,
		ControlToken: cmd.RegistrationToken,
		Handlers: agentworker.Config{
			MCPTool:      serveMCPToolRequest,
			MCPDiscovery: serveMCPDiscoveryRequest,
			// The same cleanup the nodes.<id>.backend.stop subscription below
			// performs, reachable over the tunnel on the path a backend worker
			// already serves. Both are live: the subject is what the frontend
			// still publishes on, and it is a later task that removes it.
			BackendStop: dropMCPSessionsForBackend,
		},
	})
	if err != nil {
		return fmt.Errorf("starting the agent worker control plane: %w", err)
	}
	defer func() {
		if err := agentCtl.Close(); err != nil {
			xlog.Warn("Closing the agent worker tunnel failed", "error", err)
		}
	}()

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
	//
	// It runs BESIDE the tunnel route mounted above, not instead of it, and
	// both call dropMCPSessionsForBackend. The subject is still what the
	// frontend publishes on; a later task is what moves it. Two carriers, one
	// implementation, so which one delivered cannot change what happened.
	if _, err := natsClient.Subscribe(messaging.SubjectNodeBackendStop(nodeID), func(data []byte) {
		var req messaging.BackendStopRequest
		if err := json.Unmarshal(data, &req); err != nil {
			xlog.Warn("Agent worker could not decode a backend stop event", "error", err)
			return
		}
		if err := dropMCPSessionsForBackend(context.Background(), req); err != nil {
			xlog.Warn("Agent worker could not drop the MCP sessions of a stopped backend", "error", err)
		}
	}); err != nil {
		return fmt.Errorf("subscribing to %s: %w", messaging.SubjectNodeBackendStop(nodeID), err)
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

// The MCP verbs, written ONCE and served on two carriers.
//
// The bus subscription and the tunnel's control route both call the same
// serve* function and both send the same bytes, so a worker reached either way
// answers identically. Two implementations of one verb is the shape that lets a
// deployment behave differently depending on which carrier a frontend happened
// to pick, and there is no version of this migration in which that is
// acceptable: for the whole of it, both carriers are live at once.
//
// The distinction the return type carries: an MCP tool that RAN and failed is
// this worker's own answer and travels as bytes with an error field set, on a
// 200. A returned error is this worker failing to serve the verb at all, which
// becomes a non-2xx over the tunnel and nothing the frontend may act on.

// dropMCPSessionsForBackend closes the MCP sessions this worker cached for a
// backend that is going away.
//
// It is the agent worker's whole implementation of backend.stop, and it is
// deliberately nothing like the backend worker's, which kills the process and
// recycles its port. An agent worker runs no backend processes; what it holds
// are sessions that were created against one.
//
// A backend nobody named is a no-op rather than an error. The event carries the
// name, and a request without one asks this worker to forget nothing in
// particular; failing it would put a malformed publish into the bucket the
// frontend reads as a worker that could not be reached.
func dropMCPSessionsForBackend(_ context.Context, req messaging.BackendStopRequest) error {
	if req.Backend == "" {
		return nil
	}
	mcpTools.CloseMCPSessions(req.Backend)
	return nil
}

// serveMCPToolRequest answers an MCP tool execution request.
func serveMCPToolRequest(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return encodeMCPReply(runMCPTool(ctx, raw))
}

// runMCPTool creates or reuses the named MCP sessions from the request's config
// and executes the named tool against them.
//
// Every failure inside it is an answer rather than an error, because every one
// of them is something this worker LEARNED by trying: a config it could not
// build sessions from, a discovery that failed, a tool that returned an error.
func runMCPTool(ctx context.Context, raw json.RawMessage) mcpRemote.MCPToolResponse {
	var req mcpRemote.MCPToolRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return mcpRemote.MCPToolResponse{Error: fmt.Sprintf("unmarshal error: %v", err)}
	}

	// Bounded here rather than by the caller, so the bus path and the tunnel
	// path give a stuck MCP server the same budget.
	ctx, cancel := context.WithTimeout(ctx, config.DefaultMCPToolTimeout)
	defer cancel()

	namedSessions, err := mcpTools.NamedSessionsFromMCPConfig(req.ModelName, req.RemoteServers, req.StdioServers, nil)
	if err != nil {
		return mcpRemote.MCPToolResponse{Error: fmt.Sprintf("session error: %v", err)}
	}

	// Discover tools to find the right session
	tools, err := mcpTools.DiscoverMCPTools(ctx, namedSessions)
	if err != nil {
		return mcpRemote.MCPToolResponse{Error: fmt.Sprintf("discovery error: %v", err)}
	}

	argsJSON, _ := json.Marshal(req.Arguments)
	result, err := mcpTools.ExecuteMCPToolCall(ctx, tools, req.ToolName, string(argsJSON))
	if err != nil {
		return mcpRemote.MCPToolResponse{Error: err.Error()}
	}
	return mcpRemote.MCPToolResponse{Result: result}
}

// serveMCPDiscoveryRequest answers an MCP tool/prompt/resource discovery
// request.
func serveMCPDiscoveryRequest(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	return encodeMCPReply(runMCPDiscovery(ctx, raw))
}

// runMCPDiscovery lists the servers this worker can reach for a model, with
// their tools, prompts and resources.
func runMCPDiscovery(ctx context.Context, raw json.RawMessage) mcpRemote.MCPDiscoveryResponse {
	var req mcpRemote.MCPDiscoveryRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return mcpRemote.MCPDiscoveryResponse{Error: fmt.Sprintf("unmarshal error: %v", err)}
	}

	ctx, cancel := context.WithTimeout(ctx, config.DefaultMCPDiscoveryTimeout)
	defer cancel()

	namedSessions, err := mcpTools.NamedSessionsFromMCPConfig(req.ModelName, req.RemoteServers, req.StdioServers, nil)
	if err != nil {
		return mcpRemote.MCPDiscoveryResponse{Error: fmt.Sprintf("session error: %v", err)}
	}

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

	var servers []mcpRemote.MCPServerInfo
	for _, srv := range serverInfos {
		servers = append(servers, mcpRemote.MCPServerInfo{
			Name:      srv.Name,
			Type:      srv.Type,
			Tools:     srv.Tools,
			Prompts:   srv.Prompts,
			Resources: srv.Resources,
			Error:     srv.Error,
		})
	}
	return mcpRemote.MCPDiscoveryResponse{Servers: servers, Tools: toolDefs}
}

// encodeMCPReply turns a verb's answer into the bytes both carriers send.
//
// A marshalling failure is the one thing here that is NOT an answer: this
// worker has said nothing about the request, so it is returned as an error and
// becomes a non-2xx over the tunnel rather than an empty 200.
func encodeMCPReply(resp any) (json.RawMessage, error) {
	out, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("encoding the reply: %w", err)
	}
	return out, nil
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
