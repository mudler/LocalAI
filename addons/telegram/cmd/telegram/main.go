// SPDX-License-Identifier: MIT

// Command telegram is the executable of the Telegram add-on. It carries the
// messages of Telegram bots for the chats: the host asks it for updates and
// hands it the answers to send. It holds no token and no state.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/telegram/transport"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/hostclient"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

func main() {
	os.Exit(protocol.Run(os.Args, func(s protocol.Startup) contract.Runtime {
		rt := transport.Runtime{API: &transport.API{}}
		if h := hostclient.New(s); h != nil {
			rt.Host = h
		}
		return rt
	}))
}
