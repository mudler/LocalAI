package localai

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/library"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/xlog"
)

// LibrarySourcesEndpoints serve the library sources of all three kinds
// (add-ons, backends and models). They work on the store `local-ai library`
// edits, through the same functions, so the command and the routes cannot
// drift. The routes are registered behind the administrator middleware.
type LibrarySourcesEndpoints struct {
	// Store returns the one store of the running server.
	Store func() (*catalog.Sources, error)
	// Official says whether the official libraries are on.
	Official func() bool
	// Applied runs after a change, so the server reads the new state.
	Applied func(dropped ...string) error
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
}

func librarySourceError(c echo.Context, code int, message string) error {
	return c.JSON(code, map[string]any{"error": map[string]any{"code": code, "message": message}})
}

func (e LibrarySourcesEndpoints) open(c echo.Context) (*catalog.Sources, error) {
	store, err := e.Store()
	if err != nil {
		xlog.Warn("library sources are not available", "error", err)
		return nil, librarySourceError(c, http.StatusServiceUnavailable, "the library sources are not available")
	}
	return store, nil
}

func (e LibrarySourcesEndpoints) applied(dropped ...string) {
	if e.Applied == nil {
		return
	}
	if err := e.Applied(dropped...); err != nil {
		xlog.Warn("could not apply the library sources", "error", err)
	}
}

func (e LibrarySourcesEndpoints) official() bool {
	if e.Official == nil {
		return true
	}
	return e.Official()
}

// storeError maps a store error to the status the caller sees.
func storeError(c echo.Context, err error, id string) error {
	switch {
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
		store, err := e.open(c)
		if store == nil {
			return err
		}
		return c.JSON(http.StatusOK, LibrarySourcesResponse{Sources: library.List(store), OfficialLibraries: e.official()})
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
		req.URL, req.ID, req.Name = strings.TrimSpace(req.URL), strings.TrimSpace(req.ID), strings.TrimSpace(req.Name)
		switch req.Kind {
		case string(catalog.KindAddon), string(catalog.KindBackend), string(catalog.KindModel):
		default:
			return librarySourceError(c, http.StatusBadRequest, "kind must be addon, backend or model")
		}
		format := ""
		switch req.Format {
		case "", "library":
		case "gallery":
			format = catalog.FormatGallery
		default:
			return librarySourceError(c, http.StatusBadRequest, "format must be library or gallery")
		}
		if req.URL == "" {
			return librarySourceError(c, http.StatusBadRequest, "url is required")
		}
		store, err := e.open(c)
		if store == nil {
			return err
		}
		src, err := library.Add(store, catalog.Kind(req.Kind), req.URL, req.ID, req.Name, format)
		if err != nil {
			return storeError(c, err, req.ID)
		}
		e.applied()
		for _, in := range library.List(store) {
			if in.ID == src.ID {
				return c.JSON(http.StatusCreated, in)
			}
		}
		return c.JSON(http.StatusCreated, library.Info{Kind: src.Kind, ID: src.ID, Name: src.Name, URL: src.URL, Enabled: true})
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
		store, err := e.open(c)
		if store == nil {
			return err
		}
		if err := store.SetEnabled(id, enabled); err != nil {
			return storeError(c, err, id)
		}
		e.applied()
		for _, in := range library.List(store) {
			if in.ID == id {
				return c.JSON(http.StatusOK, in)
			}
		}
		return c.NoContent(http.StatusNoContent)
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
		store, err := e.open(c)
		if store == nil {
			return err
		}
		url := library.URLOf(store, id)
		wasDefault, err := library.Remove(store, id)
		if err != nil {
			return storeError(c, err, id)
		}
		// A tombstone keeps its record, so only a deleted source is dropped by address.
		if wasDefault {
			e.applied()
		} else {
			e.applied(url)
		}
		return c.JSON(http.StatusOK, map[string]any{"id": id, "default": wasDefault, "tombstone": wasDefault})
	}
}
