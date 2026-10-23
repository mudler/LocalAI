// SPDX-License-Identifier: MIT

// Command face is the executable of the Face add-on. It serves no tools:
// the add-on works in the browser, through the SDK and the core API.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/internal/idle"
)

func main() {
	os.Exit(idle.Run(os.Args))
}
