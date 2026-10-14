// SPDX-License-Identifier: MIT

// Package packaging builds, checks, merges and publishes first-party add-on
// OCI packages. It is the code behind scripts/addons/package.sh,
// scripts/addons/channel.sh and the add-on package workflow, so the rules
// that workflow relies on are code that a test can run.
//
// Every operation fails closed: a missing input is an error, never a default.
package packaging

import (
	"fmt"
	"io"
	"os"
)

// stdout receives the key=value result lines of the subcommands.
var stdout io.Writer = os.Stdout

var commands = map[string]func(args []string) error{
	"matrix":       cmdMatrix,
	"channel":      cmdChannel,
	"build":        cmdBuild,
	"verify":       cmdVerify,
	"extract":      cmdExtract,
	"native-check": cmdNativeCheck,
	"merge":        cmdMerge,
	"publish":      cmdPublish,
	"promote":      cmdPromote,
	"catalog":      cmdCatalog,
	"policy":       cmdPolicy,
}

// Run executes one subcommand of the addonpkg command line.
func Run(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: addonpkg <subcommand> [flags]")
	}
	cmd, ok := commands[args[0]]
	if !ok {
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
	previous := stdout
	stdout = out
	defer func() { stdout = previous }()
	return cmd(args[1:])
}
