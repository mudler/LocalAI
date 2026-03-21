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
		userID := getUserID(c)
		cols, err := svc.ListCollectionsForUser(userID)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}

		resp := map[string]any{
			"collections": cols,
			"count":       len(cols),
		}

		// Admin cross-user aggregation
		if wantsAllUsers(c) {
			usm := svc.UserServicesManager()
			if usm != nil {
				userIDs, _ := usm.ListAllUserIDs()
				userGroups := map[string]any{}
				for _, uid := range userIDs {
					if uid == userID {
						continue
					}
					userCols, err := svc.ListCollectionsForUser(uid)
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

func CreateCollectionEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := getUserID(c)
		var payload struct {
			Name string `json:"name"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := svc.CreateCollectionForUser(userID, payload.Name); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusCreated, map[string]string{"status": "ok", "name": payload.Name})
	}
}

func UploadToCollectionEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := effectiveUserID(c)
		name := c.Param("name")
		file, err := c.FormFile("file")
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "file required"})
		}
		src, err := file.Open()
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		defer src.Close()
		key, err := svc.UploadToCollectionForUser(userID, name, file.Filename, src)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok", "filename": file.Filename, "key": key})
	}
}

func ListCollectionEntriesEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := effectiveUserID(c)
		entries, err := svc.ListCollectionEntriesForUser(userID, c.Param("name"))
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
		userID := effectiveUserID(c)
		entryParam := c.Param("*")
		entry, err := url.PathUnescape(entryParam)
		if err != nil {
			entry = entryParam
		}
		content, chunkCount, err := svc.GetCollectionEntryContentForUser(userID, c.Param("name"), entry)
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
		userID := effectiveUserID(c)
		var payload struct {
			Query      string `json:"query"`
			MaxResults int    `json:"max_results"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		results, err := svc.SearchCollectionForUser(userID, c.Param("name"), payload.Query, payload.MaxResults)
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
		userID := effectiveUserID(c)
		if err := svc.ResetCollectionForUser(userID, c.Param("name")); err != nil {
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
		userID := effectiveUserID(c)
		var payload struct {
			Entry string `json:"entry"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		remaining, err := svc.DeleteCollectionEntryForUser(userID, c.Param("name"), payload.Entry)
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
		if err := svc.AddCollectionSourceForUser(userID, c.Param("name"), payload.URL, payload.UpdateInterval); err != nil {
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
		userID := effectiveUserID(c)
		var payload struct {
			URL string `json:"url"`
		}
		if err := c.Bind(&payload); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if err := svc.RemoveCollectionSourceForUser(userID, c.Param("name"), payload.URL); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
	}
}

// GetCollectionEntryRawFileEndpoint serves the original uploaded binary file.
func GetCollectionEntryRawFileEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.AgentPoolService()
		userID := effectiveUserID(c)
		entryParam := c.Param("*")
		entry, err := url.PathUnescape(entryParam)
		if err != nil {
			entry = entryParam
		}
		fpath, err := svc.GetCollectionEntryFilePathForUser(userID, c.Param("name"), entry)
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
		userID := effectiveUserID(c)
		sources, err := svc.ListCollectionSourcesForUser(userID, c.Param("name"))
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
