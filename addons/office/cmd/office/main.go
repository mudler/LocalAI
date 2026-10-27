// SPDX-License-Identifier: MIT

// Command office is the executable of the Office add-on. It reads and edits
// Word, Excel, PowerPoint and PDF files for the chats; the files themselves
// stay with the host, which hands them over through the workspace operations.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/internal/idle"
)

func main() {
	os.Exit(idle.Run(os.Args))
}
