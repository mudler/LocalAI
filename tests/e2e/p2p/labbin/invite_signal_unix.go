//go:build !windows

package main

import (
	"os"
	"syscall"
)

// inviteStatusSignal makes the server print the state of its invites.
var inviteStatusSignal os.Signal = syscall.SIGUSR1
