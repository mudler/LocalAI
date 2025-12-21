package mcp

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/signals"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mudler/xlog"
)

type sessionCache struct {
	mu    sync.Mutex
	cache map[string][]*mcp.ClientSession
}

var (
	cache = sessionCache{
		cache: make(map[string][]*mcp.ClientSession),
	}

	client = mcp.NewClient(&mcp.Implementation{Name: "LocalAI", Version: "v1.0.0"}, nil)
)

func SessionsFromMCPConfig(
	name string,
	remote config.MCPGenericConfig[config.MCPRemoteServers],
	stdio config.MCPGenericConfig[config.MCPSTDIOServers],
) ([]*mcp.ClientSession, error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	sessions, exists := cache.cache[name]
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
			Timeout:   360 * time.Second,
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

	signals.RegisterGracefulTerminationHandler(func() {
		for _, session := range allSessions {
			session.Close()
		}
		cancel()
	})

	return allSessions, nil
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
