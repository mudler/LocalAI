// SPDX-License-Identifier: MIT

// Command jellyfin is the executable of the Jellyfin add-on. It reads a
// Jellyfin media server for the chats and the agents, and remote-controls its
// players when the person allows it, with an API key that the host keeps for
// the person and hands over with each call. The add-on stores no key and
// talks to no server but the one of the connection.
package main

import (
	"context"
	"os"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/jellyfin/internal/jfapi"
	"github.com/mudler/LocalAI/addons/jellyfin/internal/platform"
	"github.com/mudler/LocalAI/addons/jellyfin/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

type runtime struct {
	*tools.Runtime
	platform platform.Platform
}

func (r runtime) Connector(ctx context.Context, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return connector.Serve(ctx, r.platform, req)
}

func main() {
	os.Exit(protocol.Run(os.Args, func(st protocol.Startup) contract.Runtime {
		return runtime{Runtime: tools.New(jfapi.New()).WithDataDir(st.DataDir)}
	}))
}
