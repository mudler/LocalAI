package localai

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/services/backendupdates"
)

// BackendUpdatesProvider serves the backend update read model. The upgrade
// checker implements it and is nil-safe, so a server without a checker (backend
// installs off, no backend gallery) answers with disabled sources.
type BackendUpdatesProvider interface {
	// UpdatesSnapshot answers from this instance's cache.
	UpdatesSnapshot(ctx context.Context) backendupdates.Snapshot
	// CheckNow runs a check, waits for it and answers with the new data.
	CheckNow(ctx context.Context) backendupdates.Snapshot
}

// BackendUpdatesEndpoint lists the gallery and library backends that have a
// newer build, with the freshness of each source.
// @Summary List the backend updates
// @Description Gallery and library backends with a newer build, the state of each source (checkedAt, lastSuccessAt, error, stale) and what the automatic update covers. Answers from the cache of the instance that serves the request; in a cluster each frontend has its own cache. Contacts no registry.
// @Tags backends
// @Success 200 {object} backendupdates.Snapshot "Response"
// @Router /backends/updates [get]
func BackendUpdatesEndpoint(p BackendUpdatesProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		if p == nil {
			return c.JSON(http.StatusOK, backendupdates.Build(disabledInput()))
		}
		return c.JSON(http.StatusOK, p.UpdatesSnapshot(c.Request().Context()))
	}
}

// CheckBackendUpdatesEndpoint checks the registries now and answers when the
// check is done.
// @Summary Check for backend updates now
// @Description Refreshes the sources, waits for the check (at most 45 seconds) and returns the new data. A failed check shows as an error in the state of its source and keeps the previous list. Callers that arrive during a check share it; a call within 15 seconds of the last check is answered from it with throttled set. In a cluster the frontend that gets the request updates its own cache only. Installs nothing.
// @Tags backends
// @Success 200 {object} backendupdates.Snapshot "Response"
// @Router /backends/updates/check [post]
func CheckBackendUpdatesEndpoint(p BackendUpdatesProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		if p == nil {
			return c.JSON(http.StatusOK, backendupdates.Build(disabledInput()))
		}
		return c.JSON(http.StatusOK, p.CheckNow(c.Request().Context()))
	}
}

func disabledInput() backendupdates.Input {
	const why = "backend updates are not checked on this server"
	return backendupdates.Input{
		GalleryStatus: backendupdates.SourceStatus{Disabled: true, Reason: why},
		LibraryStatus: backendupdates.SourceStatus{Disabled: true, Reason: why},
	}
}
