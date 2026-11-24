package signals

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// terminationChannels are the channels registered with NotifyTermination.
// A stop request that is not an OS signal (the Windows service manager)
// reaches them through deliverTermination.
var (
	terminationMu       sync.Mutex
	terminationChannels []chan<- os.Signal
)

// NotifyTermination makes c receive a value when the process is asked to
// stop: SIGINT or SIGTERM, Ctrl+C or Ctrl+Break and closing the console on
// Windows, and a stop or shutdown request from the Windows service manager
// when the process runs as a Windows service. Use it instead of signal.Notify
// for a shutdown path, so it also works for a Windows service. Like
// signal.Notify, it never blocks: c must have room, or a value is dropped.
func NotifyTermination(c chan<- os.Signal) {
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	terminationMu.Lock()
	terminationChannels = append(terminationChannels, c)
	terminationMu.Unlock()
	startServiceBridge()
}

// deliverTermination sends SIGTERM to every channel registered with
// NotifyTermination. It never blocks.
func deliverTermination() {
	terminationMu.Lock()
	channels := append([]chan<- os.Signal(nil), terminationChannels...)
	terminationMu.Unlock()
	for _, c := range channels {
		select {
		case c <- syscall.SIGTERM:
		default:
		}
	}
}
