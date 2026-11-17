// Command localai-relay runs a gated LocalAI relay. It is the small binary of
// the localai/relay image and has the same flags as `local-ai p2p relay`.
package main

import (
	"github.com/alecthomas/kong"

	"github.com/mudler/LocalAI/core/services/p2prelay/relaycmd"
)

func main() {
	var cmd relaycmd.Command
	ctx := kong.Parse(&cmd,
		kong.Name("localai-relay"),
		kong.Description("Run a gated relay for LocalAI P2P: a small public node that carries the traffic of your paired servers and devices when they cannot reach each other directly."),
		kong.UsageOnError(),
	)
	ctx.FatalIfErrorf(cmd.Execute())
}
