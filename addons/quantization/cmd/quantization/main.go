// SPDX-License-Identifier: MIT

// Command quantization is the executable of the quantization add-on. It serves
// the typed tools of the add-on and reaches the job engine through the host
// operations; the add-on UI works in the browser through the SDK.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/internal/hostclient"
	"github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/addons/quantization/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func main() {
	os.Exit(protocol.Run(os.Args, func(s protocol.Startup) contract.Runtime {
		return tools.New(hostclient.New(s), s.DataDir)
	}))
}
