// Command localai-relay runs a gated LocalAI relay. It is the small binary of
// the localai/relay image and has the same flags and the same log setup as
// `local-ai p2p relay`.
package main

import (
	"context"

	"github.com/alecthomas/kong"

	"github.com/mudler/LocalAI/core/services/p2prelay/relaycmd"
)

func main() {
	var cli struct {
		relaycmd.LogFlags `embed:""`
		relaycmd.Command  `embed:""`
	}
	ctx := kong.Parse(&cli,
		kong.Name("localai-relay"),
		kong.Description("Run a gated relay for LocalAI P2P: a small public node that carries the traffic of your paired servers and devices when they cannot reach each other directly."),
		kong.UsageOnError(),
	)
	logs := cli.LogFlags.Settings()
	relaycmd.SetLogger(logs, false)
	ctx.FatalIfErrorf(cli.Command.Execute(context.Background(), logs))
}
