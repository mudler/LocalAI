package cli

import (
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/cli/worker"
)

var CLI struct {
	cliContext.Context `embed:""`

	Run             RunCMD             `cmd:"" help:"Run LocalAI, this the default command if no other command is specified. Run 'local-ai run --help' for more information" default:"withargs"`
	Federated       FederatedCLI       `cmd:"" help:"Run LocalAI in federated mode"`
	Models          ModelsCMD          `cmd:"" help:"Manage LocalAI models and definitions"`
	Backends        BackendsCMD        `cmd:"" help:"Manage LocalAI backends and definitions"`
	TTS             TTSCMD             `cmd:"" help:"Convert text to speech"`
	SoundGeneration SoundGenerationCMD `cmd:"" help:"Generates audio files from text or audio"`
	Transcript      TranscriptCMD      `cmd:"" help:"Convert audio to text"`
	P2PWorker       worker.Worker      `cmd:"" name:"p2p-worker" help:"Run workers to distribute workload via p2p (llama.cpp-only)"`
	Worker          WorkerCMD          `cmd:"" help:"Start a worker for distributed mode (generic, backend-agnostic)"`
	AgentWorker     AgentWorkerCMD     `cmd:"" name:"agent-worker" help:"Start an agent worker for distributed mode (executes agent chats via NATS)"`
	Util            UtilCMD            `cmd:"" help:"Utility commands"`
	Agent           AgentCMD           `cmd:"" help:"Run agents standalone without the full LocalAI server"`
	MCPServer       MCPServerCMD       `cmd:"" name:"mcp-server" help:"Run the LocalAI admin tool surface as a stdio MCP server (controls a remote LocalAI instance over HTTP)"`
	Explorer        ExplorerCMD        `cmd:"" help:"Run p2p explorer"`
	Completion      CompletionCMD      `cmd:"" help:"Generate shell completion scripts for bash, zsh, or fish"`
}
