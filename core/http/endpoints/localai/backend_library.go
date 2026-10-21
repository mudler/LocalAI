package localai

import (
	"context"
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/addons/backendlib"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/pkg/downloader"
	"github.com/mudler/xlog"
)

// BackendLibraryInstaller installs a backend from a library entry.
type BackendLibraryInstaller interface {
	Install(ctx context.Context, sourceID, backend, version string, status func(string, string, string, float64)) error
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

// InstallBackendFromLibraryEndpoint installs a backend from a library entry
// on this node. The install runs inside the request and answers when it is
// done. The library resolves the entry, so the request cannot name an image,
// a digest or a policy.
// @Summary Install a backend from a library entry on this node.
// @Tags backends
// @Param request body BackendLibraryInstallRequest true "library, backend name and version"
// @Success 200 {object} map[string]string "installed"
// @Failure 404 {object} schema.ErrorResponse "no such entry"
// @Failure 409 {object} schema.ErrorResponse "the name is installed from another origin"
// @Failure 413 {object} schema.ErrorResponse "the image is over the size limit"
// @Router /backends/library/install [post]
func InstallBackendFromLibraryEndpoint(installer BackendLibraryInstaller) echo.HandlerFunc {
	return func(c echo.Context) error {
		var in BackendLibraryInstallRequest
		if err := c.Bind(&in); err != nil {
			return libraryError(c, http.StatusBadRequest, "invalid request")
		}
		if in.SourceID == "" || in.Name == "" || in.Version == "" {
			return libraryError(c, http.StatusBadRequest, "sourceId, name and version are required")
		}
		err := installer.Install(c.Request().Context(), in.SourceID, in.Name, in.Version, nil)
		if err == nil {
			return c.JSON(http.StatusOK, map[string]string{"status": "installed", "name": in.Name, "sourceId": in.SourceID})
		}
		xlog.Warn("backend library install failed", "source", in.SourceID, "backend", in.Name, "error", err)
		status := http.StatusInternalServerError
		switch {
		case errors.Is(err, catalog.ErrNotFound):
			status = http.StatusNotFound
		case errors.Is(err, backendlib.ErrCollision):
			status = http.StatusConflict
		case errors.Is(err, backendlib.ErrIntegrity), errors.Is(err, backendlib.ErrInvalid), errors.Is(err, catalog.ErrInvalid):
			status = http.StatusBadRequest
		case errors.Is(err, catalog.ErrUnavailable), errors.Is(err, catalog.ErrRefresh):
			status = http.StatusServiceUnavailable
		case errors.Is(err, downloader.ErrImageTooLarge):
			status = http.StatusRequestEntityTooLarge
		}
		return libraryError(c, status, err.Error())
	}
}

// libraryError answers in the error format of the other routes.
func libraryError(c echo.Context, status int, message string) error {
	return c.JSON(status, schema.ErrorResponse{Error: &schema.APIError{Code: status, Message: message, Type: "backend_library"}})
}
