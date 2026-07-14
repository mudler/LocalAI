package localai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/quantization"
)

// StartQuantizationJobEndpoint starts a new quantization job.
func StartQuantizationJobEndpoint(qService *quantization.QuantizationService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)

		var req schema.QuantizationJobRequest
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

		resp, err := qService.StartJob(c.Request().Context(), userID, req)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": err.Error(),
			})
		}

		return c.JSON(http.StatusCreated, resp)
	}
}

// ListQuantizationJobsEndpoint lists quantization jobs for the current user.
func ListQuantizationJobsEndpoint(qService *quantization.QuantizationService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		jobs := qService.ListJobs(userID)
		if jobs == nil {
			jobs = []*schema.QuantizationJob{}
		}
		return c.JSON(http.StatusOK, jobs)
	}
}

// GetQuantizationJobEndpoint gets a specific quantization job.
func GetQuantizationJobEndpoint(qService *quantization.QuantizationService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		jobID := c.Param("id")

		job, err := qService.GetJob(userID, jobID)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{
				"error": err.Error(),
			})
		}

		return c.JSON(http.StatusOK, job)
	}
}

// StopQuantizationJobEndpoint stops a running quantization job.
func StopQuantizationJobEndpoint(qService *quantization.QuantizationService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		jobID := c.Param("id")

		err := qService.StopJob(c.Request().Context(), userID, jobID)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{
				"error": err.Error(),
			})
		}

		return c.JSON(http.StatusOK, map[string]string{
			"status":  "stopped",
			"message": "Quantization job stopped",
		})
	}
}

// DeleteQuantizationJobEndpoint deletes a quantization job and its data.
func DeleteQuantizationJobEndpoint(qService *quantization.QuantizationService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		jobID := c.Param("id")

		err := qService.DeleteJob(userID, jobID)
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
			"message": "Quantization job deleted",
		})
	}
}

// QuantizationProgressEndpoint streams progress updates via SSE.
func QuantizationProgressEndpoint(qService *quantization.QuantizationService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		jobID := c.Param("id")

		// Set SSE headers
		c.Response().Header().Set("Content-Type", "text/event-stream")
		c.Response().Header().Set("Cache-Control", "no-cache")
		c.Response().Header().Set("Connection", "keep-alive")
		c.Response().WriteHeader(http.StatusOK)

		err := qService.StreamProgress(c.Request().Context(), userID, jobID, func(event *schema.QuantizationProgressEvent) {
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

// ImportQuantizedModelEndpoint imports a quantized model into LocalAI.
func ImportQuantizedModelEndpoint(qService *quantization.QuantizationService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		jobID := c.Param("id")

		var req schema.QuantizationImportRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "Invalid request: " + err.Error(),
			})
		}

		modelName, err := qService.ImportModel(c.Request().Context(), userID, jobID, req)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": err.Error(),
			})
		}

		return c.JSON(http.StatusAccepted, map[string]string{
			"status":     "importing",
			"message":    "Import started for model '" + modelName + "'",
			"model_name": modelName,
		})
	}
}

// DownloadQuantizedModelEndpoint streams the quantized model file.
func DownloadQuantizedModelEndpoint(qService *quantization.QuantizationService) echo.HandlerFunc {
	return func(c echo.Context) error {
		userID := getUserID(c)
		jobID := c.Param("id")

		outputPath, downloadName, err := qService.GetOutputPath(userID, jobID)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{
				"error": err.Error(),
			})
		}

		return c.Attachment(outputPath, downloadName)
	}
}

// ListQuantizationBackendsEndpoint returns installed backends tagged with "quantization".
func ListQuantizationBackendsEndpoint(appConfig *config.ApplicationConfig) echo.HandlerFunc {
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
				if strings.EqualFold(t, "quantization") {
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
