// Command localai-relay runs a gated LocalAI relay. It is the small binary of
// the localai/relay image and has the same flags and the same log setup as
// `local-ai p2p relay`.
package main

import (
	"context"

	"github.com/alecthomas/kong"

	"github.com/mudler/LocalAI/core/services/p2prelay/relaycmd"
	"github.com/mudler/LocalAI/pkg/logsetup"
)

func main() {
	var cli struct {
		logsetup.LogFlags `embed:""`
		relaycmd.Command  `embed:""`
	}
	ctx := kong.Parse(&cli,
		kong.Name("localai-relay"),
		kong.Description("Run a gated relay for LocalAI P2P: a small public node that carries the traffic of your paired servers and devices when they cannot reach each other directly."),
		kong.UsageOnError(),
	)
	// With --json, standard output carries only the banner: logs go to standard error.
	logsetup.SetLogger(cli.LogFlags.Settings(), cli.Command.JSON)
	ctx.FatalIfErrorf(cli.Command.Execute(context.Background()))
}
