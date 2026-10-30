// SPDX-License-Identifier: MIT

// Command web is the executable of the Web add-on. It serves web_search and
// web_fetch in place of the built-in tools of the chats, with search
// providers that need a key and with the text of PDF files.
package main

import (
	"context"
	"os"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/addons/internal/hostclient"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/web/internal/settings"
	"github.com/mudler/LocalAI/addons/web/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// hostAdmin asks the host whether the user of a request is an administrator.
type hostAdmin struct{ c *hostclient.Client }

func (a hostAdmin) IsAdmin(ctx context.Context, operationID string) bool {
	return a.c.Do(ctx, operationID, "workspace.admin", nil, nil) == nil
}

func main() {
	os.Exit(protocol.Run(os.Args, func(s protocol.Startup) contract.Runtime {
		store, err := settings.Open(s.DataDir)
		if err != nil {
			// Without settings only fetch works; search and the settings tools
			// say why.
			xlog.Error("web: cannot read the settings", "error", err)
		}
		return tools.New(tools.Options{Settings: store, Admin: hostAdmin{hostclient.New(s)}})
	}))
}
