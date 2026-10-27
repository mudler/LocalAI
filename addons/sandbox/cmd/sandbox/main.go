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

func main() {
	os.Exit(protocol.Run(os.Args, func(s protocol.Startup) contract.Runtime {
		store := remote.New(hostclient.New(s), s.DataDir)
		m, err := manager.New(manager.Options{
			Backend:  backendFor(s.DataDir),
			Store:    store,
			StateDir: filepath.Join(s.DataDir, "sandboxes"),
		})
		if err != nil {
			// A manager that cannot start serves no sandbox; every tool then
			// answers that the host cannot run commands.
			xlog.Error("sandbox: cannot start the manager", "error", err)
			return tools.New(unavailable{})
		}
		m.Start(context.Background())
		return tools.New(m)
	}))
}
