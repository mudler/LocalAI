package localai

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/library"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/xlog"
)

// LibrarySourcesEndpoints serve the library sources of all three kinds
// (add-ons, backends and models). They work on the store `local-ai library`
// edits, through the same functions, so the command and the routes cannot
// drift. The routes are registered behind the administrator middleware.
type LibrarySourcesEndpoints struct {
	// Admin works on the library sources: the store of this process, or the
	// add-on owner's store through the carrier on a frontend. The routes
	// answer the same either way.
	Admin library.Admin
}

type LibrarySourcesResponse struct {
	Sources           []library.Info `json:"sources"`
	OfficialLibraries bool           `json:"officialLibraries"`
}

type addLibrarySourceRequest struct {
	Kind   string `json:"kind"`
	URL    string `json:"url"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Format string `json:"format"`
	// Mirrors are fallback addresses of a model or backend source.
	Mirrors []string `json:"mirrors,omitempty"`
}

type updateLibrarySourceRequest struct {
	Mirrors []string `json:"mirrors"`
}

func librarySourceError(c echo.Context, code int, message string) error {
	return c.JSON(code, map[string]any{"error": map[string]any{"code": code, "message": message}})
}

// storeError maps an error of the library administration to the status the
// caller sees.
func storeError(c echo.Context, err error, id string) error {
	var bad *library.BadRequestError
	switch {
	case errors.As(err, &bad):
		return librarySourceError(c, http.StatusBadRequest, bad.Message)
	case errors.Is(err, library.ErrStoreUnavailable):
		return librarySourceError(c, http.StatusServiceUnavailable, "the library sources are not available")
	case errors.Is(err, gateway.ErrForbidden):
		return librarySourceError(c, http.StatusForbidden, "this account may not change the library sources")
	case errors.Is(err, gateway.ErrUnavailable):
		// The add-on owner cannot serve the call. The body says why.
		gateway.WriteUnavailable(c.Response(), gateway.UnavailableReason(err))
		return nil
	case errors.Is(err, catalog.ErrNotFound):
		return librarySourceError(c, http.StatusNotFound, "no source with id "+id)
	case errors.Is(err, catalog.ErrExists), errors.Is(err, library.ErrDuplicate):
		return librarySourceError(c, http.StatusConflict, err.Error())
	case errors.Is(err, catalog.ErrInvalid):
		return librarySourceError(c, http.StatusBadRequest, "the id must be lowercase letters, digits and dashes and start with a letter, and a library index URL must be http or https (use format gallery for a gallery index)")
	}
	xlog.Warn("library source change failed", "error", err)
	return librarySourceError(c, http.StatusInternalServerError, "the change could not be stored")
}

// List godoc
// @Summary List the library sources
// @Description Every library source of the add-on, backend and model kinds, with its state: kind, id, name, url, enabled (what the host does with it), default (shipped), removed (a default the user removed; a tombstone), suspended (a default while the official libraries are off), the conflict message when it clashes with another source, and the last fetch state when there is one. Administrators only.
// @Tags library
// @Produce json
// @Success 200 {object} localai.LibrarySourcesResponse
// @Router /api/library/sources [get]
func (e LibrarySourcesEndpoints) List() echo.HandlerFunc {
	return func(c echo.Context) error {
		l, err := e.Admin.List(c.Request().Context(), "")
		if err != nil {
			return storeError(c, err, "")
		}
		return c.JSON(http.StatusOK, LibrarySourcesResponse{Sources: l.Sources, OfficialLibraries: l.OfficialLibraries})
	}
}

// Update godoc
// @Summary Change the mirrors of a library source
// @Description Replaces the mirrors of a model or backend source; an empty list clears them. Each mirror is an http(s), github:, oci:// or file:// address. Administrators only.
// @Tags library
// @Accept json
// @Produce json
// @Param id path string true "Source id"
// @Success 200 {object} library.Info
// @Router /api/library/sources/{id} [put]
func (e LibrarySourcesEndpoints) Update() echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		var req updateLibrarySourceRequest
		if err := c.Bind(&req); err != nil {
			return librarySourceError(c, http.StatusBadRequest, "the request is not valid JSON")
		}
		in, err := e.Admin.SetMirrors(c.Request().Context(), id, req.Mirrors)
		if err != nil {
			return storeError(c, err, id)
		}
		if in == nil {
			return c.NoContent(http.StatusNoContent)
		}
		return c.JSON(http.StatusOK, in)
	}
}

// Add godoc
// @Summary Add a library source
// @Description Adds a source of your own. kind is addon, backend or model; format is library (default) or gallery. The id is derived from the name or URL when it is empty. A source with the same kind and URL is refused (409), as is a taken id. Administrators only.
// @Tags library
// @Accept json
// @Produce json
// @Success 201 {object} library.Info
// @Router /api/library/sources [post]
func (e LibrarySourcesEndpoints) Add() echo.HandlerFunc {
	return func(c echo.Context) error {
		var req addLibrarySourceRequest
		if err := c.Bind(&req); err != nil {
			return librarySourceError(c, http.StatusBadRequest, "the request is not valid JSON")
		}
		in, err := e.Admin.Add(c.Request().Context(), library.AddRequest{Kind: req.Kind, URL: req.URL, ID: req.ID, Name: req.Name, Format: req.Format, Mirrors: req.Mirrors})
		if err != nil {
			return storeError(c, err, req.ID)
		}
		return c.JSON(http.StatusCreated, in)
	}
}

// SetEnabled godoc
// @Summary Turn a library source on or off
// @Description Enabling a removed default brings it back. Administrators only.
// @Tags library
// @Produce json
// @Param id path string true "Source id"
// @Success 200 {object} library.Info
// @Router /api/library/sources/{id}/enable [post]
// @Router /api/library/sources/{id}/disable [post]
func (e LibrarySourcesEndpoints) SetEnabled(enabled bool) echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		in, err := e.Admin.SetEnabled(c.Request().Context(), id, enabled)
		if err != nil {
			return storeError(c, err, id)
		}
		if in == nil {
			return c.NoContent(http.StatusNoContent)
		}
		return c.JSON(http.StatusOK, in)
	}
}

// Remove godoc
// @Summary Remove a library source
// @Description A source of your own is deleted. A shipped default is not: it stays as a tombstone (removed true), so it does not come back at the next start; enabling it brings it back. Administrators only.
// @Tags library
// @Produce json
// @Param id path string true "Source id"
// @Success 200 {object} map[string]any
// @Router /api/library/sources/{id} [delete]
func (e LibrarySourcesEndpoints) Remove() echo.HandlerFunc {
	return func(c echo.Context) error {
		id := c.Param("id")
		res, err := e.Admin.Remove(c.Request().Context(), id)
		if err != nil {
			return storeError(c, err, id)
		}
		return c.JSON(http.StatusOK, map[string]any{"id": res.ID, "default": res.Default, "tombstone": res.Tombstone})
	}
}
