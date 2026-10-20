// SPDX-License-Identifier: MIT

// Command talk is the executable of the Talk add-on. It serves no tools:
// the add-on works in the browser, through the SDK and the core API.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/internal/idle"
)

func main() {
	os.Exit(idle.Run(os.Args))
}
