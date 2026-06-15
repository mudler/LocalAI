package cli

import (
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/services/worker"
)

// WorkerCMD is the kong-parsed CLI surface for `local-ai worker`.
// All business logic lives in core/services/worker — this struct just
// embeds the worker.Config (so kong sees the flag tags) and delegates Run.
type WorkerCMD struct {
	worker.Config `embed:""`
}

// Run starts the distributed worker. Delegates to worker.Run after kong has
// populated the embedded Config.
func (cmd *WorkerCMD) Run(ctx *cliContext.Context) error {
	return worker.Run(ctx, &cmd.Config)
}
