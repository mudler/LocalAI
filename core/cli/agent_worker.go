package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/sanitize"
	"github.com/mudler/xlog"
)

// AgentWorkerCMD starts a dedicated worker process for distributed mode. It
// registers with the frontend and serves the MCP tool calls and discovery
// requests of the frontend over NATS, because the frontend cannot start the
// stdio MCP servers of a model (for example, ones that need docker). It needs
// no database access.
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

	// Removed: the worker no longer runs agents or jobs. These are accepted and
	// ignored so a deployment that sets them keeps starting.
	APIURL          string `env:"LOCALAI_API_URL" hidden:"" help:"Removed; ignored"`
	APIToken        string `env:"LOCALAI_API_TOKEN" hidden:"" help:"Removed; ignored"`
	Subject         string `env:"LOCALAI_AGENT_SUBJECT" hidden:"" help:"Removed; ignored"`
	Queue           string `env:"LOCALAI_AGENT_QUEUE" hidden:"" help:"Removed; ignored"`
	MCPCIJobTimeout string `env:"LOCALAI_MCP_CI_JOB_TIMEOUT" hidden:"" help:"Removed; ignored"`

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
}

// natsAuthRequired reports whether NATS JWT credentials must be present — the
// granular flag or the umbrella (LOCALAI_DISTRIBUTED_REQUIRE_AUTH).
func (cmd *AgentWorkerCMD) natsAuthRequired() bool {
	return cmd.NatsRequireAuth || cmd.DistributedRequireAuth
}

func (cmd *AgentWorkerCMD) Run(ctx *cliContext.Context) error {
	xlog.Info("Starting agent worker", "nats", sanitize.URL(cmd.NatsURL), "register_to", cmd.RegisterTo)

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

	xlog.Info("Agent worker ready")

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
	mcpTools.CloseAllMCPSessions()
	regClient.GracefulDeregister(nodeID)
	return runErr
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
