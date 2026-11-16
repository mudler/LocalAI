// SPDX-License-Identifier: MIT
package main

import (
	"github.com/mudler/LocalAI/addons/asset-pipeline/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"os"
)

func main() {
	os.Exit(protocol.Run(os.Args, func(s protocol.Startup) contract.Runtime { return tools.New(s) }))
}
