// SPDX-License-Identifier: MIT

// Command slack is the executable of the Slack add-on. It carries the
// messages of Slack apps for the chats: it keeps a Socket Mode link for each
// binding that the host polls, and sends the answers that the host hands it.
// The host passes the tokens on every call; nothing is written to disk.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/slack/transport"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func main() {
	os.Exit(protocol.Run(os.Args, func(protocol.Startup) contract.Runtime { return transport.New(&transport.API{}) }))
}
