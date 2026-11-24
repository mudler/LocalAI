package routes

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// Readiness is what the /readyz probe reads. Every member is consulted per
// request rather than sampled once at registration time, because readiness is
// a lifecycle property that changes after the router has been built.
type Readiness struct {
	// Ready reports whether the process has finished starting up. A nil Ready
	// fails open: an embedder that does not wire readiness keeps the
	// historical always-200 behaviour instead of being stuck out of rotation
	// forever.
	Ready func() bool
	// Draining, when set, reports that shutdown has started. /readyz then says
	// so in its body instead of blaming the startup.
	Draining func() bool
	// Unavailable, when set, returns why a process that has started must not
	// take traffic now, or "" when it may. In distributed mode it is a NATS
	// server that is configured and not reachable, or a missing peer address.
	Unavailable func() string
}

// HealthRoutes registers the liveness (/healthz) and readiness (/readyz)
// probes.
func HealthRoutes(app *echo.Echo, r Readiness) {
	// Liveness: "is this process alive?". Deliberately independent of
	// readiness — a long startup preload must not read as a hung process, or
	// an orchestrator restarts the pod mid-download and the preload can never
	// finish.
	app.GET("/healthz", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	// Readiness: "should this replica receive traffic?". Answering 200 while
	// the process is still starting up makes a Kubernetes Service add the pod
	// to its endpoints early, and traffic round-robins onto a replica that
	// cannot serve it.
	app.GET("/readyz", func(c echo.Context) error {
		if r.Ready != nil && !r.Ready() {
			if r.Draining != nil && r.Draining() {
				return c.JSON(http.StatusServiceUnavailable, map[string]string{
					"status": "draining",
					"reason": "shutdown in progress",
				})
			}
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"status": "starting",
				"reason": "startup preload in progress",
			})
		}
		if r.Unavailable != nil {
			if reason := r.Unavailable(); reason != "" {
				return c.JSON(http.StatusServiceUnavailable, map[string]string{
					"status": "unavailable",
					"reason": reason,
				})
			}
		}
		return c.NoContent(http.StatusOK)
	})
}
