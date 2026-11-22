// SPDX-License-Identifier: MIT

// Command ollama-search is the executable of the Ollama search add-on. It adds
// the public model library of Ollama as a source of the model search. It keeps
// no data and has no UI: the host draws the rows and the filters.
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/ollama-search/internal/search"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

func main() {
	os.Exit(protocol.Run(os.Args, func(protocol.Startup) contract.Runtime {
		return search.New(search.Options{})
	}))
}
