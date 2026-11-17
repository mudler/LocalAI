// Command labbin is the binary the P2P NAT lab runs inside its containers
// (see tests/e2e/p2p/lab and the README next to it).
//
// PLACEHOLDER. The packages it is meant to drive (core/services/p2pnet,
// core/services/p2prelay) are not in the tree yet. Until they are, this program
// only answers the "caps" probe, with the single capability "placeholder", so
// that the lab harness knows there is nothing to test and stops with a notice
// instead of a failure. The change that adds the packages replaces this file
// with the real command line described in tests/e2e/p2p/lab/README.md.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "caps" {
		fmt.Println("placeholder")
		return
	}
	fmt.Fprintln(os.Stderr, "labbin is a placeholder: the P2P packages are not wired in yet")
	os.Exit(2)
}
