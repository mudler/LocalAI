// SPDX-License-Identifier: MIT

// Command ssh is the executable of the SSH add-on. It lets chats and agents
// run commands on a remote machine over SSH. The secret (a private key or a
// user name and password) comes with each call from the host; the add-on
// stores none, and it talks to no machine but the one of the connection, whose
// host key is pinned in its address.
package main

import (
	"context"
	"os"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/ssh/internal/platform"
	"github.com/mudler/LocalAI/addons/ssh/internal/tools"
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
		return runtime{Runtime: tools.New().WithDataDir(st.DataDir)}
	}))
}
