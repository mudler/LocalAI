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
	"github.com/mudler/cogito"
	"github.com/mudler/cogito/clients"
	"github.com/mudler/xlog"
)

// AgentWorkerCMD starts a dedicated agent worker process for distributed mode.
// It registers with the frontend and serves agent execution and MCP CI runs as
// STREAMING CONTROL VERBS on the tunnel it holds. The worker is a pure
// executor: it receives the full agent config and skills in the request body,
// so it does not need direct database access.
//
// It joins no queue group, and there is none left to join: a queue group only
// ever selected one consumer out of a set, the frontend makes that selection
// itself, and the work it hands over is a row it claimed on the job store.
//
// It also holds one tunnel to the frontend, so the frontend can reach its
// control verbs by RPC without the worker opening an inbound port. No verb the
// frontend addresses to THIS worker travels on the bus any more.
//
// It dials NO message bus. The last family that needed one was
// agent.<name>.cancel, which ran the other way, from a frontend replica to
// whichever worker held the execution; it is now a control verb on this
// worker's own tunnel (workerctl.PathAgentCancel), so a cancel reaches the
// worker running the agent without either side touching a broker.
//
// Usage:
//
//	localai agent-worker --register-to http://localai:8080
type AgentWorkerCMD struct {
	// NatsURL is accepted and ignored, exactly as the backend worker's is (see
	// core/services/worker/config.go). An agent worker connects to no message
	// bus: every verb a frontend addresses to it arrives on the tunnel it
	// dials, and a cancel now arrives the same way. It stays here, without
	// required, so an existing command line or unit file that still carries
	// --nats-url starts rather than failing to parse.
	NatsURL string `env:"LOCALAI_NATS_URL" help:"Ignored. An agent worker connects to no message bus; the frontend reaches it over its outbound tunnel. Accepted so an existing worker command line still starts." group:"distributed" hidden:""`

	// Registration (required)
	RegisterTo        string `env:"LOCALAI_REGISTER_TO" required:"" help:"Frontend URL for registration" group:"registration"`
	NodeName          string `env:"LOCALAI_NODE_NAME" help:"Node name for registration (defaults to hostname)" group:"registration"`
	RegistrationToken string `env:"LOCALAI_REGISTRATION_TOKEN" help:"Token for authenticating with the frontend" group:"registration"`
	HeartbeatInterval string `env:"LOCALAI_HEARTBEAT_INTERVAL" default:"10s" help:"Interval between heartbeats" group:"registration"`

	// API access
	APIURL   string `env:"LOCALAI_API_URL" help:"LocalAI API URL for inference (auto-derived from RegisterTo if not set)" group:"api"`
	APIToken string `env:"LOCALAI_API_TOKEN" help:"API token for LocalAI inference (auto-provisioned during registration if not set)" group:"api"`

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
	xlog.Info("Starting agent worker", "register_to", cmd.RegisterTo)

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

	// Register, and obtain this node's identity and its tunnel credential.
	//
	// The manager is still the NATS credential manager and still gated on the
	// NATS auth flags, and that is deliberate rather than left over: what the
	// gate decides is whether registration WAITS THROUGH ADMIN APPROVAL instead
	// of returning a pending response, and that behaviour is unchanged by this
	// worker no longer dialling a bus. What it no longer does is dial one: the
	// only value read off it below is TunnelToken.
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

	// The executor and the event bridge the control plane serves, built BEFORE
	// the tunnel because a verb mounted with a nil handler answers a 404, which
	// a frontend reads as a worker too old to serve it.
	//
	// No ConfigProvider and no SkillStore: config and skills arrive in the
	// request body, exactly as they arrived in the job payload before, because
	// an agent worker still has no database.
	eventBridge := agents.NewWorkerEventBridge("agent-worker-" + nodeID)
	executor := agents.NewWorkerExecutor(eventBridge, nil, apiURL, cmd.APIToken)

	mcpCIJobTimeout, err := time.ParseDuration(cmd.MCPCIJobTimeout)
	if err != nil && cmd.MCPCIJobTimeout != "" {
		xlog.Warn("invalid MCP CI job timeout, using default 10m", "input", cmd.MCPCIJobTimeout, "error", err)
	}
	mcpCIJobTimeout = cmp.Or(mcpCIJobTimeout, config.DefaultMCPCIJobTimeout)

	// The tunnel, and the loopback control plane behind it.
	//
	// It is now the ONLY way anything the frontend addresses to THIS worker
	// arrives: MCP tool execution, MCP discovery, backend.stop, agent execution
	// and MCP CI runs. Every one of their subjects is gone. The two queue
	// groups went last, because a queue group was only ever a way of SELECTING
	// a worker: the frontend makes that selection itself
	// (nodes.AgentSelector) and hands over a claim it took off the job store.
	//
	// The worker opens no inbound port for any of it: it dials out and the
	// control plane rides the tunnel it holds.
	//
	// The credential is read through credMgr rather than captured from res,
	// because every registration the manager performs ROTATES it: the frontend
	// stores only the hash of the newest one, so a captured value would lock
	// this worker out of its own tunnel after any re-registration.
	//
	// It is started AFTER registration, which is what supplies both the node
	// identity the dial names and the credential it presents.
	agentCtl, err := agentworker.Start(shutdownCtx, agentworker.Options{
		FrontendURL:  cmd.RegisterTo,
		NodeID:       nodeID,
		TunnelToken:  credMgr.TunnelToken,
		ControlToken: cmd.RegistrationToken,
		Handlers:     agentWorkerControlHandlers(executor, apiURL, cmd.APIToken, mcpCIJobTimeout),
	})
	if err != nil {
		return fmt.Errorf("starting the agent worker control plane: %w", err)
	}
	defer func() {
		if err := agentCtl.Close(); err != nil {
			xlog.Warn("Closing the agent worker tunnel failed", "error", err)
		}
	}()

	xlog.Info("Agent worker ready, serving agent execution and MCP CI runs on its tunnel", "node", nodeID)

	// Wait for an OS signal. There is no internal fatal condition left to wait
	// on: the one that existed was a NATS credential this worker could no
	// longer renew, and it renews none.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	xlog.Info("Shutting down agent worker")
	shutdownCancel() // stop heartbeat loop immediately
	mcpTools.CloseAllMCPSessions()
	regClient.GracefulDeregister(nodeID)
	return nil
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
//
// Everything it publishes now goes onto pub, which is the response body of the
// control verb rather than the bus. The subjects are unchanged, and they are
// still what the claiming replica checks against this worker's allow list
// before re-broadcasting, so an SSE stream open on any replica still sees the
// same events on the same subjects.
//
// It returns the terminal answer rather than only publishing it. That is the
// structural half of the fix: the claiming replica persists this before it
// releases the claim, so a job that finished on a worker cannot be left
// `running` because a result message went to a subject nobody was reading.
func handleMCPCIJob(ctx context.Context, data []byte, apiURL, apiToken string, pub messaging.Publisher, jobTimeout time.Duration) jobs.ClaimReply {
	var evt jobs.JobEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		xlog.Error("Failed to unmarshal job event", "error", err)
		return jobs.ClaimReply{Status: "failed", Error: "unreadable job event"}
	}

	job := evt.Job
	task := evt.Task
	if job == nil || task == nil {
		xlog.Error("MCP CI job missing enriched data", "jobID", evt.JobID)
		return mcpCIAnswer(pub, evt.JobID, "failed", "", "job or task data missing from NATS event")
	}

	modelCfg := evt.ModelConfig
	if modelCfg == nil {
		return mcpCIAnswer(pub, evt.JobID, "failed", "", "model config missing from job event")
	}

	xlog.Info("Processing MCP CI job", "jobID", evt.JobID, "taskID", evt.TaskID, "model", task.Model)

	// Publish running status
	dropTrace(pub.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
		JobID: evt.JobID, Status: "running", Message: "Job started on agent worker",
	}), evt.JobID)

	// Parse MCP config
	if modelCfg.MCP.Servers == "" && modelCfg.MCP.Stdio == "" {
		return mcpCIAnswer(pub, evt.JobID, "failed", "", "no MCP servers configured for model")
	}

	remote, stdio, err := modelCfg.MCP.MCPConfigFromYAML()
	if err != nil {
		return mcpCIAnswer(pub, evt.JobID, "failed", "", fmt.Sprintf("failed to parse MCP config: %v", err))
	}

	// Create MCP sessions locally (agent worker has docker)
	sessions, err := mcpTools.SessionsFromMCPConfig(modelCfg.Name, remote, stdio)
	if err != nil || len(sessions) == 0 {
		errMsg := "no working MCP servers found"
		if err != nil {
			errMsg = fmt.Sprintf("failed to create MCP sessions: %v", err)
		}
		return mcpCIAnswer(pub, evt.JobID, "failed", "", errMsg)
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
	ctx, cancel := context.WithTimeout(ctx, jobTimeout)
	defer cancel()

	// Update job status to running in DB
	publishJobStatus(pub, evt.JobID, "running", "")

	// Buffer stream tokens and flush as complete blocks
	var reasoningBuf, contentBuf strings.Builder
	var lastStreamType cogito.StreamEventType

	flushStreamBuf := func() {
		if reasoningBuf.Len() > 0 {
			dropTrace(pub.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
				JobID: evt.JobID, TraceType: "reasoning", TraceContent: reasoningBuf.String(),
			}), evt.JobID)
			reasoningBuf.Reset()
		}
		if contentBuf.Len() > 0 {
			dropTrace(pub.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
				JobID: evt.JobID, TraceType: "content", TraceContent: contentBuf.String(),
			}), evt.JobID)
			contentBuf.Reset()
		}
	}

	cogitoOpts := modelCfg.BuildCogitoOptions()
	cogitoOpts = append(cogitoOpts,
		cogito.WithContext(ctx),
		cogito.WithMCPs(sessions...),
		cogito.WithStatusCallback(func(status string) {
			flushStreamBuf()
			dropTrace(pub.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
				JobID: evt.JobID, TraceType: "status", TraceContent: status,
			}), evt.JobID)
		}),
		cogito.WithToolCallResultCallback(func(t cogito.ToolStatus) {
			flushStreamBuf()
			dropTrace(pub.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
				JobID: evt.JobID, TraceType: "tool_result", TraceContent: fmt.Sprintf("%s: %s", t.Name, t.Result),
			}), evt.JobID)
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
				dropTrace(pub.Publish(messaging.SubjectJobProgress(evt.JobID), jobs.ProgressEvent{
					JobID: evt.JobID, TraceType: "tool_call", TraceContent: fmt.Sprintf("%s(%s)", ev.ToolName, ev.ToolArgs),
				}), evt.JobID)
			}
		}),
	)

	// Execute via cogito
	fragment := cogito.NewEmptyFragment()
	fragment = fragment.AddMessage("user", prompt)

	f, err := cogito.ExecuteTools(llm, fragment, cogitoOpts...)
	flushStreamBuf() // flush any remaining buffered tokens

	if err != nil {
		return mcpCIAnswer(pub, evt.JobID, "failed", "", fmt.Sprintf("cogito execution failed: %v", err))
	}

	result := ""
	if msg := f.LastMessage(); msg != nil {
		result = msg.Content
	}
	xlog.Info("MCP CI job completed", "jobID", evt.JobID, "resultLen", len(result))
	return mcpCIAnswer(pub, evt.JobID, "completed", result, "")
}

// dropTrace logs a progress line that could not be written, and never returns
// it. A progress line is a NOTIFICATION about a run; a failure to write one says
// nothing about the run, and returning it would make the claiming replica read
// a finished job as a verb this worker could not serve.
func dropTrace(err error, jobID string) {
	if err != nil {
		xlog.Debug("An MCP CI progress line could not be written", "jobID", jobID, "error", err)
	}
}

// mcpCIAnswer is the ONE place an MCP CI run's terminal state is stated.
//
// It says it TWICE and on purpose, on two carriers with different jobs. The
// publish drives the SSE streams, on the same jobs.<id>.result subject it always
// used, re-broadcast by the claiming replica after its allow-list check. The
// returned reply is the answer to the control verb, which the claiming replica
// persists BEFORE it releases the claim; that is what makes a finished job
// impossible to leave `running`, where the publish alone could reach nobody.
func mcpCIAnswer(pub messaging.Publisher, jobID, status, result, errMsg string) jobs.ClaimReply {
	jobs.PublishJobResult(pub, jobID, status, result, errMsg)
	return jobs.ClaimReply{JobID: jobID, Status: status, Result: result, Error: errMsg}
}

func publishJobStatus(pub messaging.Publisher, jobID, status, message string) {
	jobs.PublishJobProgress(pub, jobID, status, message)
}

// agentWorkerControlHandlers is every verb this worker serves on the tunnel it
// holds to the frontend.
//
// It is a function rather than a literal inside the start-up path so that a
// spec can stand the same set up and post to it. Each of these is the ONLY
// carrier for its verb: the queue subjects the two MCP verbs arrived on and the
// node subject backend.stop arrived on are all gone, so a field silently
// dropped here is a 404 at runtime, which the frontend reads as a worker too
// old to serve the verb.
func agentWorkerControlHandlers(executor *agents.WorkerExecutor, apiURL, apiToken string, mcpCITimeout time.Duration) agentworker.Config {
	return agentworker.Config{
		MCPTool:      serveMCPToolRequest,
		MCPDiscovery: serveMCPDiscoveryRequest,
		// The verb that removed this process's last reason to dial a bus. It
		// reaches the SAME cancel registry the executor registers a run on,
		// because it is the same bridge: a cancel arriving on the tunnel has to
		// find an execution that is publishing onto a control stream.
		AgentCancel: executor.Cancel,
		// Drops the MCP sessions cached for a backend that went away, on the
		// path a backend worker serves by killing the process instead.
		BackendStop: dropMCPSessionsForBackend,
		// The two verbs that replace the queue groups. Both STREAM: their
		// progress, their agent events and their terminal answer all travel on
		// the response body the claiming replica is already reading, which is
		// what lets that replica persist the terminal line before it releases
		// the claim.
		AgentExecute: executor.Execute,
		MCPCIRun:     serveMCPCIRun(apiURL, apiToken, mcpCITimeout),
	}
}

// serveMCPCIRun answers workerctl.PathMCPCIRun.
//
// The handler's ctx is the REQUEST's, not this process's shutdown context, and
// that is the point: when the claiming replica goes away the response body dies
// with it, the run stops, and the claim is reaped for another replica to take.
// Bound to this worker's shutdown context instead, the run would keep going
// with nobody reading it.
func serveMCPCIRun(apiURL, apiToken string, jobTimeout time.Duration) agentworker.StreamHandler {
	return func(ctx context.Context, raw json.RawMessage, pub messaging.Publisher) (json.RawMessage, error) {
		return json.Marshal(handleMCPCIJob(ctx, raw, apiURL, apiToken, pub, jobTimeout))
	}
}
