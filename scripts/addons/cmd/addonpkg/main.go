// SPDX-License-Identifier: MIT

// Command addonpkg is the command line of pkg/addons/packaging: build, verify
// and publish first-party add-on OCI packages. scripts/addons/package.sh and
// the add-on package workflow call it.
package main

import (
	"fmt"
	"os"

	"github.com/mudler/LocalAI/pkg/addons/packaging"
)

func main() {
	if err := packaging.Run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "addonpkg: %v\n", err)
		os.Exit(1)
	}
}
