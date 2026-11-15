// SPDX-License-Identifier: MIT

// Command pirateface-search is the executable of the Pirate Face search
// add-on. It adds Pirate Face as a source of the model search. It keeps no
// data and has no UI: the host draws the rows and the filter.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/pirateface-search/internal/search"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

func main() {
	os.Exit(protocol.Run(os.Args, func(protocol.Startup) contract.Runtime {
		return search.New(search.Options{})
	}))
}
