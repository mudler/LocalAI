package cli

import (
	cliContext "github.com/go-skynet/LocalAI/core/cli/context"
	"github.com/go-skynet/LocalAI/core/cli/worker"
)

var CLI struct {
	cliContext.Context `embed:""`

	Run        RunCMD        `cmd:"" help:"Run LocalAI, this the default command if no other command is specified. Run 'local-ai run --help' for more information" default:"withargs"`
	Models     ModelsCMD     `cmd:"" help:"Manage LocalAI models and definitions"`
	TTS        TTSCMD        `cmd:"" help:"Convert text to speech"`
	Transcript TranscriptCMD `cmd:"" help:"Convert audio to text"`
	Worker     worker.Worker `cmd:"" help:"Run workers to distribute workload (llama.cpp-only)"`
}
