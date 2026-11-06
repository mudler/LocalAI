package signals

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// DefaultHookTimeout bounds how long the termination hooks may run. A hook
// that blocks must not keep the process alive past a pod's grace period.
const DefaultHookTimeout = 20 * time.Second

var (
	signalHandlers      []func()
	drainHandlers       []func(context.Context)
	drainTimeout        time.Duration
	signalHandlersMutex sync.Mutex
)

var setupSignalHandler = sync.OnceFunc(func() {
	c := make(chan os.Signal, 2)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	go signalHandler(c)
})

// RegisterGracefulTerminationHandler registers a hook that runs on SIGINT or
// SIGTERM, after the drain handlers have finished or timed out.
func RegisterGracefulTerminationHandler(fn func()) {
	signalHandlersMutex.Lock()
	defer signalHandlersMutex.Unlock()
	signalHandlers = append(signalHandlers, fn)
}

// RegisterDrainHandler registers a function that runs first on SIGINT or
// SIGTERM, before the termination hooks. The context ends when the drain
// timeout passes. Use it to stop accepting work and let in-flight work finish.
func RegisterDrainHandler(fn func(ctx context.Context)) {
	signalHandlersMutex.Lock()
	defer signalHandlersMutex.Unlock()
	drainHandlers = append(drainHandlers, fn)
}

// SetDrainTimeout sets how long the drain handlers may run. Zero or less
// skips the wait: the context of the handlers ends at once.
func SetDrainTimeout(d time.Duration) {
	signalHandlersMutex.Lock()
	defer signalHandlersMutex.Unlock()
	drainTimeout = d
}

func init() {
	setupSignalHandler()
}

func signalHandler(c chan os.Signal) {
	<-c

	signalHandlersMutex.Lock()
	drains := append([]func(context.Context){}, drainHandlers...)
	hooks := append([]func(){}, signalHandlers...)
	timeout := drainTimeout
	signalHandlersMutex.Unlock()

	// A second signal means the operator wants out now.
	go func() {
		<-c
		os.Exit(1)
	}()

	terminate(drains, hooks, timeout, DefaultHookTimeout)
	os.Exit(0)
}

// terminate runs the drain handlers, then the hooks. Each phase is bounded:
// when a phase overruns, terminate moves on and leaves the stuck function
// behind, because the caller exits right after.
func terminate(drains []func(context.Context), hooks []func(), drainFor, hooksFor time.Duration) {
	if len(drains) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), max(drainFor, 0))
		var wg sync.WaitGroup
		for _, fn := range drains {
			wg.Add(1)
			go func() {
				defer wg.Done()
				fn(ctx)
			}()
		}
		waitOrTimeout(&wg, drainFor+time.Second)
		cancel()
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, fn := range hooks {
			fn()
		}
	}()
	waitOrTimeout(&wg, hooksFor)
}

func waitOrTimeout(wg *sync.WaitGroup, d time.Duration) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
	}
}
