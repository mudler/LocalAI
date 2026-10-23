// SPDX-License-Identifier: MIT

// Command tables is the Tables addon executable. The LocalAI owner starts it
// with "--localai-addon-v1": descriptor 3 is a bound loopback listener and
// descriptor 4 carries the startup object. It holds no network access of its
// own and exits when the owner closes descriptor 4.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/tables/internal/server"
)

func main() {
	os.Exit(server.Run(os.Args, server.Hooks{}))
}
