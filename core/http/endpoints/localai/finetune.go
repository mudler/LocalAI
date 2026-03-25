package localai

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services"
)

// StartFineTuneJobEndpoint starts a new fine-tuning job.
func StartFineTuneJobEndpoint(ftService *services.FineTuneService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)

		var req schema.FineTuneJobRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "Invalid request: " + err.Error(),
			})
		}

		if req.Model == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "model is required",
			})
		}
		if req.DatasetSource == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "dataset_source is required",
			})
		}

		resp, err := ftService.StartJob(c.Request().Context(), userID, req)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": err.Error(),
			})
		}

		return c.JSON(http.StatusCreated, resp)
	}
}

// ListFineTuneJobsEndpoint lists fine-tuning jobs for the current user.
func ListFineTuneJobsEndpoint(ftService *services.FineTuneService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		jobs := ftService.ListJobs(userID)
		if jobs == nil {
			jobs = []*schema.FineTuneJob{}
		}
		return c.JSON(http.StatusOK, jobs)
	}
}

// GetFineTuneJobEndpoint gets a specific fine-tuning job.
func GetFineTuneJobEndpoint(ftService *services.FineTuneService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		jobID := c.Param("id")

		job, err := ftService.GetJob(userID, jobID)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{
				"error": err.Error(),
			})
		}

		return c.JSON(http.StatusOK, job)
	}
}

// StopFineTuneJobEndpoint stops a running fine-tuning job.
func StopFineTuneJobEndpoint(ftService *services.FineTuneService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		jobID := c.Param("id")

		// Check for save_checkpoint query param
		saveCheckpoint := c.QueryParam("save_checkpoint") == "true"

		err := ftService.StopJob(c.Request().Context(), userID, jobID, saveCheckpoint)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{
				"error": err.Error(),
			})
		}

		return c.JSON(http.StatusOK, map[string]string{
			"status":  "stopped",
			"message": "Fine-tuning job stopped",
		})
	}
}

// DeleteFineTuneJobEndpoint deletes a fine-tuning job and its data.
func DeleteFineTuneJobEndpoint(ftService *services.FineTuneService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		jobID := c.Param("id")

		err := ftService.DeleteJob(userID, jobID)
		if err != nil {
			status := http.StatusInternalServerError
			if strings.Contains(err.Error(), "not found") {
				status = http.StatusNotFound
			} else if strings.Contains(err.Error(), "cannot delete") {
				status = http.StatusConflict
			}
			return c.JSON(status, map[string]string{
				"error": err.Error(),
			})
		}

		return c.JSON(http.StatusOK, map[string]string{
			"status":  "deleted",
			"message": "Fine-tuning job deleted",
		})
	}
}

// FineTuneProgressEndpoint streams progress updates via SSE.
func FineTuneProgressEndpoint(ftService *services.FineTuneService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		jobID := c.Param("id")

		// Set SSE headers
		c.Response().Header().Set("Content-Type", "text/event-stream")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")
		c.Response().WriteHeader(http.StatusOK)

		err := ftService.StreamProgress(c.Request().Context(), userID, jobID, func(event *schema.FineTuneProgressEvent) {
			data, err := json.Marshal(event)
			if err != nil {
				return
			}
			fmt.Fprintf(c.Response(), "data: %s\n\n", data)
			c.Response().Flush()
		})
		if err != nil {
			// If headers already sent, we can't send a JSON error
			fmt.Fprintf(c.Response(), "data: {\"status\":\"error\",\"message\":%q}\n\n", err.Error())
			c.Response().Flush()
		}

		return nil
	}
}

// ListCheckpointsEndpoint lists checkpoints for a job.
func ListCheckpointsEndpoint(ftService *services.FineTuneService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		jobID := c.Param("id")

		checkpoints, err := ftService.ListCheckpoints(c.Request().Context(), userID, jobID)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{
				"error": err.Error(),
			})
		}

		return c.JSON(http.StatusOK, map[string]any{
			"checkpoints": checkpoints,
		})
	}
}

// ExportModelEndpoint exports a model from a checkpoint.
func ExportModelEndpoint(ftService *services.FineTuneService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		jobID := c.Param("id")

		var req schema.ExportRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "Invalid request: " + err.Error(),
			})
		}

		modelName, err := ftService.ExportModel(c.Request().Context(), userID, jobID, req)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": err.Error(),
			})
		}

		return c.JSON(http.StatusAccepted, map[string]string{
			"status":     "exporting",
			"message":    "Export started for model '" + modelName + "'",
			"model_name": modelName,
		})
	}
}

// DownloadExportedModelEndpoint streams the exported model directory as a tar.gz archive.
func DownloadExportedModelEndpoint(ftService *services.FineTuneService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		jobID := c.Param("id")

		modelDir, modelName, err := ftService.GetExportedModelPath(userID, jobID)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{
				"error": err.Error(),
			})
		}

		c.Response().Header().Set("Content-Type", "application/gzip")
		c.Response().Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.tar.gz"`, modelName))
		c.Response().WriteHeader(http.StatusOK)

		gw := gzip.NewWriter(c.Response())
		defer gw.Close()

		tw := tar.NewWriter(gw)
		defer tw.Close()

		err = filepath.Walk(modelDir, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}

			relPath, err := filepath.Rel(modelDir, path)
			if err != nil {
				return err
			}

			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = filepath.Join(modelName, relPath)

			if err := tw.WriteHeader(header); err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()

			_, err = io.Copy(tw, f)
			return err
		})

		if err != nil {
			// Headers already sent, can't return JSON error
			return err
		}

		return nil
	}
}

// ListFineTuneBackendsEndpoint returns installed backends tagged with "fine-tuning".
func ListFineTuneBackendsEndpoint(appConfig *config.ApplicationConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		backends, err := gallery.AvailableBackends(appConfig.BackendGalleries, appConfig.SystemState)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to list backends: " + err.Error(),
			})
		}

		type backendInfo struct {
			Name        string   `json:"name"`
			Description string   `json:"description,omitempty"`
			Tags        []string `json:"tags,omitempty"`
		}

		var result []backendInfo
		for _, b := range backends {
			if !b.Installed {
				continue
			}
			hasTag := false
			for _, t := range b.Tags {
				if strings.EqualFold(t, "fine-tuning") {
					hasTag = true
					break
				}
			}
			if !hasTag {
				continue
			}
			name := b.Name
			if b.Alias != "" {
				name = b.Alias
			}
			result = append(result, backendInfo{
				Name:        name,
				Description: b.Description,
				Tags:        b.Tags,
			})
		}

		if result == nil {
			result = []backendInfo{}
		}

		return c.JSON(http.StatusOK, result)
	}
}

// UploadDatasetEndpoint handles dataset file upload.
func UploadDatasetEndpoint(ftService *services.FineTuneService) echo.HandlerFunc {
	return func(c echo.Context) error {
		file, err := c.FormFile("file")
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "file is required",
			})
		}

		src, err := file.Open()
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to open file",
			})
		}
		defer src.Close()

		data, err := io.ReadAll(src)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to read file",
			})
		}

		path, err := ftService.UploadDataset(file.Filename, data)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": err.Error(),
			})
		}

		return c.JSON(http.StatusOK, map[string]string{
			"path": path,
		})
	}
}
