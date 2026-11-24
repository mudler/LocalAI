//go:build !windows

package signals

// startServiceBridge does nothing: only Windows has a service manager that
// stops a process by a request instead of a signal.
func startServiceBridge() {}

// reportServiceStopped does nothing outside Windows.
func reportServiceStopped() {}
