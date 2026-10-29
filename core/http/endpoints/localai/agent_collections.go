package localai

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
)

// ListCollectionsEndpoint handles /api/collections (GET).
// @Summary List your collections.
// @Description The knowledge-base collections of the caller. An administrator can add all_users=true to also get the other users' collections in user_groups. Needs the collections feature.
// @Tags collections
// @Success 200 {object} schema.CollectionList "The collections"
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

// CreateCollectionEndpoint handles /api/collections (POST).
// @Summary Create a collection.
// @Description Makes an empty collection under the name in the body.
// @Tags collections
// @Param request body schema.CollectionCreateRequest true "The name of the collection"
// @Success 201 {object} schema.CollectionStatus "Created"
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

// UploadToCollectionEndpoint handles /api/collections/{name}/upload (POST).
// @Summary Add a file to a collection.
// @Description Uploads a file as multipart form field `file`. The server cuts it into passages and embeds them with the collections embedding model. 409 when the embedding model changed and the vectors have another size.
// @Tags collections
// @Param name path string true "Collection name"
// @Param file formData file true "The file to add"
// @Success 200 {object} schema.CollectionStatus "The entry key"
// @Failure 404 {object} map[string]string "Collection not found"
// @Failure 409 {object} map[string]string "The embedding model changed"
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
		key, err := svc.UploadForUser(c.Request().Context(), userID, name, file.Filename, src)
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

// ListCollectionEntriesEndpoint handles /api/collections/{name}/entries (GET).
// @Summary List the entries of a collection.
// @Tags collections
// @Param name path string true "Collection name"
// @Success 200 {object} schema.CollectionEntries "The entries"
// @Failure 404 {object} map[string]string "Collection not found"
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

// GetCollectionEntryContentEndpoint handles /api/collections/{name}/entries/{entry} (GET).
// @Summary Read the text of an entry.
// @Tags collections
// @Param name path string true "Collection name"
// @Param entry path string true "Entry key"
// @Success 200 {object} schema.CollectionEntryContent "The text and its number of passages"
// @Failure 404 {object} map[string]string "Collection not found"
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

// SearchCollectionEndpoint handles /api/collections/{name}/search (POST).
// @Summary Search a collection.
// @Description Returns the passages that match the query best. Asking for more passages than the collection holds returns all of them; an empty collection returns none.
// @Tags collections
// @Param name path string true "Collection name"
// @Param request body schema.CollectionSearchRequest true "The query"
// @Success 200 {object} schema.CollectionSearchResponse "The passages"
// @Failure 404 {object} map[string]string "Collection not found"
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
		results, err := svc.SearchForUser(c.Request().Context(), userID, decodedParam(c, "name"), payload.Query, payload.MaxResults)
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

// ResetCollectionEndpoint handles /api/collections/{name}/reset (POST).
// @Summary Empty a collection.
// @Description Deletes every entry and keeps the collection and the sources it follows.
// @Tags collections
// @Param name path string true "Collection name"
// @Success 200 {object} schema.CollectionStatus "Done"
// @Failure 404 {object} map[string]string "Collection not found"
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

// DeleteCollectionEntryEndpoint handles /api/collections/{name}/entry/delete (DELETE).
// @Summary Delete one entry of a collection.
// @Tags collections
// @Param name path string true "Collection name"
// @Param request body schema.CollectionEntryDeleteRequest true "The entry"
// @Success 200 {object} schema.CollectionEntryDeleteResponse "The entries left"
// @Failure 404 {object} map[string]string "Collection not found"
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

// AddCollectionSourceEndpoint handles /api/collections/{name}/sources (POST).
// @Summary Make a collection follow a URL.
// @Description The server fetches the URL again at the given interval, in minutes; a value below 1 means 60.
// @Tags collections
// @Param name path string true "Collection name"
// @Param request body schema.CollectionSourceRequest true "The URL and the interval in minutes"
// @Success 200 {object} schema.CollectionStatus "Done"
// @Failure 404 {object} map[string]string "Collection not found"
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

// RemoveCollectionSourceEndpoint handles /api/collections/{name}/sources (DELETE).
// @Summary Stop a collection following a URL.
// @Tags collections
// @Param name path string true "Collection name"
// @Param request body schema.CollectionSourceRequest true "The URL"
// @Success 200 {object} schema.CollectionStatus "Done"
// @Failure 404 {object} map[string]string "Collection not found"
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
// @Summary Download the file of an entry.
// @Description Serves the file as it was uploaded.
// @Tags collections
// @Param name path string true "Collection name"
// @Param entry path string true "Entry key"
// @Success 200 {file} binary "The original file"
// @Failure 404 {object} map[string]string "Collection not found"
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

// ListCollectionSourcesEndpoint handles /api/collections/{name}/sources (GET).
// @Summary List the URLs a collection follows.
// @Tags collections
// @Param name path string true "Collection name"
// @Success 200 {object} schema.CollectionSources "The sources"
// @Failure 404 {object} map[string]string "Collection not found"
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
