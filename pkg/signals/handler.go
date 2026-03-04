package signals

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var (
	signalHandlers      []func()
	signalHandlersMutex sync.Mutex
	signalHandlersOnce  sync.Once
)

func RegisterGracefulTerminationHandler(fn func()) {
	signalHandlersMutex.Lock()
	defer signalHandlersMutex.Unlock()
	signalHandlers = append(signalHandlers, fn)
}

func init() {
	signalHandlersOnce.Do(func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
		go signalHandler(c)
	})
}

func signalHandler(c chan os.Signal) {
	<-c

	signalHandlersMutex.Lock()
	defer signalHandlersMutex.Unlock()
	for _, fn := range signalHandlers {
		fn()
	}

	os.Exit(0)
}
