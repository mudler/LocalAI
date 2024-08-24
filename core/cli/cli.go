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
	TTS             TTSCMD             `cmd:"" help:"Convert text to speech"`
	SoundGeneration SoundGenerationCMD `cmd:"" help:"Generates audio files from text or audio"`
	Transcript      TranscriptCMD      `cmd:"" help:"Convert audio to text"`
	Worker          worker.Worker      `cmd:"" help:"Run workers to distribute workload (llama.cpp-only)"`
	Util            UtilCMD            `cmd:"" help:"Utility commands"`
	Explorer        ExplorerCMD        `cmd:"" help:"Run p2p explorer"`
}
