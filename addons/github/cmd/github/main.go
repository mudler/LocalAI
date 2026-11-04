// SPDX-License-Identifier: MIT

// Command github is the executable of the GitHub add-on. It reads repositories
// for the chats and the agents with a token that the host keeps for the person
// and hands over with each call. The add-on stores no token. It changes GitHub
// only through its write tools, and only as far as the host allows.
package main

import (
	"context"
	"os"

	"github.com/mudler/LocalAI/addons/github/internal/ghapi"
	"github.com/mudler/LocalAI/addons/github/internal/platform"
	"github.com/mudler/LocalAI/addons/github/internal/tools"
	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
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
		api := ghapi.New()
		return runtime{Runtime: tools.New(api).WithDataDir(st.DataDir), platform: platform.Platform{API: api}}
	}))
}
