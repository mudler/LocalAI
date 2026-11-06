package localai

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	lagi "github.com/mudler/LocalAI/core/services/collections/backend"
)

// ListCollectionsEndpoint handles /api/collections (GET).
// @Summary List your collections.
// @Description The knowledge-base collections of the caller. shared lists the collections other people shared with the caller: name is the name to use in the other calls (the collection's own name, or shared:<id> when the caller has a collection of that name or two people share one under it), title its own name, owner the display name of the person who shared it and role viewer (search and read) or editor (also add and remove entries). shares gives, for each of the caller's own collections that was made shareable, its resource_id in /api/shares/collection and shared_with, the number of grants. sharing is false when this server cannot share collections (no named accounts). An administrator can add all_users=true to also get the other users' collections in user_groups. embedding_model names the model that indexes and searches the collections (collections_embedding_model), and embedding_model_installed says whether this server has it: without it, adding files and searching fail. search_mode says how a search finds passages: semantic (by meaning only) or hybrid (by meaning and by keyword). max_chunk_size and chunk_overlap are the chunking of a text in characters: a text of N characters becomes about N / (max_chunk_size - chunk_overlap) passages, and each passage is embedded once. Needs the collections feature.
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

		model := app.ApplicationConfig().Collections.EmbeddingModel
		ctx := c.Request().Context()
		shared := []schema.SharedCollection{}
		for _, s := range app.SharedCollections(ctx, userID, cols) {
			shared = append(shared, schema.SharedCollection{Name: s.Address, Title: s.Name, Owner: s.OwnerName, Role: s.Role, Everyone: s.Everyone})
		}
		shares := map[string]schema.CollectionShare{}
		for name, s := range app.CollectionShares(ctx, userID, cols) {
			shares[name] = schema.CollectionShare{ResourceID: s.ID, SharedWith: s.SharedWith}
		}
		resp := map[string]any{
			"collections":               cols,
			"count":                     len(cols),
			"shared":                    shared,
			"shares":                    shares,
			"sharing":                   userID != "" && app.CollectionSharing(),
			"embedding_model":           model,
			"embedding_model_installed": embeddingModelInstalled(app, model),
			"search_mode":               collectionSearchMode(app.ApplicationConfig().Collections.VectorEngine),
			"max_chunk_size":            app.ApplicationConfig().Collections.MaxChunkingSize,
			"chunk_overlap":             app.ApplicationConfig().Collections.ChunkOverlap,
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

// collectionSearchMode says how a search of the vector engine finds passages.
// The chromem and localai engines compare embeddings only. The postgres engine
// also matches words (BM25) and mixes the two lists.
func collectionSearchMode(engine string) string {
	if engine == "postgres" {
		return "hybrid"
	}
	return "semantic"
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
		var key string
		err = app.WithCollection(c.Request().Context(), userID, name, policy.ActionWrite, func(ctx context.Context, owner, coll string) error {
			var err error
			key, err = svc.UploadForUser(ctx, owner, coll, file.Filename, src)
			return err
		})
		if err != nil {
			if handled, herr := collectionDenied(c, err); handled {
				return herr
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
		var entries []string
		err := app.WithCollection(c.Request().Context(), userID, decodedParam(c, "name"), policy.ActionRead, func(_ context.Context, owner, coll string) error {
			var err error
			entries, err = svc.ListEntriesForUser(owner, coll)
			return err
		})
		if err != nil {
			if handled, herr := collectionDenied(c, err); handled {
				return herr
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
		var content string
		var chunkCount int
		err = app.WithCollection(c.Request().Context(), userID, decodedParam(c, "name"), policy.ActionRead, func(_ context.Context, owner, coll string) error {
			var err error
			content, chunkCount, err = svc.EntryContentForUser(owner, coll, entry)
			return err
		})
		if err != nil {
			if handled, herr := collectionDenied(c, err); handled {
				return herr
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
		var results []lagi.SearchResult
		err := app.WithCollection(c.Request().Context(), userID, decodedParam(c, "name"), policy.ActionRead, func(ctx context.Context, owner, coll string) error {
			var err error
			results, err = svc.SearchForUser(ctx, owner, coll, payload.Query, payload.MaxResults)
			return err
		})
		if err != nil {
			if handled, herr := collectionDenied(c, err); handled {
				return herr
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
		err := app.WithCollection(c.Request().Context(), userID, decodedParam(c, "name"), policy.ActionSchema, func(_ context.Context, owner, coll string) error {
			return svc.ResetForUser(owner, coll)
		})
		if err != nil {
			if handled, herr := collectionDenied(c, err); handled {
				return herr
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
		var remaining []string
		err := app.WithCollection(c.Request().Context(), userID, decodedParam(c, "name"), policy.ActionWrite, func(_ context.Context, owner, coll string) error {
			var err error
			remaining, err = svc.DeleteEntryForUser(owner, coll, payload.Entry)
			return err
		})
		if err != nil {
			if handled, herr := collectionDenied(c, err); handled {
				return herr
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
		err := app.WithCollection(c.Request().Context(), userID, decodedParam(c, "name"), policy.ActionSchema, func(_ context.Context, owner, coll string) error {
			return svc.AddSourceForUser(owner, coll, payload.URL, payload.UpdateInterval)
		})
		if err != nil {
			if handled, herr := collectionDenied(c, err); handled {
				return herr
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
		err := app.WithCollection(c.Request().Context(), userID, decodedParam(c, "name"), policy.ActionSchema, func(_ context.Context, owner, coll string) error {
			return svc.RemoveSourceForUser(owner, coll, payload.URL)
		})
		if err != nil {
			if handled, herr := collectionDenied(c, err); handled {
				return herr
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

// GetCollectionEntryRawFileEndpoint serves the original uploaded binary file.
// @Summary Download the file of an entry.
// @Description Serves the file as it was uploaded. For a collection shared with the caller the download runs under the share: a revocation or a lowering stops it.
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
		if svc == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "collections are not available"})
		}
		// The file is served inside the admission of the share: a revoke or a
		// lowering that takes the read away stops the download and answers
		// only once no more bytes go out.
		err = app.ServeCollectionEntryFile(c.Request().Context(), userID, decodedParam(c, "name"), entry, func(ctx context.Context, fpath string) error {
			return serveFileUntil(ctx, c, fpath)
		})
		if err != nil {
			if c.Response().Committed {
				// The download was cut short; the client sees a short body.
				return nil
			}
			if handled, herr := collectionDenied(c, err); handled {
				return herr
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return nil
	}
}

// serveFileUntil serves a file with its type, ranges and caching headers, and
// stops reading it when ctx ends.
func serveFileUntil(ctx context.Context, c echo.Context, fpath string) error {
	f, err := os.Open(fpath) // #nosec G304 -- the path comes from the collection backend, never from the request
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.ErrNotExist
	}
	http.ServeContent(c.Response(), c.Request(), info.Name(), info.ModTime(), &ctxFile{ctx: ctx, f: f})
	return ctx.Err()
}

// ctxFile is a file whose reads fail once ctx ends, so a running copy stops.
type ctxFile struct {
	ctx context.Context
	f   *os.File
}

func (r *ctxFile) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.f.Read(p)
}

func (r *ctxFile) Seek(offset int64, whence int) (int64, error) { return r.f.Seek(offset, whence) }

// ListCollectionSourcesEndpoint handles /api/collections/{name}/sources (GET).
// @Summary List the URLs a collection follows.
// @Description Only the owner may list the sources: a source URL can carry a credential. A person the collection is shared with gets 403 whatever the role.
// @Tags collections
// @Param name path string true "Collection name"
// @Success 200 {object} schema.CollectionSources "The sources"
// @Failure 403 {object} map[string]string "The collection is shared with the caller, who is not its owner"
// @Failure 404 {object} map[string]string "Collection not found"
// @Router /api/collections/{name}/sources [get]
func ListCollectionSourcesEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := effectiveUserID(c)
		// Only the owner lists the sources: a source URL can carry a token.
		sources, err := app.CollectionSources(c.Request().Context(), userID, decodedParam(c, "name"))
		if err != nil {
			if handled, herr := collectionDenied(c, err); handled {
				return herr
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"sources": sources,
			"count":   len(sources),
		})
	}
}

// embeddingModelInstalled reports whether the model that the collections embed
// with is installed: it has a configuration, or a file of that name is in the
// models folder. The collections call this server's embeddings endpoint, which
// answers 404 for a model it does not have.
func embeddingModelInstalled(app *application.Application, name string) bool {
	return app.EmbeddingModelInstalled(name)
}

// collectionDenied answers the errors that say the caller may not reach the
// collection: 404 for one that does not exist or is not shared with the
// caller (the two look the same), 403 for a shared one whose role does not
// allow the call. handled is false for any other error.
func collectionDenied(c echo.Context, err error) (bool, error) {
	switch {
	case errors.Is(err, application.ErrCollectionRole):
		return true, c.JSON(http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, application.ErrCollectionNotFound), strings.Contains(err.Error(), "not found"):
		return true, c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
	}
	return false, nil
}

// ShareCollectionEndpoint handles /api/collections/{name}/share (POST).
// @Summary Make a collection shareable.
// @Description Returns the ID of the collection in the sharing API: the owner then lists, grants and revokes access with /api/shares/collection/{resource_id}/grants. The roles are viewer (search and read the entries) and editor (also add and remove entries); a collection may be shared with @everyone. Only the owner may call it: another caller gets 404, as for a collection that does not exist. 409 sharing_disabled when this server cannot share collections (no named accounts).
// @Tags collections
// @Param name path string true "Collection name"
// @Success 200 {object} schema.CollectionShare "The share of the collection"
// @Failure 404 {object} map[string]string "Collection not found"
// @Failure 409 {object} map[string]string "Sharing is not available on this server"
// @Router /api/collections/{name}/share [post]
func ShareCollectionEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		name := decodedParam(c, "name")
		id, err := app.EnsureCollectionShare(c.Request().Context(), userID, name)
		switch {
		case errors.Is(err, application.ErrSharingOff):
			return c.JSON(http.StatusConflict, map[string]string{"error": err.Error(), "type": "sharing_disabled"})
		case err != nil:
			if handled, herr := collectionDenied(c, err); handled {
				return herr
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		n := 0
		if s, ok := app.CollectionShares(c.Request().Context(), userID, []string{name})[name]; ok {
			n = s.SharedWith
		}
		return c.JSON(http.StatusOK, schema.CollectionShare{ResourceID: id, SharedWith: n})
	}
}
