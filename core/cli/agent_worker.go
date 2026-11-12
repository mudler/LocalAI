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
	"github.com/mudler/LocalAI/core/services/agentworker"
	"github.com/mudler/LocalAI/core/services/cluster"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/worker"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/sanitize"
	"github.com/mudler/xlog"
)

// AgentWorkerCMD starts a dedicated worker process for distributed mode. It
// registers with the frontend, attaches to the carrier that the frontend names,
// and serves the MCP tool calls and discovery requests of the frontend, because
// the frontend cannot start the stdio MCP servers of a model (for example, ones
// that need docker). On NATS it subscribes to the MCP subjects. On the tunnel it
// holds one outbound connection to the frontend and serves the requests that
// arrive on it. It needs no database access.
//
// Usage:
//
//	localai agent-worker --register-to http://localai:8080
type AgentWorkerCMD struct {
	// NATS (optional: the frontend hands over the address)
	NatsURL string `env:"LOCALAI_NATS_URL" help:"NATS server URL. Optional: the frontend hands over the address when the cluster runs on NATS, and this overrides it" group:"distributed"`

	// Registration (required)
	RegisterTo        string `env:"LOCALAI_REGISTER_TO" required:"" help:"Frontend URL for registration" group:"registration"`
	NodeName          string `env:"LOCALAI_NODE_NAME" help:"Node name for registration (defaults to hostname)" group:"registration"`
	RegistrationToken string `env:"LOCALAI_REGISTRATION_TOKEN" help:"Token for authenticating with the frontend" group:"registration"`
	HeartbeatInterval string `env:"LOCALAI_HEARTBEAT_INTERVAL" default:"10s" help:"Interval between heartbeats" group:"registration"`

	// Removed: the worker no longer runs agents or jobs. These are accepted and
	// ignored so a deployment that sets them keeps starting.
	RemovedAgentWorkerOptions `embed:""`

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

// agentCarrier maps the carrier that the frontend named onto the one this agent
// worker attaches to, and reports whether it is the tunnel. A frontend that names
// none predates carriers and runs on NATS. The flag of the NATS URL cannot be
// required, because the carrier is known only after the registration, so the
// check is here.
func agentCarrier(named, natsURL, handedURL string) (onTunnel bool, err error) {
	switch named {
	case "tunnel":
		if natsURL != "" {
			xlog.Info("The cluster runs on the tunnel; the NATS URL of this agent worker is kept for a change to NATS")
		}
		return true, nil
	case "", "nats":
		if natsURL == "" && handedURL == "" {
			return false, fmt.Errorf("the cluster runs on NATS and this agent worker has no NATS URL, and the frontend handed over none: set LOCALAI_NATS_URL")
		}
		return false, nil
	default:
		return false, fmt.Errorf("the frontend names the carrier %q, which this agent worker does not know: upgrade the agent worker", named)
	}
}

func (cmd *AgentWorkerCMD) Run(ctx *cliContext.Context) error {
	cmd.RemovedAgentWorkerOptions.warn()
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
	registrationBody := func() map[string]any {
		body := map[string]any{
			"name":      nodeName,
			"node_type": "agent",
			"version":   internal.Version,
			"commit":    internal.Commit,
		}
		if cmd.RegistrationToken != "" {
			body["token"] = cmd.RegistrationToken
		}
		return body
	}

	// Context cancelled on shutdown — used by registration waits, heartbeat, and
	// other background goroutines.
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	// Acquire credentials via (re)registration. When the bus requires auth and no
	// static fallback is configured, wait through admin approval until the
	// frontend mints credentials rather than starting unauthenticated.
	staticNATS := cmd.NatsJWT != "" || cmd.NatsServiceJWT != ""
	credMgr := workerregistry.NewCredentialManager(
		func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
			return regClient.RegisterFull(ctx, registrationBody())
		},
		cmd.natsAuthRequired() && !staticNATS,
	)
	res, err := credMgr.Acquire(shutdownCtx)
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	nodeID := res.ID
	xlog.Info("Registered with frontend", "nodeID", nodeID, "frontend", cmd.RegisterTo)

	onTunnel, err := agentCarrier(res.Carrier, cmd.NatsURL, res.NatsURL)
	if err != nil {
		return err
	}
	bootCarrier := cluster.CarrierNATS
	if onTunnel {
		bootCarrier = cluster.CarrierTunnel
	}

	heartbeatInterval, err := time.ParseDuration(cmd.HeartbeatInterval)
	if err != nil && cmd.HeartbeatInterval != "" {
		xlog.Warn("invalid heartbeat interval, using default 10s", "input", cmd.HeartbeatInterval, "error", err)
	}
	heartbeatInterval = cmp.Or(heartbeatInterval, 10*time.Second)

	// Resolve NATS credentials with precedence: explicit env override, then
	// frontend-minted (auto-refreshed before expiry), then service fallback.
	// Each static source must supply JWT and seed together.
	if (cmd.NatsJWT == "") != (cmd.NatsUserSeed == "") {
		return fmt.Errorf("LOCALAI_NATS_JWT and LOCALAI_NATS_USER_SEED must be set together")
	}
	if (cmd.NatsServiceJWT == "") != (cmd.NatsServiceSeed == "") {
		return fmt.Errorf("LOCALAI_NATS_SERVICE_JWT and LOCALAI_NATS_SERVICE_SEED must be set together")
	}
	local := worker.NATSLocal{
		URL: cmd.NatsURL, JWT: cmd.NatsJWT, Seed: cmd.NatsUserSeed, Required: cmd.natsAuthRequired(),
		TLS:          messaging.TLSFiles{CA: cmd.NatsTLSCA, Cert: cmd.NatsTLSCert, Key: cmd.NatsTLSKey},
		FallbackJWT:  cmd.NatsServiceJWT,
		FallbackSeed: cmd.NatsServiceSeed,
	}
	serve := agentworker.FollowConfig{
		NodeID: nodeID, FrontendURL: cmd.RegisterTo, ControlToken: cmd.RegistrationToken,
		Local: local,
		// Model-level MCP tool calls and discovery from the frontend. The worker
		// creates and caches the MCP sessions from the serialized config.
		Tool:      handleMCPToolRequest,
		Discovery: handleMCPDiscoveryRequest,
		// The agent worker has no model loader, so it drops the sessions cached for
		// a backend that goes away when it hears the stop of that backend.
		BackendStop: func(backend string) {
			if backend != "" {
				mcpTools.CloseMCPSessions(backend)
			}
		},
	}

	carriers := worker.NewFollower(worker.FollowerOptions{
		Attachers: serve.Attachers(),
		Credentials: func(c cluster.Carrier) *workerregistry.CredentialManager {
			return workerregistry.NewCredentialManagerFor(
				func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
					body := registrationBody()
					body["carrier"] = string(c)
					return regClient.RegisterFull(ctx, body)
				}, c == cluster.CarrierNATS && cmd.natsAuthRequired() && !staticNATS, string(c))
		},
		Heartbeat: func(ctx context.Context, body map[string]any) (*workerregistry.HeartbeatReply, error) {
			return regClient.HeartbeatFull(ctx, nodeID, body)
		},
		Cannot:   serve.Cannot(),
		Interval: heartbeatInterval,
	})
	defer carriers.Close()
	if _, err := carriers.Attach(shutdownCtx, bootCarrier, credMgr, res); err != nil {
		return fmt.Errorf("attaching to the %s carrier: %w", bootCarrier, err)
	}
	go carriers.Run(shutdownCtx)
	xlog.Info("Agent worker ready", "carrier", bootCarrier)

	runErr := awaitShutdown(shutdownCtx, "the carrier stopped")
	xlog.Info("Shutting down agent worker")
	shutdownCancel() // stop heartbeat loop immediately
	mcpTools.CloseAllMCPSessions()
	regClient.GracefulDeregister(nodeID)
	return runErr
}

// awaitShutdown blocks until the process is told to stop or the context of the
// worker ends for a fatal reason, and returns an error in the second case so
// that the worker restarts and registers again instead of lingering unable to
// serve.
func awaitShutdown(shutdownCtx context.Context, fatal string) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	select {
	case <-sigCh:
		return nil
	case <-shutdownCtx.Done():
		err := fmt.Errorf("agent worker shutting down: %s", fatal)
		xlog.Error("Internal shutdown requested", "error", err)
		return err
	}
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
