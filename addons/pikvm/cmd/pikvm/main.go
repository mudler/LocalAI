// SPDX-License-Identifier: MIT

// Command pikvm is the executable of the PiKVM add-on. It lets chats and
// agents read the state of a PiKVM (or compatible kvmd) device and, when a
// person confirms each call, press its power buttons and use its keyboard. The
// user name and password come with each call from the host; the add-on stores
// none, and it talks to no device but the one of the connection.
package main

import (
	"context"
	"os"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/pikvm/internal/kvmd"
	"github.com/mudler/LocalAI/addons/pikvm/internal/platform"
	"github.com/mudler/LocalAI/addons/pikvm/internal/settings"
	"github.com/mudler/LocalAI/addons/pikvm/internal/tools"
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
		rt := tools.New(kvmd.New(), settings.Open(st.DataDir)).WithDataDir(st.DataDir)
		rt.Admin = hostAdmin{hostclient.New(st)}
		return runtime{Runtime: rt}
	}))
}
