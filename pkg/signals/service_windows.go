//go:build windows

package signals

import (
	"sync"
	"time"

	"github.com/mudler/xlog"
	"golang.org/x/sys/windows/svc"
)

// When local-ai.exe runs as a Windows service, the service manager does not
// send a signal to stop it. It sends a stop or shutdown request through the
// service control dispatcher, and it fails the start of a service that does
// not connect to the dispatcher within 30 seconds. The bridge connects, reports
// the service as running, and turns a stop or shutdown request into the same
// SIGTERM path a Linux service gets.

var (
	serviceBridgeOnce sync.Once
	serviceStopped    = make(chan struct{})
	serviceStopOnce   sync.Once
	serviceDone       = make(chan struct{})
)

// serviceStopWaitHint is how long the service manager waits for the stop
// before it treats the service as hung. It covers the whole shutdown budget.
var serviceStopWaitHint = WorstCase(DefaultDrainTimeout) + 5*time.Second

func startServiceBridge() {
	serviceBridgeOnce.Do(func() {
		isService, err := svc.IsWindowsService()
		if err != nil {
			xlog.Warn("Could not tell whether LocalAI runs as a Windows service", "error", err)
			close(serviceDone)
			return
		}
		if !isService {
			close(serviceDone)
			return
		}
		go func() {
			defer close(serviceDone)
			// The name is ignored for a service that has its own process.
			if err := svc.Run("", &serviceHandler{stop: deliverTermination, stopped: serviceStopped}); err != nil {
				xlog.Error("The Windows service control dispatcher failed", "error", err)
			}
		}()
	})
}

// reportServiceStopped tells the service manager that the service stopped,
// and waits a short time for it to receive the status, so the stop is not
// recorded as an unexpected termination. Call it right before the process
// exits.
func reportServiceStopped() {
	serviceStopOnce.Do(func() { close(serviceStopped) })
	select {
	case <-serviceDone:
	case <-time.After(5 * time.Second):
	}
}

// serviceHandler implements svc.Handler.
type serviceHandler struct {
	stop    func()
	stopped <-chan struct{}
}

func (h *serviceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	const accepted = svc.AcceptStop | svc.AcceptShutdown
	status <- svc.Status{State: svc.Running, Accepts: accepted}
	current := svc.Status{State: svc.Running, Accepts: accepted}
	for {
		select {
		case <-h.stopped:
			// The process stopped on its own (Ctrl+C in a console session, an
			// internal fatal error) or finished a requested stop.
			status <- svc.Status{State: svc.StopPending}
			return false, 0
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				status <- current
			case svc.Stop, svc.Shutdown:
				current = svc.Status{State: svc.StopPending, WaitHint: uint32(serviceStopWaitHint / time.Millisecond)}
				status <- current
				h.stop()
			}
		}
	}
}
