package localai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/middleware"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/galleryop"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/xlog"
)

// BackendLibraryInstaller installs a backend from a library entry and lists
// the installed ones that have a newer build in their library.
type BackendLibraryInstaller interface {
	Install(ctx context.Context, sourceID, backend, version string, progress backendlib.Progress) error
	Updates(ctx context.Context) ([]backendlib.Update, error)
}

// BackendLibraryJobs records the status of an install under a job ID. The
// gallery service is the one the backend gallery installs use, so the same
// route, GET /backends/jobs/{id}, answers for both.
type BackendLibraryJobs interface {
	UpdateStatus(id string, op *galleryop.OpStatus)
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

// libraryInstallTimeout bounds one install that runs after its request ended.
const libraryInstallTimeout = time.Hour

// InstallBackendFromLibraryEndpoint starts the install of a backend from a
// library entry on this node and answers at once with a job. The job reports
// the steps of the install (resolving, verifying, size, fetching, writing) as
// the phase of its status, with the byte progress of the fetch, and ends with
// the result or the reason of the refusal. The library resolves the entry, so
// the request cannot name an image, a digest or a policy.
// @Summary Install a backend from a library entry on this node.
// @Tags backends
// @Param request body BackendLibraryInstallRequest true "library, backend name and version"
// @Success 200 {object} schema.BackendResponse "the job; read its status at the status URL"
// @Failure 400 {object} schema.ErrorResponse "the request does not name an entry"
// @Failure 409 {object} schema.ErrorResponse "the same backend is already being installed"
// @Router /backends/library/install [post]
func InstallBackendFromLibraryEndpoint(installer BackendLibraryInstaller, jobs BackendLibraryJobs) echo.HandlerFunc {
	var mu sync.Mutex
	running := map[string]bool{}
	return func(c echo.Context) error {
		var in BackendLibraryInstallRequest
		if err := c.Bind(&in); err != nil {
			return libraryError(c, http.StatusBadRequest, "invalid request")
		}
		if in.SourceID == "" || in.Name == "" || in.Version == "" {
			return libraryError(c, http.StatusBadRequest, "sourceId, name and version are required")
		}
		// One install of a name at a time: two would write the same directory.
		mu.Lock()
		if running[in.Name] {
			mu.Unlock()
			return libraryError(c, http.StatusConflict, "this backend is already being installed")
		}
		running[in.Name] = true
		mu.Unlock()

		id := uuid.NewString()
		jobs.UpdateStatus(id, &galleryop.OpStatus{Phase: backendlib.StepResolving, Message: stepMessage(backendlib.StepResolving), GalleryElementName: in.Name})
		// The request ends before the install does, so the install keeps the
		// values of the request and drops its cancellation.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request().Context()), libraryInstallTimeout)
		go func() {
			defer cancel()
			defer func() {
				mu.Lock()
				delete(running, in.Name)
				mu.Unlock()
			}()
			runLibraryInstall(ctx, installer, jobs, id, in)
		}()
		return c.JSON(http.StatusOK, schema.BackendResponse{ID: id, StatusURL: fmt.Sprintf("%sbackends/jobs/%s", middleware.BaseURL(c), id)})
	}
}

// stepMessage is the text of a step in the status of a job.
func stepMessage(step string) string {
	switch step {
	case backendlib.StepResolving:
		return "Resolving the library entry"
	case backendlib.StepVerifying:
		return "Verifying the signature"
	case backendlib.StepSize:
		return "Checking the image size"
	case backendlib.StepFetching:
		return "Fetching the image"
	case backendlib.StepWriting:
		return "Writing the metadata"
	}
	return step
}

func runLibraryInstall(ctx context.Context, installer BackendLibraryInstaller, jobs BackendLibraryJobs, id string, in BackendLibraryInstallRequest) {
	var mu sync.Mutex
	phase := backendlib.StepResolving
	post := func(op *galleryop.OpStatus) {
		op.GalleryElementName = in.Name
		jobs.UpdateStatus(id, op)
	}
	err := installer.Install(ctx, in.SourceID, in.Name, in.Version, backendlib.Progress{
		Step: func(step string) {
			mu.Lock()
			phase = step
			mu.Unlock()
			post(&galleryop.OpStatus{Phase: step, Message: stepMessage(step)})
		},
		Bytes: func(file, current, total string, pct float64) {
			mu.Lock()
			p := phase
			mu.Unlock()
			// Bytes arrive while the image is fetched; a late tick must not
			// move the job back from a step it has left.
			if p != backendlib.StepFetching {
				return
			}
			post(&galleryop.OpStatus{Phase: p, Message: stepMessage(p), FileName: file, Progress: pct, DownloadedFileSize: current, TotalFileSize: total})
		},
	})
	if err == nil {
		post(&galleryop.OpStatus{Phase: "done", Message: "completed", Processed: true, Progress: 100})
		return
	}
	xlog.Warn("backend library install failed", "source", in.SourceID, "backend", in.Name, "error", err)
	mu.Lock()
	failed := phase
	mu.Unlock()
	// The phase stays at the step that failed, so the page can mark it.
	post(&galleryop.OpStatus{Phase: failed, Error: err, Processed: true, Message: "error: " + libraryReason(err)})
}

// libraryReason words the failure of an install for the administrator.
func libraryReason(err error) string {
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		return "the library has no such entry for this host"
	case errors.Is(err, backendlib.ErrCollision):
		return err.Error()
	case errors.Is(err, catalog.ErrUnavailable), errors.Is(err, catalog.ErrRefresh):
		return "the library is not available: " + err.Error()
	case errors.Is(err, downloader.ErrImageTooLarge):
		return "the image is over the size limit: " + err.Error()
	}
	return err.Error()
}

// BackendLibraryUpdatesEndpoint lists the installed library backends that have
// a newer build in their library. It reads the cached index and installs
// nothing.
// @Summary List the installed library backends with a newer build in their library.
// @Tags backends
// @Success 200 {object} map[string][]backendlib.Update "updates"
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
