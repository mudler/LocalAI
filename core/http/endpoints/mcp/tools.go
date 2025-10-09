package mcp

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/core/config"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog/log"
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
	ctx context.Context,
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

	// Get the list of all the tools that the Agent will be esposed to
	for _, server := range remote.Servers {
		log.Debug().Msgf("[MCP remote server] Configuration : %+v", server)
		// Create HTTP client with custom roundtripper for bearer token injection
		httpClient := &http.Client{
			Timeout:   360 * time.Second,
			Transport: newBearerTokenRoundTripper(server.Token, http.DefaultTransport),
		}

		transport := &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: httpClient}
		mcpSession, err := client.Connect(context.Background(), transport, nil)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to connect to MCP server %s", server.URL)
			continue
		}
		log.Debug().Msgf("[MCP remote server] Connected to MCP server %s", server.URL)
		cache.cache[name] = append(cache.cache[name], mcpSession)
	}

	for _, server := range stdio.Servers {
		log.Debug().Msgf("[MCP stdio server] Configuration : %+v", server)
		command := exec.Command(server.Command, server.Args...)
		command.Env = os.Environ()
		for key, value := range server.Env {
			command.Env = append(command.Env, key+"="+value)
		}
		transport := &mcp.CommandTransport{Command: command}
		mcpSession, err := client.Connect(context.Background(), transport, nil)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to start MCP server %s", command)
			continue
		}
		log.Debug().Msgf("[MCP stdio server] Connected to MCP server %s", command)
		cache.cache[name] = append(cache.cache[name], mcpSession)
	}

	handleSignal(allSessions)

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

func handleSignal(sessions []*mcp.ClientSession) {

	// Create a channel to receive OS signals
	sigChan := make(chan os.Signal, 1)

	// Register for interrupt and terminate signals
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Handle signals in a separate goroutine
	go func() {
		sig := <-sigChan
		log.Printf("Received signal %v, shutting down gracefully...", sig)

		for _, t := range sessions {
			t.Close()
		}

		// Exit the application
		os.Exit(0)
	}()
}
