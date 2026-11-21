package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"time"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/config"
	mcpTools "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/agentworker"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/worker"
	"github.com/mudler/LocalAI/internal"
	"github.com/mudler/LocalAI/pkg/sanitize"
	"github.com/mudler/LocalAI/pkg/signals"
	"github.com/mudler/xlog"
)

// AgentWorkerCMD starts a dedicated worker process for distributed mode. It
// registers with the frontend, holds the routes its operator gave it, and serves
// the MCP tool calls and discovery requests of the frontend, because the
// frontend cannot start the stdio MCP servers of a model (for example, ones that
// need docker). On NATS it subscribes to the MCP subjects. On the tunnel it holds
// one outbound connection to the frontend and serves the requests that arrive
// on it. It needs no database access.
//
// Usage:
//
//	localai agent-worker --register-to http://localai:8080
type AgentWorkerCMD struct {
	// NATS. A URL gives the worker the NATS route.
	NatsURL string `env:"LOCALAI_NATS_URL" help:"NATS server URL. With it the agent worker holds the NATS route. Without it, and with --routes nats, the agent worker uses the address that the frontend hands over" group:"distributed"`
	// Routes are the ways this agent worker reaches the cluster. Empty derives
	// them from the other flags (see routes).
	Routes []string `env:"LOCALAI_WORKER_ROUTES" help:"Routes this agent worker holds to the cluster, comma separated: nats, tunnel or both. Default: nats when a NATS URL is set, and the websocket tunnel to the frontend when a registration token is set; with neither, nats with the address that the frontend hands over" group:"registration"`

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

// routes returns the routes of the agent worker, NATS first. The tunnel needs
// the registration token, which guards the control server that the tunnel
// reaches, so it is not a default without one. A worker with neither a NATS URL
// nor a token takes NATS from the address that the frontend hands over.
func (cmd *AgentWorkerCMD) routes() ([]worker.Route, error) {
	if len(cmd.Routes) == 0 {
		var out []worker.Route
		if cmd.NatsURL != "" {
			out = append(out, worker.RouteNATS)
		}
		if cmd.RegistrationToken != "" {
			out = append(out, worker.RouteTunnel)
		}
		if len(out) == 0 {
			out = append(out, worker.RouteNATS)
		}
		return out, nil
	}
	out, err := worker.ParseRoutes(cmd.Routes)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--routes names no route: give nats, tunnel or both")
	}
	if slices.Contains(out, worker.RouteTunnel) && cmd.RegistrationToken == "" {
		return nil, fmt.Errorf("the tunnel route of an agent worker needs LOCALAI_REGISTRATION_TOKEN: it guards the control server that the tunnel reaches")
	}
	return out, nil
}

func (cmd *AgentWorkerCMD) Run(ctx *cliContext.Context) error {
	cmd.RemovedAgentWorkerOptions.warn()
	xlog.Info("Starting agent worker", "nats", sanitize.URL(cmd.NatsURL), "register_to", cmd.RegisterTo)

	routes, err := cmd.routes()
	if err != nil {
		return err
	}

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
	// register asks for the credentials of the given routes. Each route gets its
	// own credential from a registration that asks for that route only, so the
	// refresh of the NATS credential never mints a new tunnel token.
	register := func(rs ...worker.Route) workerregistry.RegisterFunc {
		names := make([]string, 0, len(rs))
		for _, r := range rs {
			names = append(names, string(r))
		}
		return func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
			body := map[string]any{
				"name":      nodeName,
				"node_type": "agent",
				"version":   internal.Version,
				"commit":    internal.Commit,
				"routes":    names,
			}
			if cmd.RegistrationToken != "" {
				body["token"] = cmd.RegistrationToken
			}
			return regClient.RegisterFull(ctx, body)
		}
	}

	// Context cancelled on shutdown — used by registration waits, heartbeat, and
	// other background goroutines.
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	defer shutdownCancel()

	// Register once for the identity of the node, with no credential.
	res, err := workerregistry.NewCredentialManager(register(), false).Acquire(shutdownCtx)
	if err != nil {
		return fmt.Errorf("registration failed: %w", err)
	}
	nodeID := res.ID
	xlog.Info("Registered with frontend", "nodeID", nodeID, "frontend", cmd.RegisterTo, "routes", routes)

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
	// When the bus requires auth and no static credential is configured, the
	// NATS route waits through admin approval until the frontend mints its
	// credential rather than connecting unauthenticated.
	staticNATS := cmd.NatsJWT != "" || cmd.NatsServiceJWT != ""
	local := worker.NATSLocal{
		URL: cmd.NatsURL, JWT: cmd.NatsJWT, Seed: cmd.NatsUserSeed, Required: cmd.natsAuthRequired(),
		TLS:          messaging.TLSFiles{CA: cmd.NatsTLSCA, Cert: cmd.NatsTLSCert, Key: cmd.NatsTLSKey},
		FallbackJWT:  cmd.NatsServiceJWT,
		FallbackSeed: cmd.NatsServiceSeed,
	}
	serve := agentworker.ServeConfig{
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

	held := worker.NewRouteHolder(worker.RouteHolderOptions{
		Routes:    routes,
		Attachers: serve.Attachers(),
		Credentials: func(r worker.Route) *workerregistry.CredentialManager {
			return workerregistry.NewCredentialManagerFor(register(r), r == worker.RouteNATS && cmd.natsAuthRequired() && !staticNATS, string(r))
		},
		Heartbeat: func(ctx context.Context, body map[string]any) error {
			return regClient.SendHeartbeat(ctx, nodeID, body)
		},
		Interval: heartbeatInterval,
	})
	defer held.Close()
	go held.Run(shutdownCtx)
	xlog.Info("Agent worker ready", "routes", routes)

	runErr := awaitShutdown(shutdownCtx, "the routes of the worker stopped")
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
	// NotifyTermination also covers a stop request from the Windows service
	// manager, which is not a signal.
	signals.NotifyTermination(sigCh)
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
