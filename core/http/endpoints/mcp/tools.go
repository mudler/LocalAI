package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/signals"

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
		log.Debug().Msgf("[MCP remote server] Configuration : %+v", server)
		// Create HTTP client with custom roundtripper for bearer token injection
		httpClient := &http.Client{
			Timeout:   360 * time.Second,
			Transport: newBearerTokenRoundTripper(server.Token, http.DefaultTransport),
		}

		transport := &mcp.StreamableClientTransport{Endpoint: server.URL, HTTPClient: httpClient}
		mcpSession, err := client.Connect(ctx, transport, nil)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to connect to MCP server %s", server.URL)
			continue
		}
		log.Debug().Msgf("[MCP remote server] Connected to MCP server %s", server.URL)
		cache.cache[name] = append(cache.cache[name], mcpSession)
		allSessions = append(allSessions, mcpSession)
	}

	for _, server := range stdio.Servers {
		log.Debug().Msgf("[MCP stdio server] Configuration : %+v", server)
		
		// Validate and create secure command to prevent command injection
		command, err := createSecureMCPCommand(server.Command, server.Args)
		if err != nil {
			log.Error().Err(err).Msgf("Invalid MCP server command: %s", server.Command)
			continue
		}
		command.Env = os.Environ()
		for key, value := range server.Env {
			command.Env = append(command.Env, key+"="+value)
		}
		transport := &mcp.CommandTransport{Command: command}
		mcpSession, err := client.Connect(ctx, transport, nil)
		if err != nil {
			log.Error().Err(err).Msgf("Failed to start MCP server %s", command)
			continue
		}
		log.Debug().Msgf("[MCP stdio server] Connected to MCP server %s", command)
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

// validateAndSanitizeMCPCommand validates and sanitizes MCP server commands to prevent command injection
func validateAndSanitizeMCPCommand(command string, args []string) (string, []string, error) {
	if command == "" {
		return "", nil, errors.New("command cannot be empty")
	}

	// Check for dangerous shell metacharacters in command
	dangerousChars := []string{";", "&", "|", "`", "$", "(", ")", "{", "}", "[", "]", "<", ">", "\"", "'", "\\"}
	for _, char := range dangerousChars {
		if strings.Contains(command, char) {
			return "", nil, fmt.Errorf("command contains dangerous character: %s", char)
		}
	}

	// Check for path traversal attempts
	if strings.Contains(command, "..") {
		return "", nil, errors.New("command contains path traversal sequence")
	}

	// Ensure command is a clean path (no relative paths or hidden directories)
	cleanCommand := filepath.Clean(command)
	if cleanCommand != command {
		return "", nil, fmt.Errorf("command path is not clean: %s vs %s", command, cleanCommand)
	}

	// Validate and sanitize arguments
	sanitizedArgs := make([]string, len(args))
	for i, arg := range args {
		// Check for shell metacharacters in arguments
		for _, char := range dangerousChars {
			if strings.Contains(arg, char) {
				return "", nil, fmt.Errorf("argument %d contains dangerous character: %s", i, char)
			}
		}
		
		// Check for path traversal in arguments
		if strings.Contains(arg, "..") {
			return "", nil, fmt.Errorf("argument %d contains path traversal sequence", i)
		}
		
		// Clean and sanitize the argument
		sanitizedArgs[i] = filepath.Clean(arg)
	}

	return cleanCommand, sanitizedArgs, nil
}
