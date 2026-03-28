package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
	mcpRemote "github.com/mudler/LocalAI/core/services/mcp"
	"github.com/mudler/LocalAI/core/services/messaging"

	"github.com/mudler/LocalAI/pkg/functions"
	"github.com/mudler/LocalAI/pkg/signals"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/xlog"
)

// NamedSession pairs an MCP session with its server name and type.
type NamedSession struct {
	Name    string
	Type    string // "remote" or "stdio"
	Session *mcp.ClientSession
}

// MCPToolInfo holds a discovered MCP tool along with its origin session.
type MCPToolInfo struct {
	ServerName string
	ToolName   string
	Function   functions.Function
	Session    *mcp.ClientSession
}

// MCPServerInfo describes an MCP server and its available tools, prompts, and resources.
type MCPServerInfo struct {
	Name      string   `json:"name"`
	Type      string   `json:"type"`
	Tools     []string `json:"tools"`
	Prompts   []string `json:"prompts,omitempty"`
	Resources []string `json:"resources,omitempty"`
}

// MCPPromptInfo holds a discovered MCP prompt along with its origin session.
type MCPPromptInfo struct {
	ServerName  string
	PromptName  string
	Description string
	Title       string
	Arguments   []*mcp.PromptArgument
	Session     *mcp.ClientSession
}

// MCPResourceInfo holds a discovered MCP resource along with its origin session.
type MCPResourceInfo struct {
	ServerName  string
	Name        string
	URI         string
	Description string
	MIMEType    string
	Session     *mcp.ClientSession
}

type sessionCache struct {
	mu      sync.Mutex
	cache   map[string][]*mcp.ClientSession
	cancels map[string]context.CancelFunc
}

type namedSessionCache struct {
	mu      sync.Mutex
	cache   map[string][]NamedSession
	cancels map[string]context.CancelFunc
}

var (
	cache = sessionCache{
		cache:   make(map[string][]*mcp.ClientSession),
		cancels: make(map[string]context.CancelFunc),
	}

	namedCache = namedSessionCache{
		cache:   make(map[string][]NamedSession),
		cancels: make(map[string]context.CancelFunc),
	}

	client = mcp.NewClient(&mcp.Implementation{Name: "LocalAI", Version: "v1.0.0"}, nil)
)

// MCPNATSClient is the interface for NATS request-reply operations needed by MCP routing.
type MCPNATSClient interface {
	Request(subject string, data []byte, timeout time.Duration) ([]byte, error)
}

// MCPServersFromMetadata extracts the MCP server list from the metadata map
// and returns the list. The "mcp_servers" key is consumed (deleted from the map)
// so it doesn't leak to the backend.
func MCPServersFromMetadata(metadata map[string]string) []string {
	raw, ok := metadata["mcp_servers"]
	if !ok || raw == "" {
		return nil
	}
	delete(metadata, "mcp_servers")
	servers := strings.Split(raw, ",")
	for i := range servers {
		servers[i] = strings.TrimSpace(servers[i])
	}
	return servers
}

func SessionsFromMCPConfig(
	name string,
	remote config.MCPGenericConfig[config.MCPRemoteServers],
	stdio config.MCPGenericConfig[config.MCPSTDIOServers],
) ([]*mcp.ClientSession, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	sessions, exists := cache.cache[name]

	// Verify cached sessions are still alive.
	if exists {
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer pingCancel()
		alive := true
		for _, s := range sessions {
			if err := s.Ping(pingCtx, nil); err != nil {
				xlog.Warn("MCP session dead, evicting cache", "name", name, "error", err)
				alive = false
				break
			}
		}
		if !alive {
			if cancel, ok := cache.cancels[name]; ok {
				cancel()
			}
			delete(cache.cache, name)
			delete(cache.cancels, name)
			exists = false
		}
	}

	if exists {
		return sessions, nil
	}

	allSessions := []*mcp.ClientSession{}

	ctx, cancel := context.WithCancel(context.Background())

	// Get the list of all the tools that the Agent will be esposed to
	for _, server := range remote.Servers {
		xlog.Debug("[MCP remote server] Configuration", "server", server)
		// Create HTTP client with custom roundtripper for bearer token injection
		httpClient := &http.Client{
			Timeout:   config.DefaultMCPToolTimeout,
			Transport: newBearerTokenRoundTripper(server.Token, http.DefaultTransport),
		}

		transport := &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: httpClient}
		mcpSession, err := client.Connect(ctx, transport, nil)
		if err != nil {
			xlog.Error("Failed to connect to MCP server", "error", err, "url", server.URL)
			continue
		}
		xlog.Debug("[MCP remote server] Connected to MCP server", "url", server.URL)
		cache.cache[name] = append(cache.cache[name], mcpSession)
		allSessions = append(allSessions, mcpSession)
	}

	for _, server := range stdio.Servers {
		xlog.Debug("[MCP stdio server] Configuration", "server", server)
		command := exec.Command(server.Command, server.Args...)
		command.Env = os.Environ()
		for key, value := range server.Env {
			command.Env = append(command.Env, key+"="+value)
		}
		transport := &mcp.CommandTransport{Command: command}
		mcpSession, err := client.Connect(ctx, transport, nil)
		if err != nil {
			xlog.Error("Failed to start MCP server", "error", err, "command", command)
			continue
		}
		xlog.Debug("[MCP stdio server] Connected to MCP server", "command", command)
		cache.cache[name] = append(cache.cache[name], mcpSession)
		allSessions = append(allSessions, mcpSession)
	}

	cache.cancels[name] = cancel

	return allSessions, nil
}

// NamedSessionsFromMCPConfig returns sessions with their server names preserved.
// If enabledServers is non-empty, only servers with matching names are returned.
func NamedSessionsFromMCPConfig(
	name string,
	remote config.MCPGenericConfig[config.MCPRemoteServers],
	stdio config.MCPGenericConfig[config.MCPSTDIOServers],
	enabledServers []string,
) ([]NamedSession, error) {
	namedCache.mu.Lock()
	defer namedCache.mu.Unlock()

	allSessions, exists := namedCache.cache[name]

	// If cached, verify sessions are still alive via Ping.
	// Dead sessions (e.g. exited stdio containers) are evicted so they get recreated.
	if exists {
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer pingCancel()
		alive := true
		for _, ns := range allSessions {
			if err := ns.Session.Ping(pingCtx, nil); err != nil {
				xlog.Warn("MCP session dead, evicting cache", "server", ns.Name, "error", err)
				alive = false
				break
			}
		}
		if !alive {
			// Close dead sessions and recreate
			if cancel, ok := namedCache.cancels[name]; ok {
				cancel()
			}
			delete(namedCache.cache, name)
			delete(namedCache.cancels, name)
			exists = false
			allSessions = nil
		}
	}

	if !exists {
		ctx, cancel := context.WithCancel(context.Background())

		for serverName, server := range remote.Servers {
			xlog.Debug("[MCP remote server] Configuration", "name", serverName, "server", server)
			httpClient := &http.Client{
				Timeout:   config.DefaultMCPToolTimeout,
				Transport: newBearerTokenRoundTripper(server.Token, http.DefaultTransport),
			}

			transport := &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: httpClient}
			mcpSession, err := client.Connect(ctx, transport, nil)
			if err != nil {
				xlog.Error("Failed to connect to MCP server", "error", err, "name", serverName, "url", server.URL)
				continue
			}
			xlog.Debug("[MCP remote server] Connected", "name", serverName, "url", server.URL)
			allSessions = append(allSessions, NamedSession{
				Name:    serverName,
				Type:    "remote",
				Session: mcpSession,
			})
		}

		for serverName, server := range stdio.Servers {
			xlog.Debug("[MCP stdio server] Configuration", "name", serverName, "server", server)
			command := exec.Command(server.Command, server.Args...)
			command.Env = os.Environ()
			for key, value := range server.Env {
				command.Env = append(command.Env, key+"="+value)
			}
			transport := &mcp.CommandTransport{Command: command}
			mcpSession, err := client.Connect(ctx, transport, nil)
			if err != nil {
				xlog.Error("Failed to start MCP server", "error", err, "name", serverName, "command", command)
				continue
			}
			xlog.Debug("[MCP stdio server] Connected", "name", serverName, "command", command)
			allSessions = append(allSessions, NamedSession{
				Name:    serverName,
				Type:    "stdio",
				Session: mcpSession,
			})
		}

		namedCache.cache[name] = allSessions
		namedCache.cancels[name] = cancel
	}

	if len(enabledServers) == 0 {
		return allSessions, nil
	}

	enabled := make(map[string]bool, len(enabledServers))
	for _, s := range enabledServers {
		enabled[s] = true
	}
	var filtered []NamedSession
	for _, ns := range allSessions {
		if enabled[ns.Name] {
			filtered = append(filtered, ns)
		}
	}
	return filtered, nil
}

// DiscoverMCPTools queries each session for its tools and converts them to functions.Function.
// Deduplicates by tool name (first server wins).
func DiscoverMCPTools(ctx context.Context, sessions []NamedSession) ([]MCPToolInfo, error) {
	seen := make(map[string]bool)
	var result []MCPToolInfo

	for _, ns := range sessions {
		toolsResult, err := ns.Session.ListTools(ctx, nil)
		if err != nil {
			xlog.Error("Failed to list tools from MCP server", "error", err, "server", ns.Name)
			continue
		}
		for _, tool := range toolsResult.Tools {
			if seen[tool.Name] {
				continue
			}
			seen[tool.Name] = true

			f := functions.Function{
				Name:        tool.Name,
				Description: tool.Description,
			}

			// Convert InputSchema to map[string]any for functions.Function
			if tool.InputSchema != nil {
				schemaBytes, err := json.Marshal(tool.InputSchema)
				if err == nil {
					var params map[string]any
					if err := json.Unmarshal(schemaBytes, &params); err == nil {
						f.Parameters = params
					} else {
						xlog.Warn("Failed to unmarshal MCP tool input schema", "tool", tool.Name, "error", err)
					}
				}
			}
			if f.Parameters == nil {
				f.Parameters = map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				}
			}

			result = append(result, MCPToolInfo{
				ServerName: ns.Name,
				ToolName:   tool.Name,
				Function:   f,
				Session:    ns.Session,
			})
		}
	}
	return result, nil
}

// ExecuteMCPToolCall finds the matching tool and executes it.
func ExecuteMCPToolCall(ctx context.Context, tools []MCPToolInfo, toolName string, arguments string) (string, error) {
	var toolInfo *MCPToolInfo
	for i := range tools {
		if tools[i].ToolName == toolName {
			toolInfo = &tools[i]
			break
		}
	}
	if toolInfo == nil {
		return "", fmt.Errorf("MCP tool %q not found", toolName)
	}

	var args map[string]any
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return "", fmt.Errorf("failed to parse arguments for tool %q: %w", toolName, err)
		}
	}

	result, err := toolInfo.Session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		return "", fmt.Errorf("MCP tool %q call failed: %w", toolName, err)
	}

	// Extract text content from result
	var texts []string
	for _, content := range result.Content {
		if tc, ok := content.(*mcp.TextContent); ok {
			texts = append(texts, tc.Text)
		}
	}
	if len(texts) == 0 {
		// Fallback: marshal the whole result
		data, _ := json.Marshal(result.Content)
		return string(data), nil
	}
	if len(texts) == 1 {
		return texts[0], nil
	}
	combined, _ := json.Marshal(texts)
	return string(combined), nil
}

// ExecuteMCPToolCallRemote routes an MCP tool execution request to an agent worker via NATS.
// Used in distributed mode when the frontend doesn't hold MCP sessions locally.
func ExecuteMCPToolCallRemote(
	ctx context.Context,
	natsClient MCPNATSClient,
	modelName string,
	remote config.MCPGenericConfig[config.MCPRemoteServers],
	stdio config.MCPGenericConfig[config.MCPSTDIOServers],
	toolName, arguments string,
) (string, error) {
	if natsClient == nil {
		return "", fmt.Errorf("NATS client not configured for distributed MCP")
	}

	var args map[string]any
	if arguments != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return "", fmt.Errorf("invalid tool arguments JSON: %w", err)
		}
	}

	req := mcpRemote.MCPToolRequest{
		ModelName:     modelName,
		ToolName:      toolName,
		Arguments:     args,
		RemoteServers: remote,
		StdioServers:  stdio,
	}
	reqData, _ := json.Marshal(req)

	replyData, err := natsClient.Request(messaging.SubjectMCPToolExecute, reqData, config.DefaultMCPToolTimeout)
	if err != nil {
		return "", fmt.Errorf("NATS MCP tool request failed: %w", err)
	}

	var resp mcpRemote.MCPToolResponse
	if err := json.Unmarshal(replyData, &resp); err != nil {
		return "", fmt.Errorf("unmarshal MCP reply: %w", err)
	}
	if resp.Error != "" {
		return "", fmt.Errorf("remote MCP tool error: %s", resp.Error)
	}
	return resp.Result, nil
}

// DiscoverMCPToolsRemote routes an MCP discovery request to an agent worker via NATS.
// Returns server info and tool function schemas from the remote worker.
func DiscoverMCPToolsRemote(
	ctx context.Context,
	natsClient MCPNATSClient,
	modelName string,
	remote config.MCPGenericConfig[config.MCPRemoteServers],
	stdio config.MCPGenericConfig[config.MCPSTDIOServers],
) (*mcpRemote.MCPDiscoveryResponse, error) {
	if natsClient == nil {
		return nil, fmt.Errorf("NATS client not configured for distributed MCP")
	}

	req := mcpRemote.MCPDiscoveryRequest{
		ModelName:     modelName,
		RemoteServers: remote,
		StdioServers:  stdio,
	}
	reqData, _ := json.Marshal(req)

	replyData, err := natsClient.Request(messaging.SubjectMCPDiscovery, reqData, config.DefaultMCPDiscoveryTimeout)
	if err != nil {
		return nil, fmt.Errorf("NATS MCP discovery request failed: %w", err)
	}

	var resp mcpRemote.MCPDiscoveryResponse
	if err := json.Unmarshal(replyData, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal MCP discovery reply: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("remote MCP discovery error: %s", resp.Error)
	}
	return &resp, nil
}

// ListMCPServers returns server info with tool, prompt, and resource names for each session.
func ListMCPServers(ctx context.Context, sessions []NamedSession) ([]MCPServerInfo, error) {
	var result []MCPServerInfo
	for _, ns := range sessions {
		info := MCPServerInfo{
			Name: ns.Name,
			Type: ns.Type,
		}
		toolsResult, err := ns.Session.ListTools(ctx, nil)
		if err != nil {
			xlog.Error("Failed to list tools from MCP server", "error", err, "server", ns.Name)
		} else {
			for _, tool := range toolsResult.Tools {
				info.Tools = append(info.Tools, tool.Name)
			}
		}

		promptsResult, err := ns.Session.ListPrompts(ctx, nil)
		if err != nil {
			xlog.Debug("Failed to list prompts from MCP server", "error", err, "server", ns.Name)
		} else {
			for _, p := range promptsResult.Prompts {
				info.Prompts = append(info.Prompts, p.Name)
			}
		}

		resourcesResult, err := ns.Session.ListResources(ctx, nil)
		if err != nil {
			xlog.Debug("Failed to list resources from MCP server", "error", err, "server", ns.Name)
		} else {
			for _, r := range resourcesResult.Resources {
				info.Resources = append(info.Resources, r.URI)
			}
		}

		result = append(result, info)
	}
	return result, nil
}

// IsMCPTool checks if a tool name is in the MCP tool list.
func IsMCPTool(tools []MCPToolInfo, name string) bool {
	for _, t := range tools {
		if t.ToolName == name {
			return true
		}
	}
	return false
}

// DiscoverMCPPrompts queries each session for its prompts.
// Deduplicates by prompt name (first server wins).
func DiscoverMCPPrompts(ctx context.Context, sessions []NamedSession) ([]MCPPromptInfo, error) {
	seen := make(map[string]bool)
	var result []MCPPromptInfo

	for _, ns := range sessions {
		promptsResult, err := ns.Session.ListPrompts(ctx, nil)
		if err != nil {
			xlog.Error("Failed to list prompts from MCP server", "error", err, "server", ns.Name)
			continue
		}
		for _, p := range promptsResult.Prompts {
			if seen[p.Name] {
				continue
			}
			seen[p.Name] = true
			result = append(result, MCPPromptInfo{
				ServerName:  ns.Name,
				PromptName:  p.Name,
				Description: p.Description,
				Title:       p.Title,
				Arguments:   p.Arguments,
				Session:     ns.Session,
			})
		}
	}
	return result, nil
}

// GetMCPPrompt finds and expands a prompt by name using the discovered prompts list.
func GetMCPPrompt(ctx context.Context, prompts []MCPPromptInfo, name string, args map[string]string) ([]*mcp.PromptMessage, error) {
	var info *MCPPromptInfo
	for i := range prompts {
		if prompts[i].PromptName == name {
			info = &prompts[i]
			break
		}
	}
	if info == nil {
		return nil, fmt.Errorf("MCP prompt %q not found", name)
	}

	result, err := info.Session.GetPrompt(ctx, &mcp.GetPromptParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("MCP prompt %q get failed: %w", name, err)
	}
	return result.Messages, nil
}

// DiscoverMCPResources queries each session for its resources.
// Deduplicates by URI (first server wins).
func DiscoverMCPResources(ctx context.Context, sessions []NamedSession) ([]MCPResourceInfo, error) {
	seen := make(map[string]bool)
	var result []MCPResourceInfo

	for _, ns := range sessions {
		resourcesResult, err := ns.Session.ListResources(ctx, nil)
		if err != nil {
			xlog.Error("Failed to list resources from MCP server", "error", err, "server", ns.Name)
			continue
		}
		for _, r := range resourcesResult.Resources {
			if seen[r.URI] {
				continue
			}
			seen[r.URI] = true
			result = append(result, MCPResourceInfo{
				ServerName:  ns.Name,
				Name:        r.Name,
				URI:         r.URI,
				Description: r.Description,
				MIMEType:    r.MIMEType,
				Session:     ns.Session,
			})
		}
	}
	return result, nil
}

// ReadMCPResource reads a resource by URI from the matching session.
func ReadMCPResource(ctx context.Context, resources []MCPResourceInfo, uri string) (string, error) {
	var info *MCPResourceInfo
	for i := range resources {
		if resources[i].URI == uri {
			info = &resources[i]
			break
		}
	}
	if info == nil {
		return "", fmt.Errorf("MCP resource %q not found", uri)
	}

	result, err := info.Session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return "", fmt.Errorf("MCP resource %q read failed: %w", uri, err)
	}

	var texts []string
	for _, c := range result.Contents {
		if c.Text != "" {
			texts = append(texts, c.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}

// MCPPromptFromMetadata extracts the prompt name and arguments from metadata.
// The "mcp_prompt" and "mcp_prompt_args" keys are consumed (deleted from the map).
func MCPPromptFromMetadata(metadata map[string]string) (string, map[string]string) {
	name, ok := metadata["mcp_prompt"]
	if !ok || name == "" {
		return "", nil
	}
	delete(metadata, "mcp_prompt")

	var args map[string]string
	if raw, ok := metadata["mcp_prompt_args"]; ok && raw != "" {
		json.Unmarshal([]byte(raw), &args)
		delete(metadata, "mcp_prompt_args")
	}
	return name, args
}

// MCPResourcesFromMetadata extracts resource URIs from metadata.
// The "mcp_resources" key is consumed (deleted from the map).
func MCPResourcesFromMetadata(metadata map[string]string) []string {
	raw, ok := metadata["mcp_resources"]
	if !ok || raw == "" {
		return nil
	}
	delete(metadata, "mcp_resources")
	uris := strings.Split(raw, ",")
	for i := range uris {
		uris[i] = strings.TrimSpace(uris[i])
	}
	return uris
}

// PromptMessageToText extracts text from a PromptMessage's Content.
func PromptMessageToText(msg *mcp.PromptMessage) string {
	if tc, ok := msg.Content.(*mcp.TextContent); ok {
		return tc.Text
	}
	// Fallback: marshal content
	data, _ := json.Marshal(msg.Content)
	return string(data)
}

// CloseMCPSessions closes all MCP sessions for a given model and removes them from the cache.
// This should be called when a model is unloaded or shut down.
func CloseMCPSessions(modelName string) {
	// Close sessions in the unnamed cache
	cache.mu.Lock()
	if sessions, ok := cache.cache[modelName]; ok {
		for _, s := range sessions {
			s.Close()
		}
		delete(cache.cache, modelName)
	}
	if cancel, ok := cache.cancels[modelName]; ok {
		cancel()
		delete(cache.cancels, modelName)
	}
	cache.mu.Unlock()

	// Close sessions in the named cache
	namedCache.mu.Lock()
	if sessions, ok := namedCache.cache[modelName]; ok {
		for _, ns := range sessions {
			ns.Session.Close()
		}
		delete(namedCache.cache, modelName)
	}
	if cancel, ok := namedCache.cancels[modelName]; ok {
		cancel()
		delete(namedCache.cancels, modelName)
	}
	namedCache.mu.Unlock()

	xlog.Debug("Closed MCP sessions for model", "model", modelName)
}

// CloseAllMCPSessions closes all cached MCP sessions across all models.
// This should be called during graceful shutdown.
func CloseAllMCPSessions() {
	cache.mu.Lock()
	for name, sessions := range cache.cache {
		for _, s := range sessions {
			s.Close()
		}
		if cancel, ok := cache.cancels[name]; ok {
			cancel()
		}
	}
	cache.cache = make(map[string][]*mcp.ClientSession)
	cache.cancels = make(map[string]context.CancelFunc)
	cache.mu.Unlock()

	namedCache.mu.Lock()
	for name, sessions := range namedCache.cache {
		for _, ns := range sessions {
			ns.Session.Close()
		}
		if cancel, ok := namedCache.cancels[name]; ok {
			cancel()
		}
	}
	namedCache.cache = make(map[string][]NamedSession)
	namedCache.cancels = make(map[string]context.CancelFunc)
	namedCache.mu.Unlock()

	xlog.Debug("Closed all MCP sessions")
}

func init() {
	signals.RegisterGracefulTerminationHandler(func() {
		CloseAllMCPSessions()
	})
}

// bearerTokenRoundTripper is a custom roundtripper that injects a bearer token
// into HTTP requests
type bearerTokenRoundTripper struct {
	token string
	base  http.RoundTripper
}

// RoundTrip implements the http.RoundTripper interface
func (rt *bearerTokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.token != "" {
		req.Header.Set("Authorization", "Bearer "+rt.token)
	}
	return rt.base.RoundTrip(req)
}

// newBearerTokenRoundTripper creates a new roundtripper that injects the given token
func newBearerTokenRoundTripper(token string, base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &bearerTokenRoundTripper{
		token: token,
		base:  base,
	}
}

// MCPContextResult holds the results of MCP prompt and resource discovery
// so callers can inject them into their message slices.
type MCPContextResult struct {
	// PromptMessages are schema.Message values converted from MCP prompts,
	// intended to be prepended to the conversation.
	PromptMessages []schema.Message

	// ResourceSuffix is the formatted text of all discovered MCP resources,
	// intended to be appended to the last user message's content.
	// Empty string when no resources were requested or found.
	ResourceSuffix string
}

// InjectMCPContext discovers MCP prompts and resources from the given named sessions
// and returns them in a form ready for injection into any endpoint's message list.
func InjectMCPContext(
	ctx context.Context,
	namedSessions []NamedSession,
	mcpPromptName string,
	mcpPromptArgs map[string]string,
	mcpResourceURIs []string,
) (*MCPContextResult, error) {
	result := &MCPContextResult{}

	if mcpPromptName != "" {
		prompts, discErr := DiscoverMCPPrompts(ctx, namedSessions)
		if discErr != nil {
			xlog.Error("Failed to discover MCP prompts", "error", discErr)
		} else {
			promptMsgs, getErr := GetMCPPrompt(ctx, prompts, mcpPromptName, mcpPromptArgs)
			if getErr != nil {
				xlog.Error("Failed to get MCP prompt", "error", getErr)
			} else {
				for _, pm := range promptMsgs {
					result.PromptMessages = append(result.PromptMessages, schema.Message{
						Role:    string(pm.Role),
						Content: PromptMessageToText(pm),
					})
				}
				xlog.Debug("MCP prompt discovered", "prompt", mcpPromptName, "messages", len(result.PromptMessages))
			}
		}
	}

	if len(mcpResourceURIs) > 0 {
		resources, discErr := DiscoverMCPResources(ctx, namedSessions)
		if discErr != nil {
			xlog.Error("Failed to discover MCP resources", "error", discErr)
		} else {
			var resourceTexts []string
			for _, uri := range mcpResourceURIs {
				content, readErr := ReadMCPResource(ctx, resources, uri)
				if readErr != nil {
					xlog.Error("Failed to read MCP resource", "error", readErr, "uri", uri)
					continue
				}
				name := uri
				for _, r := range resources {
					if r.URI == uri {
						name = r.Name
						break
					}
				}
				resourceTexts = append(resourceTexts, fmt.Sprintf("--- MCP Resource: %s ---\n%s", name, content))
			}
			if len(resourceTexts) > 0 {
				result.ResourceSuffix = "\n\n" + strings.Join(resourceTexts, "\n\n")
				xlog.Debug("MCP resources discovered", "count", len(resourceTexts))
			}
		}
	}

	return result, nil
}

// AppendResourceSuffix appends the resource suffix from an MCPContextResult
// to the last message's content in the given message slice.
func AppendResourceSuffix(messages []schema.Message, suffix string) {
	if suffix == "" || len(messages) == 0 {
		return
	}
	lastIdx := len(messages) - 1
	switch ct := messages[lastIdx].Content.(type) {
	case string:
		messages[lastIdx].Content = ct + suffix
	default:
		messages[lastIdx].Content = fmt.Sprintf("%v%s", ct, suffix)
	}
}
