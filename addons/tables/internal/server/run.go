// SPDX-License-Identifier: MIT
package server

import (
	"context"
	"net"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/tables/internal/store"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Hooks let a test-only executable change what the one product executable
// does. The product's main passes the zero value, which changes nothing.
type Hooks struct {
	// Store receives the open store and the add-on's data directory before the
	// protocol is served.
	Store func(st *store.Store, dataDir string)
	// Runtime wraps the runtime the protocol serves.
	Runtime func(rt contract.Runtime, dataDir string) contract.Runtime
}

// Run is the whole behaviour of the Tables executable: the owner starts it
// with "--localai-addon-v1", descriptor 3 is a bound loopback listener and
// descriptor 4 carries the startup object. It returns the process exit code.
func Run(args []string, hooks Hooks) int {
	if len(args) != 2 || args[1] != "--localai-addon-v1" {
		return 2
	}
	startup, rest, err := protocol.ReadStartup(os.NewFile(4, "startup"))
	if err != nil || !filepath.IsAbs(startup.DataDir) {
		return 3
	}
	protocol.ExitWithParent(rest)
	listener, err := net.FileListener(os.NewFile(3, "listener"))
	if err != nil {
		return 6
	}
	// A staged export of an earlier process has no handle any more.
	exports := filepath.Join(startup.DataDir, "exports")
	if err := ClearExports(exports); err != nil {
		return 7
	}
	// An export never exceeds the host's bound for one file.
	st, err := store.Open(filepath.Join(startup.DataDir, "tables"), store.Options{ExportMaxBytes: min(store.DefaultExportBytes, startup.Limits.ArtifactBytes)})
	if err != nil {
		return 7
	}
	defer func() { _ = st.Close() }()
	// The owner stopped every other process on this data before it started
	// this one, so the migration runs with exclusive ownership. Its progress is
	// in migration.json for the host; data in a format this build cannot read
	// stops the start and is left as it is.
	if err := st.Upgrade(context.Background(), filepath.Join(startup.DataDir, "migration.json")); err != nil {
		return 9
	}
	if hooks.Store != nil {
		hooks.Store(st, startup.DataDir)
	}
	var rt contract.Runtime = &Tables{Store: st, ExportDir: exports}
	if hooks.Runtime != nil {
		rt = hooks.Runtime(rt, startup.DataDir)
	}
	if err := protocol.Serve(context.Background(), listener, startup, rt); err != nil {
		return 8
	}
	return 0
}
