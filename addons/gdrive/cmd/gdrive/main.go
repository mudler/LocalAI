// SPDX-License-Identifier: MIT

// Command gdrive is the executable of the Google Drive add-on. It reads the
// files of the folders a person allows, for the chats and the agents, with an
// access token that the host keeps for the person, renews and hands over with
// each call. The add-on stores no token. It changes Drive only through its
// write tools, and only as far as the host allows.
package main

import (
	"context"
	"os"

	"github.com/mudler/LocalAI/addons/gdrive/internal/driveapi"
	"github.com/mudler/LocalAI/addons/gdrive/internal/platform"
	"github.com/mudler/LocalAI/addons/gdrive/internal/tools"
	"github.com/mudler/LocalAI/addons/internal/connector"
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
		api := driveapi.New()
		return runtime{Runtime: tools.New(api).WithDataDir(st.DataDir), platform: platform.Platform{API: api}}
	}))
}
