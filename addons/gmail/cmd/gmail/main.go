// SPDX-License-Identifier: MIT

// Command gmail is the executable of the Gmail add-on. It reads the mail of
// the account a person connects, and with approval saves drafts and sends
// them, for the chats and the agents. It uses an access token that the host
// keeps for the person, renews and hands over with each call. The add-on
// stores no token. It changes the mailbox only through its write tools, and
// only as far as the host allows.
package main

import (
	"context"
	"os"

	"github.com/mudler/LocalAI/addons/gmail/internal/gmailapi"
	"github.com/mudler/LocalAI/addons/gmail/internal/platform"
	"github.com/mudler/LocalAI/addons/gmail/internal/tools"
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
		api := gmailapi.New()
		return runtime{Runtime: tools.New(api).WithDataDir(st.DataDir), platform: platform.Platform{API: api}}
	}))
}
