package localai

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/upgraderun"
	"github.com/mudler/xlog"
)

// BackendLibraryInstaller lists the installed library backends that have a
// newer build in their library.
type BackendLibraryInstaller interface {
	Updates(ctx context.Context) ([]backendlib.Update, error)
}

// BackendLibraryJobs records the status of an install under a job ID. The
// gallery service is the one the backend gallery installs use, so the same
// route, GET /backends/jobs/{id}, answers for both.
type BackendLibraryJobs = backendlib.JobStatus

// LibraryInstallJobs starts install jobs under the guard every library
// install of this node shares (backendlib.JobRunner): the route and the
// library items of upgrade runs never write one directory twice.
type LibraryInstallJobs interface {
	Start(ctx context.Context, status backendlib.JobStatus, job backendlib.Job) (string, error)
}

// BackendLibraryInstallRequest names one library entry of kind backend.
type BackendLibraryInstallRequest struct {
	// SourceID is the library the entry comes from.
	SourceID string `json:"sourceId"`
	// Name is the backend name of the entry.
	Name string `json:"name"`
	// Version is the version or the channel of the entry.
	Version string `json:"version"`
}

// InstallBackendFromLibraryEndpoint starts the install of a backend from a
// library entry on this node and answers at once with a job. The job reports
// the steps of the install (resolving, verifying, size, fetching, writing) as
// the phase of its status, with the byte progress of the fetch, and ends with
// the result or the reason of the refusal. The library resolves the entry, so
// the request cannot name an image, a digest or a policy. refuse, when set, is
// asked first (the upgrade runs answer run_active while a run runs).
// @Summary Install a backend from a library entry on this node.
// @Tags backends
// @Param request body BackendLibraryInstallRequest true "library, backend name and version"
// @Success 200 {object} schema.BackendResponse "the job; read its status at the status URL"
// @Failure 400 {object} schema.ErrorResponse "the request does not name an entry"
// @Failure 409 {object} schema.ErrorResponse "the same backend is already being installed (also by an upgrade run), or an upgrade run runs (type run_active)"
// @Failure 503 {object} schema.ErrorResponse "on a frontend: the add-on owner is not reachable, does not serve library calls, or runs on another platform; the message says which"
// @Router /backends/library/install [post]
func InstallBackendFromLibraryEndpoint(installs LibraryInstallJobs, jobs BackendLibraryJobs, refuse func() error) echo.HandlerFunc {
	return func(c echo.Context) error {
		var in BackendLibraryInstallRequest
		if err := c.Bind(&in); err != nil {
			return libraryError(c, http.StatusBadRequest, "invalid request")
		}
		if in.SourceID == "" || in.Name == "" || in.Version == "" {
			return libraryError(c, http.StatusBadRequest, "sourceId, name and version are required")
		}
		if refuse != nil {
			if err := refuse(); err != nil {
				if upgraderun.CodeOf(err) == upgraderun.CodeRunActive {
					return c.JSON(http.StatusConflict, schema.ErrorResponse{Error: &schema.APIError{Code: http.StatusConflict, Message: runActiveMessage, Type: upgraderun.CodeRunActive}})
				}
				xlog.Error("backend library install: could not check the upgrade runs", "error", err)
				return libraryError(c, http.StatusServiceUnavailable, "the upgrade runs could not be checked; try again")
			}
		}
		// The request ends before the install does: the job keeps the values
		// of the request (the actor the owner checks) and drops its
		// cancellation.
		id, err := installs.Start(c.Request().Context(), jobs, backendlib.Job{SourceID: in.SourceID, Backend: in.Name, Version: in.Version})
		switch {
		case errors.Is(err, backendlib.ErrInstalling):
			return libraryError(c, http.StatusConflict, err.Error())
		case err != nil:
			return libraryError(c, http.StatusServiceUnavailable, err.Error())
		}
		return c.JSON(http.StatusOK, schema.BackendResponse{ID: id, StatusURL: fmt.Sprintf("%sbackends/jobs/%s", middleware.BaseURL(c), id)})
	}
}

// BackendLibraryUpdatesEndpoint lists the installed library backends that have
// a newer build in their library. It reads the cached index and installs
// nothing.
// @Summary List the installed library backends with a newer build in their library.
// @Tags backends
// @Success 200 {object} map[string][]backendlib.Update "updates"
// @Failure 503 {object} schema.ErrorResponse "on a frontend: the add-on owner is not reachable, does not serve library calls, or runs on another platform; the message says which"
// @Router /backends/library/updates [get]
func BackendLibraryUpdatesEndpoint(installer BackendLibraryInstaller) echo.HandlerFunc {
	return func(c echo.Context) error {
		updates, err := installer.Updates(c.Request().Context())
		if err != nil {
			return libraryError(c, http.StatusInternalServerError, err.Error())
		}
		return c.JSON(http.StatusOK, map[string][]backendlib.Update{"updates": updates})
	}
}

// libraryError answers in the error format of the other routes.
func libraryError(c echo.Context, status int, message string) error {
	return c.JSON(status, schema.ErrorResponse{Error: &schema.APIError{Code: status, Message: message, Type: "backend_library"}})
}
