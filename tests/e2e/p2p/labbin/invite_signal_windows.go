package main

import "os"

// inviteStatusSignal is nil on Windows, which has no SIGUSR1: the server
// prints no invite state there.
var inviteStatusSignal os.Signal
