package localai

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
)

// ListCollectionsEndpoint handles the collections route "List collections"
// @Summary List collections.
// @Description With authentication on, an administrator can add all_users=true to also get the other users' collections in user_groups. Needs the collections feature.
// @Tags collections
// @Param all_users query string false "Set to 'true' for the administrator's cross-user listing"
// @Success 200 {object} map[string]any "collections and count"
// @Router /api/collections [get]
func ListCollectionsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.CollectionsService()
		userID := getUserID(c)
		cols, err := svc.ListForUser(userID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		resp := map[string]any{
			"collections": cols,
			"count":       len(cols),
		}

		// Admin cross-user aggregation
		if wantsAllUsers(c) {
			{
				userIDs, _ := svc.UserIDs()
				userGroups := map[string]any{}
				for _, uid := range userIDs {
					if uid == userID {
						continue
					}
					userCols, err := svc.ListForUser(uid)
					if err != nil || len(userCols) == 0 {
						continue
					}
					userGroups[uid] = map[string]any{"collections": userCols}
				}
				if len(userGroups) > 0 {
					resp["user_groups"] = userGroups
				}
			}
		}

		return c.JSON(http.StatusOK, resp)
	}
}

// CreateCollectionEndpoint handles the collections route "Create a collection"
// @Summary Create a collection.
// @Tags collections
// @Accept json
// @Param request body object true "{\"name\": \"docs\"}"
// @Success 201 {object} map[string]string "status and name"
// @Failure 400 {object} map[string]string "error"
// @Router /api/collections [post]
func CreateCollectionEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.CollectionsService()
		userID := getUserID(c)
		var payload struct {
			Name string `json:"name"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := svc.CreateForUser(userID, payload.Name); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, map[string]string{"status": "ok", "name": payload.Name})
	}
}

// UploadToCollectionEndpoint handles the collections route "Upload a file to a collection"
// @Summary Upload a file to a collection.
// @Description Multipart form with the field file. 409 when the embedding model changed and the vectors have another size.
// @Tags collections
// @Accept mpfd
// @Param name path string true "Collection name (URL-encoded)"
// @Param file formData file true "The file"
// @Success 200 {object} map[string]string "status, filename and key"
// @Failure 404 {object} map[string]string "error"
// @Failure 409 {object} map[string]string "error"
// @Router /api/collections/{name}/upload [post]
func UploadToCollectionEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.CollectionsService()
		userID := effectiveUserID(c)
		name := decodedParam(c, "name")
		file, err := c.FormFile("file")
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "file required"})
		}
		src, err := file.Open()
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		defer src.Close()
		key, err := svc.UploadForUser(userID, name, file.Filename, src)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			if isEmbeddingDimensionMismatch(err) {
				return c.JSON(http.StatusConflict, map[string]string{
					"error": "embedding model dimensionality changed and the collection could not be migrated automatically; restart LocalAI to trigger re-embedding, or reset the collection",
				})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok", "filename": file.Filename, "key": key})
	}
}

// isEmbeddingDimensionMismatch detects the pgvector-side error that bubbles up
// from LocalRecall when the configured embedding model returns vectors of a
// different dimensionality than the collection's vector column. LocalRecall
// migrates the column on startup; this guard only fires for edge cases the
// migration path doesn't cover (e.g. a model swapped at runtime), so we
// surface a 409 with an actionable message instead of an opaque 500.
func isEmbeddingDimensionMismatch(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLSTATE 22000") &&
		strings.Contains(msg, "expected ") &&
		strings.Contains(msg, " dimensions, not ")
}

// ListCollectionEntriesEndpoint handles the collections route "List the entries of a collection"
// @Summary List the entries of a collection.
// @Tags collections
// @Param name path string true "Collection name (URL-encoded)"
// @Success 200 {object} map[string]any "entries and count"
// @Failure 404 {object} map[string]string "error"
// @Router /api/collections/{name}/entries [get]
func ListCollectionEntriesEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.CollectionsService()
		userID := effectiveUserID(c)
		entries, err := svc.ListEntriesForUser(userID, decodedParam(c, "name"))
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"entries": entries,
			"count":   len(entries),
		})
	}
}

// GetCollectionEntryContentEndpoint handles the collections route "Read the text of a collection entry"
// @Summary Read the text of a collection entry.
// @Tags collections
// @Param name path string true "Collection name (URL-encoded)"
// @Param entry path string true "Entry name (URL-encoded)"
// @Success 200 {object} map[string]any "content and chunk_count"
// @Failure 404 {object} map[string]string "error"
// @Router /api/collections/{name}/entries/{entry} [get]
func GetCollectionEntryContentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.CollectionsService()
		userID := effectiveUserID(c)
		entryParam := c.Param("*")
		entry, err := url.PathUnescape(entryParam)
		if err != nil {
			entry = entryParam
		}
		content, chunkCount, err := svc.EntryContentForUser(userID, decodedParam(c, "name"), entry)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"content":     content,
			"chunk_count": chunkCount,
		})
	}
}

// SearchCollectionEndpoint handles the collections route "Search a collection"
// @Summary Search a collection.
// @Tags collections
// @Accept json
// @Param name path string true "Collection name (URL-encoded)"
// @Param request body object true "{\"query\": \"...\", \"max_results\": 5}"
// @Success 200 {object} map[string]any "results and count"
// @Failure 404 {object} map[string]string "error"
// @Router /api/collections/{name}/search [post]
func SearchCollectionEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.CollectionsService()
		userID := effectiveUserID(c)
		var payload struct {
			Query      string `json:"query"`
			MaxResults int    `json:"max_results"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		results, err := svc.SearchForUser(userID, decodedParam(c, "name"), payload.Query, payload.MaxResults)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"results": results,
			"count":   len(results),
		})
	}
}

// ResetCollectionEndpoint handles the collections route "Delete every entry of a collection"
// @Summary Delete every entry of a collection.
// @Tags collections
// @Param name path string true "Collection name (URL-encoded)"
// @Success 200 {object} map[string]string "status"
// @Failure 404 {object} map[string]string "error"
// @Router /api/collections/{name}/reset [post]
func ResetCollectionEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.CollectionsService()
		userID := effectiveUserID(c)
		if err := svc.ResetForUser(userID, decodedParam(c, "name")); err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

// DeleteCollectionEntryEndpoint handles the collections route "Delete an entry of a collection"
// @Summary Delete an entry of a collection.
// @Tags collections
// @Accept json
// @Param name path string true "Collection name (URL-encoded)"
// @Param request body object true "{\"entry\": \"file.pdf\"}"
// @Success 200 {object} map[string]any "remaining_entries and count"
// @Failure 404 {object} map[string]string "error"
// @Router /api/collections/{name}/entry/delete [delete]
func DeleteCollectionEntryEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.CollectionsService()
		userID := effectiveUserID(c)
		var payload struct {
			Entry string `json:"entry"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		remaining, err := svc.DeleteEntryForUser(userID, decodedParam(c, "name"), payload.Entry)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"remaining_entries": remaining,
			"count":             len(remaining),
		})
	}
}

// AddCollectionSourceEndpoint handles the collections route "Follow a URL as a source of a collection"
// @Summary Follow a URL as a source of a collection.
// @Description update_interval is in minutes; a value below 1 means 60.
// @Tags collections
// @Accept json
// @Param name path string true "Collection name (URL-encoded)"
// @Param request body object true "{\"url\": \"https://...\", \"update_interval\": 60}"
// @Success 200 {object} map[string]string "status"
// @Failure 404 {object} map[string]string "error"
// @Router /api/collections/{name}/sources [post]
func AddCollectionSourceEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.CollectionsService()
		userID := effectiveUserID(c)
		var payload struct {
			URL            string `json:"url"`
			UpdateInterval int    `json:"update_interval"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if payload.UpdateInterval < 1 {
			payload.UpdateInterval = 60
		}
		if err := svc.AddSourceForUser(userID, decodedParam(c, "name"), payload.URL, payload.UpdateInterval); err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

// RemoveCollectionSourceEndpoint handles the collections route "Stop following a source of a collection"
// @Summary Stop following a source of a collection.
// @Tags collections
// @Accept json
// @Param name path string true "Collection name (URL-encoded)"
// @Param request body object true "{\"url\": \"https://...\"}"
// @Success 200 {object} map[string]string "status"
// @Router /api/collections/{name}/sources [delete]
func RemoveCollectionSourceEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.CollectionsService()
		userID := effectiveUserID(c)
		var payload struct {
			URL string `json:"url"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := svc.RemoveSourceForUser(userID, decodedParam(c, "name"), payload.URL); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

// GetCollectionEntryRawFileEndpoint serves the original uploaded binary file.
// GetCollectionEntryRawFileEndpoint handles the collections route "Download a collection entry as uploaded"
// @Summary Download a collection entry as uploaded.
// @Tags collections
// @Produce octet-stream
// @Param name path string true "Collection name (URL-encoded)"
// @Param entry path string true "Entry name (URL-encoded)"
// @Success 200 {file} file "The file"
// @Failure 404 {object} map[string]string "error"
// @Router /api/collections/{name}/entries-raw/{entry} [get]
func GetCollectionEntryRawFileEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.CollectionsService()
		userID := effectiveUserID(c)
		entryParam := c.Param("*")
		entry, err := url.PathUnescape(entryParam)
		if err != nil {
			entry = entryParam
		}
		fpath, err := svc.EntryFilePathForUser(userID, decodedParam(c, "name"), entry)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.File(fpath)
	}
}

// ListCollectionSourcesEndpoint handles the collections route "List the sources of a collection"
// @Summary List the sources of a collection.
// @Tags collections
// @Param name path string true "Collection name (URL-encoded)"
// @Success 200 {object} map[string]any "sources and count"
// @Failure 404 {object} map[string]string "error"
// @Router /api/collections/{name}/sources [get]
func ListCollectionSourcesEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.CollectionsService()
		userID := effectiveUserID(c)
		sources, err := svc.ListSourcesForUser(userID, decodedParam(c, "name"))
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"sources": sources,
			"count":   len(sources),
		})
	}
}
