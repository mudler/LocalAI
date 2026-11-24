package cli

import (
	"context"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/services/p2prelay/relaycmd"
	"github.com/mudler/LocalAI/pkg/logsetup"
)

// P2PRelayCMD runs a gated relay. It takes the flags of the small
// `localai-relay` binary, which are defined in the relaycmd package.
type P2PRelayCMD struct {
	relaycmd.Command `embed:""`
}

func (c *P2PRelayCMD) Run(ctx *cliContext.Context) error {
	if c.JSON {
		// Standard output carries only the JSON banner.
		logsetup.SetLogger(logsetup.NewLogSettings(ctx.Debug, ctx.LogLevel, ctx.LogFormat, ctx.LogDedupLogs), true)
	}
	return c.Execute(context.Background())
}
