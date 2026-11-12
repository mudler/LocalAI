// SPDX-License-Identifier: MIT

// Command client-example is the reference add-on for paired clients.
// The owner starts it with "--localai-addon-v1".
package main

import (
	"os"

	"github.com/mudler/LocalAI/addons/internal/clientdemo"
)

func main() {
	os.Exit(clientdemo.Run(os.Args))
}
