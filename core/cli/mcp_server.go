package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	localaitools "github.com/mudler/LocalAI/core/services/localaitools"
	"github.com/mudler/LocalAI/core/services/localaitools/httpapi"
)

// MCPServerCMD runs the LocalAI admin tool surface as a stdio MCP server,
// targeting a remote LocalAI instance over its HTTP API. The same Go package
// that powers the in-process LocalAI Assistant chat modality is used here —
// only the LocalAIClient implementation differs (httpapi instead of inproc).
type MCPServerCMD struct {
	Target   string `env:"LOCALAI_MCP_TARGET" default:"http://localhost:8080" help:"LocalAI base URL"`
	APIKey   string `env:"LOCALAI_API_KEY" help:"Bearer API key for the target LocalAI"`
	FilesDir string `env:"LOCALAI_MCP_FILES_DIR" help:"Folder where the client tools (generate_image, speak, transcribe_audio, ...) read input files from and write results to. A file argument must lie inside it, or be a data URL. There is no default: without it, the client tools that read or write files are not registered"`
	ReadOnly bool   `help:"Skip registration of mutating tools (install/delete/edit/upgrade/etc.), and of the client tools that write files or make new content (generate_*, speak, embed, voice_profile create and delete), so the assistant can browse without changing remote state or spending compute"`
}

func (m *MCPServerCMD) Run(_ *cliContext.Context) error {
	if m.Target == "" {
		return fmt.Errorf("--target / LOCALAI_MCP_TARGET is required")
	}

	client := httpapi.New(m.Target, m.APIKey)
	if m.FilesDir != "" {
		client.Files = localaitools.DirFileStore{Dir: m.FilesDir}
	}
	srv := localaitools.NewServer(client, localaitools.Options{
		DisableMutating:    m.ReadOnly,
		DisableClientFiles: m.FilesDir == "",
	})

	// Stdio: the host (e.g. Claude Desktop, Cursor, mcphost) talks JSON-RPC
	// over our stdin/stdout. There's nothing else this process should print —
	// every other goroutine logging to stderr is fine, but stdout is sacred.
	//
	// Honour SIGINT/SIGTERM so a Ctrl-C from the host or `kill -TERM` from
	// process supervision gives srv.Run a chance to drain in-flight calls
	// before exiting.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return srv.Run(ctx, &mcp.StdioTransport{})
}
