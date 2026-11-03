// SPDX-License-Identifier: MIT

// Command memory is the executable of the Memory add-on. It serves no tools:
// the add-on declares the index files of the person's notes and ships the
// skills that tell a model to read them. The host loads the files, so this
// process only speaks the add-on protocol.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/internal/idle"
)

func main() {
	os.Exit(idle.Run(os.Args))
}
