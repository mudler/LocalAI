package cli

import (
	"github.com/mudler/LocalAI/core/cli/benchmark"
	"github.com/mudler/LocalAI/core/cli/cluster"
	cliContext "github.com/mudler/LocalAI/core/cli/context"
)

var CLI struct {
	cliContext.Context `embed:""`
	Benchmark          benchmark.Command `cmd:"" help:"Benchmark configured text models against a running LocalAI server"`

	Cluster         cluster.Command    `cmd:"" help:"Read or change the carrier and the settings of a distributed cluster through a running frontend"`
	Run             RunCMD             `cmd:"" help:"Run LocalAI, this the default command if no other command is specified. Run 'local-ai run --help' for more information" default:"withargs"`
	Chat            ChatCMD            `cmd:"" help:"Run the built-in terminal agent against a LocalAI server"`
	Models          ModelsCMD          `cmd:"" help:"Manage LocalAI models and definitions"`
	Backends        BackendsCMD        `cmd:"" help:"Manage LocalAI backends and definitions"`
	Addons          AddonsCMD          `cmd:"" help:"Check an add-on zip file and upload it to a running server to install it"`
	Library         LibraryCMD         `cmd:"" help:"Manage the library sources LocalAI reads add-ons, backends and models from"`
	TTS             TTSCMD             `cmd:"" help:"Convert text to speech"`
	SoundGeneration SoundGenerationCMD `cmd:"" help:"Generates audio files from text or audio"`
	Transcript      TranscriptCMD      `cmd:"" help:"Convert audio to text"`
	Worker          WorkerGroup        `cmd:"" aliases:"p2p-worker" help:"Start a worker. With no subcommand it is the worker for distributed mode (generic, backend-agnostic); the subcommands are the standalone llama.cpp, MLX, vLLM and ds4 workers. p2p-worker is the old name"`
	P2P             P2PCMD             `cmd:"" name:"p2p" help:"Run a relay, manage relays and pair devices over P2P"`
	AgentWorker     AgentWorkerCMD     `cmd:"" name:"agent-worker" help:"Start a worker for distributed mode (serves MCP tool calls via NATS)"`
	Util            UtilCMD            `cmd:"" help:"Utility commands"`
	MCPServer       MCPServerCMD       `cmd:"" name:"mcp-server" help:"Run the LocalAI admin tool surface as a stdio MCP server (controls a remote LocalAI instance over HTTP)"`
	Completion      CompletionCMD      `cmd:"" help:"Generate shell completion scripts for bash, zsh, or fish"`
}
