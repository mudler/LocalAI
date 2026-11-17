package cli

import (
	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/services/p2prelay/relaycmd"
)

// P2PRelayCMD runs a gated relay. It takes the flags of the small
// `localai-relay` binary, which are defined in the relaycmd package.
type P2PRelayCMD struct {
	relaycmd.Command `embed:""`
}

func (c *P2PRelayCMD) Run(_ *cliContext.Context) error { return c.Execute() }
