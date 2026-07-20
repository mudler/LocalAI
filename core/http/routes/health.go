package routes

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// HealthRoutes registers the liveness (/healthz) and readiness (/readyz)
// probes.
//
// ready reports whether the process has finished starting up. It is consulted
// per request rather than sampled once at registration time, because readiness
// is a lifecycle property that changes after the router has been built. A nil
// ready fails open: an embedder that does not wire readiness keeps the
// historical always-200 behaviour instead of being stuck out of rotation
// forever.
func HealthRoutes(app *echo.Echo, ready func() bool) {
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
		if ready != nil && !ready() {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{
				"status": "starting",
				"reason": "startup preload in progress",
			})
		}
		return c.NoContent(http.StatusOK)
	})
}
