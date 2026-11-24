// SPDX-License-Identifier: MIT

// Command home-assistant is the executable of the Home Assistant add-on. It
// reads a smart home for the chats and the agents, and changes it only through
// its two write tools, under the policy kept in the add-on's data folder and
// the write mode the host keeps for the connection. The token comes with each
// call from the host; the add-on stores none.
package main

import (
	"context"
	"os"

	"github.com/mudler/LocalAI/addons/home-assistant/internal/haapi"
	"github.com/mudler/LocalAI/addons/home-assistant/internal/platform"
	"github.com/mudler/LocalAI/addons/home-assistant/internal/policy"
	"github.com/mudler/LocalAI/addons/home-assistant/internal/tools"
	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/hostclient"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

type runtime struct {
	*tools.Runtime
	platform platform.Platform
}

func (r runtime) Connector(ctx context.Context, req contract.ConnectorRequest) (contract.ConnectorResponse, error) {
	return connector.Serve(ctx, r.platform, req)
}

// hostAdmin asks the host whether the user of a request is an administrator.
type hostAdmin struct{ c *hostclient.Client }

func (a hostAdmin) IsAdmin(ctx context.Context, operationID string) bool {
	return a.c.Do(ctx, operationID, "workspace.admin", nil, nil) == nil
}

func main() {
	os.Exit(protocol.Run(os.Args, func(st protocol.Startup) contract.Runtime {
		rt := tools.New(haapi.New(), policy.Open(st.DataDir)).WithDataDir(st.DataDir)
		rt.Admin = hostAdmin{hostclient.New(st)}
		return runtime{Runtime: rt}
	}))
}
