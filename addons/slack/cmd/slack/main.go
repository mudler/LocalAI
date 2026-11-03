// SPDX-License-Identifier: MIT

// Command slack is the executable of the Slack add-on. It carries the
// messages of Slack apps for the chats: it keeps a Socket Mode link for each
// binding that the host polls, and sends the answers that the host hands it.
// The host passes the tokens on every call; they are never written to disk.
// The add-on keeps only which threads it follows, in its data directory.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/internal/hostclient"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/slack/transport"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func main() {
	os.Exit(protocol.Run(os.Args, func(s protocol.Startup) contract.Runtime {
		rt := transport.New(&transport.API{})
		rt.Socket.StateDir = s.DataDir
		if h := hostclient.New(s); h != nil {
			rt.Host = h
		}
		return rt
	}))
}
