// SPDX-License-Identifier: MIT

// Command calendar is the executable of the Calendar add-on. It keeps one
// calendar for each owner in a SQLite file of its data directory and talks to
// no network and no account.
package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/addons/calendar/internal/store"
	"github.com/mudler/LocalAI/addons/calendar/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/hostclient"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

func main() {
	os.Exit(protocol.Run(os.Args, func(s protocol.Startup) contract.Runtime {
		st, err := store.Open(filepath.Join(s.DataDir, "calendar"))
		if err != nil {
			// A runtime that cannot open its file would answer nothing useful;
			// stop, and let the owner report that the add-on failed to start.
			xlog.Error("calendar: cannot open the calendar file", "error", err)
			os.Exit(7)
		}
		rt := tools.New(st)
		rt.Events = hostEvents{hostclient.New(s)}
		return rt
	}))
}

// hostEvents reports the changes of the calendar with the events.emit host
// operation, under the live call that made them.
type hostEvents struct{ c *hostclient.Client }

func (h hostEvents) Emit(ctx context.Context, operationID, typ string, payload any, dedupe string) error {
	return h.c.Do(ctx, operationID, "events.emit", map[string]any{"type": typ, "payload": payload, "dedupe_key": dedupe}, nil)
}
