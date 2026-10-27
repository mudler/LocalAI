// SPDX-License-Identifier: MIT

// Command office is the executable of the Office add-on. It reads and edits
// Word, Excel, PowerPoint and PDF files for the chats; the files themselves
// stay with the host, which hands them over through the workspace operations.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/internal/hostclient"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/office/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func main() {
	os.Exit(protocol.Run(os.Args, func(s protocol.Startup) contract.Runtime {
		return tools.New(hostclient.New(s), s.DataDir)
	}))
}
