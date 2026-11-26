// SPDX-License-Identifier: MIT

// Command kubernetes is the executable of the Kubernetes add-on.
package main

import (
	"os"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/idle"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

func main() {
	os.Exit(protocol.Run(os.Args, func(protocol.Startup) contract.Runtime { return idle.Runtime{} }))
}
