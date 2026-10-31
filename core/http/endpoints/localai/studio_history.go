package localai

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/studiohistory"
)

// studioHistoryError maps store errors onto HTTP status codes, like
// chatHistoryError: a deleted entry is 410 Gone, so a browser holding a stale
// copy drops it instead of retrying.
func studioHistoryError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, studiohistory.ErrInvalidID), errors.Is(err, studiohistory.ErrInvalidUserID),
		errors.Is(err, studiohistory.ErrInvalidType), errors.Is(err, studiohistory.ErrInvalidEntry):
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, studiohistory.ErrNotFound):
		return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, studiohistory.ErrDeleted):
		return c.JSON(http.StatusGone, map[string]string{"error": err.Error()})
	case errors.Is(err, studiohistory.ErrConflict):
		return c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

// ListStudioHistoryEndpoint lists the current user's Studio entries and
// tombstones.
//
//	@Summary		List Studio history
//	@Description	Returns the live Studio entries, newest first, and in `deleted` the entries the user deleted. A client must drop any local copy whose id appears in `deleted` instead of uploading it again. `cacheKey` is an opaque per-account tag for the client's local cache.
//	@Tags			studio-history
//	@Produce		json
//	@Param			type	query		string	false	"Studio workspace (image, video, tts, sound, audio-transform, diarization, threed); all when omitted"
//	@Success		200		{object}	schema.StudioHistoryList
//	@Failure		400		{object}	map[string]string	"Unknown type"
//	@Router			/api/studio/history [get]
func ListStudioHistoryEndpoint(store *studiohistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		mediaType := c.QueryParam("type")
		entries, err := store.List(userID, mediaType)
		if err != nil {
			return studioHistoryError(c, err)
		}
		deleted, err := store.Tombstones(userID, mediaType)
		if err != nil {
			return studioHistoryError(c, err)
		}
		return c.JSON(http.StatusOK, schema.StudioHistoryList{
			Enabled:  true,
			Entries:  entries,
			Deleted:  deleted,
			CacheKey: studiohistory.CacheKey(userID),
		})
	}
}

// StudioHistoryDisabledEndpoint answers the list when this LocalAI keeps no
// Studio history (no user authentication): enabled is false and the list is
// empty, so the Studio keeps its history in the browser.
func StudioHistoryDisabledEndpoint() echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(http.StatusOK, schema.StudioHistoryList{
			Entries: []schema.StudioHistoryEntry{},
			Deleted: []schema.StudioHistoryTombstone{},
		})
	}
}

// GetStudioHistoryEntryEndpoint returns one Studio entry.
//
//	@Summary	Get a Studio history entry
//	@Tags		studio-history
//	@Produce	json
//	@Param		id	path		string	true	"Entry ID"
//	@Success	200	{object}	schema.StudioHistoryEntry
//	@Failure	404	{object}	map[string]string	"Not found"
//	@Failure	410	{object}	map[string]string	"Deleted"
//	@Router		/api/studio/history/{id} [get]
func GetStudioHistoryEntryEndpoint(store *studiohistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		entry, err := store.Get(getUserID(c), c.Param("id"))
		if err != nil {
			return studioHistoryError(c, err)
		}
		return c.JSON(http.StatusOK, entry)
	}
}

// SaveStudioHistoryEntryEndpoint creates or updates one Studio entry. Scripts
// that generate through the API can use it too, so their results show up in
// the Studio.
//
//	@Summary		Save a Studio history entry (upsert)
//	@Description	The path id overrides the body id. The returned version is assigned by the server. An entry the user deleted is never written again (410). The oldest entries beyond the per-workspace cap become tombstones.
//	@Tags			studio-history
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string						true	"Entry ID"
//	@Param			body	body		schema.StudioHistoryEntry	true	"Entry"
//	@Success		200		{object}	schema.StudioHistoryEntry
//	@Failure		400		{object}	map[string]string	"Invalid id, type or entry"
//	@Failure		410		{object}	map[string]string	"Deleted"
//	@Router			/api/studio/history/{id} [put]
func SaveStudioHistoryEntryEndpoint(store *studiohistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		var entry schema.StudioHistoryEntry
		if err := c.Bind(&entry); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		entry.ID = c.Param("id")
		saved, err := store.Save(getUserID(c), entry)
		if err != nil {
			return studioHistoryError(c, err)
		}
		return c.JSON(http.StatusOK, saved)
	}
}

// ImportStudioHistoryEndpoint stores the entries a browser kept before its
// history moved to the server. It never deletes anything.
//
//	@Summary		Import Studio history
//	@Description	Upserts every entry in the payload. Entries the user deleted earlier are not written and are listed in `skipped`. Nothing else is deleted.
//	@Tags			studio-history
//	@Accept			json
//	@Produce		json
//	@Param			body	body		schema.StudioHistoryImportRequest	true	"Entries"
//	@Success		200		{object}	schema.StudioHistoryImportResponse
//	@Failure		400		{object}	map[string]string	"Invalid id, type or entry"
//	@Router			/api/studio/history/import [post]
func ImportStudioHistoryEndpoint(store *studiohistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req schema.StudioHistoryImportRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		count, skipped, err := store.Import(getUserID(c), req.Entries)
		if err != nil {
			return studioHistoryError(c, err)
		}
		if skipped == nil {
			skipped = []string{}
		}
		return c.JSON(http.StatusOK, schema.StudioHistoryImportResponse{Status: "ok", Count: count, Skipped: skipped})
	}
}

// DeleteStudioHistoryEntryEndpoint deletes one Studio entry (a tombstone
// stays). Deleting is idempotent.
//
//	@Summary	Delete a Studio history entry
//	@Tags		studio-history
//	@Produce	json
//	@Param		id		path		string	true	"Entry ID"
//	@Param		type	query		string	false	"The entry's workspace, recorded when the server has not seen the entry yet"
//	@Success	200		{object}	map[string]string
//	@Failure	400		{object}	map[string]string	"Invalid id or type"
//	@Router		/api/studio/history/{id} [delete]
func DeleteStudioHistoryEntryEndpoint(store *studiohistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := store.Delete(getUserID(c), c.Param("id"), c.QueryParam("type")); err != nil {
			return studioHistoryError(c, err)
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

// DeleteAllStudioHistoryEndpoint clears one Studio workspace, or all of them
// when no type is given (tombstones stay).
//
//	@Summary	Clear Studio history
//	@Tags		studio-history
//	@Produce	json
//	@Param		type	query		string	false	"Studio workspace to clear; all when omitted"
//	@Success	200		{object}	map[string]string
//	@Failure	400		{object}	map[string]string	"Unknown type"
//	@Router		/api/studio/history [delete]
func DeleteAllStudioHistoryEndpoint(store *studiohistory.Store) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := store.DeleteAll(getUserID(c), c.QueryParam("type")); err != nil {
			return studioHistoryError(c, err)
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}
