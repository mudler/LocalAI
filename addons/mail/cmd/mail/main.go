// SPDX-License-Identifier: MIT

// Command mail is the executable of the Mail add-on. It reads and writes email
// over IMAP and SMTP for the chats and the agents, with the connection that the
// host keeps sealed for the person and hands over with each call. The add-on
// stores no account and no mail. It sends or saves mail only through its two
// write tools, and only as far as the host allows.
package main

import (
	"context"
	"os"

	"github.com/mudler/LocalAI/addons/internal/connector"
	"github.com/mudler/LocalAI/addons/mail/internal/dial"
	"github.com/mudler/LocalAI/addons/mail/internal/platform"
	"github.com/mudler/LocalAI/addons/mail/internal/tools"
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
		d := dial.New(dialOptions())
		return runtime{Runtime: tools.New(d).WithDataDir(st.DataDir), platform: platform.Platform{Dialer: d}}
	}))
}
