// SPDX-License-Identifier: MIT

// Command discord is the executable of the Discord add-on. It carries the
// messages of Discord bots for the chats: it keeps a Gateway link for each
// binding that the host polls, and sends the answers that the host hands it.
// The host passes the bot token on every call; it is never written to disk.
// The add-on keeps only which threads it follows, in its data directory.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/discord/transport"
	"github.com/mudler/LocalAI/addons/internal/hostclient"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func main() {
	os.Exit(protocol.Run(os.Args, func(s protocol.Startup) contract.Runtime {
		rt := transport.New(&transport.API{})
		rt.Gateway.StateDir = s.DataDir
		if h := hostclient.New(s); h != nil {
			rt.Host = h
		}
		return rt
	}))
}
