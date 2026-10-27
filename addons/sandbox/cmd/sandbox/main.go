// SPDX-License-Identifier: MIT

// Command sandbox is the executable of the Sandbox add-on. It runs the code
// of a chat in an isolated sandbox; the files of the user stay with the host,
// which hands them over through the workspace operations.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/internal/idle"
)

func main() {
	os.Exit(idle.Run(os.Args))
}
