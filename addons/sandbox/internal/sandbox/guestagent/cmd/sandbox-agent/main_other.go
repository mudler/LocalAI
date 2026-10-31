//go:build !linux

// Command sandbox-agent is the guest agent. It runs only in a Linux guest.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "sandbox-agent runs only in a Linux guest")
	os.Exit(1)
}
