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
	"github.com/mudler/LocalAI/addons/sandbox/internal/egress"
	"github.com/mudler/LocalAI/addons/sandbox/internal/remote"
	"github.com/mudler/LocalAI/addons/sandbox/internal/tools"
	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// hostEgress asks the host about the connections of the sandboxes, over the
// host operations egress.check and egress.audit of the live call.
type hostEgress struct{ c *hostclient.Client }

func (h hostEgress) Check(ctx context.Context, op string, a egress.CheckArgs) (egress.Verdict, error) {
	var v egress.Verdict
	err := h.c.Do(ctx, op, "egress.check", a, &v)
	return v, err
}

func (h hostEgress) Audit(ctx context.Context, op string, evs []egress.Event) error {
	return h.c.Do(ctx, op, "egress.audit", map[string]any{"events": evs}, nil)
}

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
		// The network proxy of the sandboxes: guest port 3128 of every sandbox,
		// whatever the chat's preset (the host decides each connection).
		// Without a host channel nothing can decide, so no port is offered.
		var ports func(user, conv string) map[int]sandbox.PortHandler
		var begin func(ctx context.Context, user, conv, op string) func()
		if client != nil {
			proxy := egress.New(egress.Options{Host: hostEgress{client}})
			ports, begin = proxy.Ports, proxy.Begin
		}
		m, err := runner.New(runner.Options{
			Ports:   ports,
			Begin:   begin,
			Backend: sel.Backend,
			Store:   remote.New(client, s.DataDir),
			// The backends keep their sandboxes under run/. The manager needs the
			// folder to remove the snapshots it gives up, and leaves it alone at
			// start because each backend stops the sandboxes of an earlier
			// process itself before it removes their folders.
			StateDir:     filepath.Join(s.DataDir, "run"),
			KeepStateDir: true,
			Tools:        tools.Runs,
			Prepare: func(ctx context.Context, r runner.Request) context.Context {
				return remote.WithOperation(ctx, r.Operation)
			},
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
