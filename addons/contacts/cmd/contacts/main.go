// SPDX-License-Identifier: MIT

// Command contacts is the executable of the Contacts add-on. It keeps one
// contact book for each owner in a SQLite file of its data directory and talks
// to no network and no account.
package main

import (
	"os"
	"path/filepath"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/addons/contacts/internal/store"
	"github.com/mudler/LocalAI/addons/contacts/internal/tools"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func main() {
	os.Exit(protocol.Run(os.Args, func(s protocol.Startup) contract.Runtime {
		st, err := store.Open(filepath.Join(s.DataDir, "contacts"))
		if err != nil {
			// A runtime that cannot open its file would answer nothing useful;
			// stop, and let the owner report that the add-on failed to start.
			xlog.Error("contacts: cannot open the contact file", "error", err)
			os.Exit(7)
		}
		return tools.New(st)
	}))
}
