package cli

import (
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/cli/removedoptions"
	"github.com/mudler/LocalAI/core/cli/worker"
)

// WorkerGroup is `local-ai worker`. Run is the default child: `local-ai worker
// --register-to ...` is the distributed-mode worker it has always been, and
// the `withargs` default hands it every flag the named children do not take.
// The other children are the standalone model-split and follower workers.
//
// The group is also reachable as `p2p-worker`, the name those children had
// before the group was renamed.
type WorkerGroup struct {
	Run             WorkerCMD              `cmd:"" default:"withargs" help:"Start a worker for distributed mode (generic, backend-agnostic). This is what 'local-ai worker' runs when no subcommand is given"`
	LLamaCPP        worker.LLamaCPP        `cmd:"" name:"llama-cpp-rpc" help:"Starts a llama.cpp worker in standalone mode"`
	MLXDistributed  worker.MLXDistributed  `cmd:"" name:"mlx-distributed" help:"Starts an MLX distributed worker in standalone mode (requires --hostfile and --rank)"`
	VLLMDistributed worker.VLLMDistributed `cmd:"" name:"vllm" help:"Starts a vLLM data-parallel follower process. Multi-node DP for a single model: head runs the existing vllm backend with engine_args.data_parallel_size>1, followers run this command."`
	DS4Distributed  worker.DS4Distributed  `cmd:"" name:"ds4-distributed" help:"Starts a ds4 distributed worker in standalone mode: owns a layer slice and dials the coordinator (pass ds4-worker args after --)"`

	// The old P2P workers. They are kept as hidden commands so that a script
	// that still calls them gets the migration text instead of "unexpected argument".
	RemovedP2PLlamaCPP removedCommand `cmd:"" name:"p2p-llama-cpp-rpc" hidden:"" help:"Removed"`
	RemovedP2PMLX      removedCommand `cmd:"" name:"p2p-mlx" hidden:"" help:"Removed"`
}

// removedCommand is a command that no longer exists. It takes any arguments, so
// the flags of the old command do not turn the message into a parse error.
type removedCommand struct {
	Args []string `arg:"" optional:"" passthrough:"" help:"Ignored"`
}

// Run is never reached for the removed commands: main stops them before Run
// with removedoptions.Command. It still fails if it is, so a removed command
// can never succeed.
func (c *removedCommand) Run(ctx *cliContext.Context) error {
	return removedoptions.ErrRemovedCommand
}
