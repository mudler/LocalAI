// SPDX-License-Identifier: MIT

// Command calendar is the executable of the Calendar add-on. It keeps one
// calendar for each owner in a SQLite file of its data directory and talks to
// no network and no account.
package main

import (
	"os"
	"path/filepath"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/addons/calendar/internal/store"
	"github.com/mudler/LocalAI/addons/calendar/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
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
		return tools.New(st)
	}))
}
