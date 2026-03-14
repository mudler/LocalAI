package localai

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
)

func ListCollectionsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		collections, err := svc.ListCollections()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]any{
			"collections": collections,
			"count":       len(collections),
		})
	}
}

func CreateCollectionEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		var payload struct {
			Name string `json:"name"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := svc.CreateCollection(payload.Name); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, map[string]string{"status": "ok", "name": payload.Name})
	}
}

func UploadToCollectionEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		name := c.Param("name")
		file, err := c.FormFile("file")
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "file required"})
		}
		if svc.CollectionEntryExists(name, file.Filename) {
			return c.JSON(http.StatusConflict, map[string]string{"error": "entry already exists"})
		}
		src, err := file.Open()
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		defer src.Close()
		if err := svc.UploadToCollection(name, file.Filename, src); err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok", "filename": file.Filename})
	}
}

func ListCollectionEntriesEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		entries, err := svc.ListCollectionEntries(c.Param("name"))
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

func GetCollectionEntryContentEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		entryParam := c.Param("*")
		entry, err := url.PathUnescape(entryParam)
		if err != nil {
			entry = entryParam
		}
		content, chunkCount, err := svc.GetCollectionEntryContent(c.Param("name"), entry)
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

func SearchCollectionEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		var payload struct {
			Query      string `json:"query"`
			MaxResults int    `json:"max_results"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		results, err := svc.SearchCollection(c.Param("name"), payload.Query, payload.MaxResults)
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

func ResetCollectionEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		if err := svc.ResetCollection(c.Param("name")); err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

func DeleteCollectionEntryEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		var payload struct {
			Entry string `json:"entry"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		remaining, err := svc.DeleteCollectionEntry(c.Param("name"), payload.Entry)
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

func AddCollectionSourceEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
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
		if err := svc.AddCollectionSource(c.Param("name"), payload.URL, payload.UpdateInterval); err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

func RemoveCollectionSourceEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		var payload struct {
			URL string `json:"url"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := svc.RemoveCollectionSource(c.Param("name"), payload.URL); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

// GetCollectionEntryRawFileEndpoint serves the original uploaded binary file.
func GetCollectionEntryRawFileEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		entryParam := c.Param("*")
		entry, err := url.PathUnescape(entryParam)
		if err != nil {
			entry = entryParam
		}
		fpath, err := svc.GetCollectionEntryFilePath(c.Param("name"), entry)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.File(fpath)
	}
}

func ListCollectionSourcesEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		sources, err := svc.ListCollectionSources(c.Param("name"))
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
