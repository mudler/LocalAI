package localaitools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mudler/LocalAI/internal"
)

// Options control which tools the server registers and how the embedded
// skill prompts are surfaced.
type Options struct {
	// DisableMutating omits all tools that change server state. Used by the
	// "--read-only" flavour of the standalone stdio CLI.
	DisableMutating bool

	// ServerName overrides the MCP server's advertised Implementation.Name.
	// Defaults to "localai-admin".
	ServerName string

	// ServerVersion overrides the advertised version. Defaults to the linked
	// internal.PrintableVersion().
	ServerVersion string
}

// NewServer builds an MCP server that exposes LocalAI's admin surface as
// tools, backed by the supplied LocalAIClient. The same server type is used
// in-process (paired in-memory transport) and out-of-process (stdio).
func NewServer(client LocalAIClient, opts Options) *mcp.Server {
	name := opts.ServerName
	if name == "" {
		name = DefaultServerName
	}
	version := opts.ServerVersion
	if version == "" {
		version = internal.PrintableVersion()
	}

	srv := mcp.NewServer(&mcp.Implementation{
		Name:    name,
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: SystemPrompt(opts),
	})

	registerModelTools(srv, client, opts)
	registerBackendTools(srv, client, opts)
	registerConfigTools(srv, client, opts)
	registerSystemTools(srv, client, opts)
	registerStateTools(srv, client, opts)
	registerBrandingTools(srv, client, opts)

	return srv
}
