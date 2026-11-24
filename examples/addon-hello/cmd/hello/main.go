// SPDX-License-Identifier: MIT

// Command hello is the executable of the example add-on.
package main

import (
	"os"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"

	"example.com/addon-hello/internal/tools"
)

func main() {
	os.Exit(protocol.Run(os.Args, func(protocol.Startup) contract.Runtime {
		return tools.Runtime{}
	}))
}
