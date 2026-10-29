// SPDX-License-Identifier: MIT

// Command sandbox is the executable of the Sandbox add-on. It runs the code
// of a chat in an isolated sandbox; the files of the user stay with the host,
// which hands them over through the workspace operations.
package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/addons/internal/hostclient"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/sandbox/internal/manager"
	"github.com/mudler/LocalAI/addons/sandbox/internal/remote"
	"github.com/mudler/LocalAI/addons/sandbox/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// hostAdmin asks the host whether the user of a request is an administrator.
type hostAdmin struct{ c *hostclient.Client }

func (a hostAdmin) IsAdmin(ctx context.Context, operationID string) bool {
	return a.c.Do(ctx, operationID, "workspace.admin", nil, nil) == nil
}

func main() {
	os.Exit(protocol.Run(os.Args, func(s protocol.Startup) contract.Runtime {
		client := hostclient.New(s)
		sel, err := newSelector(s.DataDir)
		if err != nil {
			// Without settings no sandbox runs; every tool says so.
			xlog.Error("sandbox: cannot read the settings", "error", err)
			return tools.New(unavailable{}, nil, hostAdmin{client})
		}
		m, err := manager.New(manager.Options{
			Backend: sel.Backend,
			Store:   remote.New(client, s.DataDir),
			// The backends keep their sandboxes under run/. The manager needs the
			// folder to remove the snapshots it gives up, and leaves it alone at
			// start because each backend stops the sandboxes of an earlier
			// process itself before it removes their folders.
			StateDir:     filepath.Join(s.DataDir, "run"),
			KeepStateDir: true,
		})
		if err != nil {
			xlog.Error("sandbox: cannot start the manager", "error", err)
			return tools.New(unavailable{}, nil, hostAdmin{client})
		}
		m.Start(context.Background())
		// Start the backend now: it removes the sandboxes that a process before
		// this one left running. Waiting for the first call would leave them
		// until somebody uses the add-on.
		go sel.Warm()
		return tools.New(m, settingsAdapter{Selector: sel, m: m}, hostAdmin{client})
	}))
}
