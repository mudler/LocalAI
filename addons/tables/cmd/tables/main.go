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
	st, err := store.Open(filepath.Join(startup.DataDir, "tables"), store.Options{})
	if err != nil {
		os.Exit(7)
	}
	defer st.Close()
	if err := server.Serve(context.Background(), listener, startup, &server.Tables{Store: st}); err != nil {
		os.Exit(8)
	}
}
