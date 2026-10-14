// SPDX-License-Identifier: MIT

// Command tables is the Tables addon executable. The LocalAI owner starts it
// with "--localai-addon-v1": descriptor 3 is a bound loopback listener and
// descriptor 4 carries the startup object. It holds no network access of its
// own and exits when the owner closes descriptor 4.
package main

import (
	"context"
	"net"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/addons/tables/internal/server"
	"github.com/mudler/LocalAI/addons/tables/internal/store"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "--localai-addon-v1" {
		os.Exit(2)
	}
	startup, rest, err := server.ReadStartup(os.NewFile(4, "startup"))
	if err != nil || !filepath.IsAbs(startup.DataDir) {
		os.Exit(3)
	}
	server.ExitWithParent(rest)
	listener, err := net.FileListener(os.NewFile(3, "listener"))
	if err != nil {
		os.Exit(6)
	}
	// A staged export of an earlier process has no handle any more.
	exports := filepath.Join(startup.DataDir, "exports")
	if err := server.ClearExports(exports); err != nil {
		os.Exit(7)
	}
	// An export never exceeds the host's bound for one file.
	st, err := store.Open(filepath.Join(startup.DataDir, "tables"), store.Options{ExportMaxBytes: min(store.DefaultExportBytes, startup.Limits.ArtifactBytes)})
	if err != nil {
		os.Exit(7)
	}
	defer st.Close()
	// The owner stopped every other process on this data before it started
	// this one, so the migration runs with exclusive ownership. Its progress is
	// in migration.json for the host; data in a format this build cannot read
	// stops the start and is left as it is.
	if err := st.Upgrade(context.Background(), filepath.Join(startup.DataDir, "migration.json")); err != nil {
		os.Exit(9)
	}
	if err := server.Serve(context.Background(), listener, startup, &server.Tables{Store: st, ExportDir: exports}); err != nil {
		os.Exit(8)
	}
}
