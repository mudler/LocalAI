package localai

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/schema"
)

// StartTrainingEndpoint creates a new fine-tuning job
// @Summary Start a fine-tuning job
// @Description Start a new fine-tuning job with the specified model and dataset
// @Tags training
// @Accept json
// @Produce json
// @Param request body schema.TrainingJobRequest true "Training job configuration"
// @Success 201 {object} schema.TrainingJob "Training job created"
// @Failure 400 {object} map[string]string "Invalid request"
// @Failure 500 {object} map[string]string "Internal server error"
// @Router /api/training/jobs [post]
func StartTrainingEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.TrainingJobService()
		if svc == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Training service not available"})
		}

		var req schema.TrainingJobRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request body: " + err.Error()})
		}

		job, err := svc.CreateJob(req)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusCreated, job)
	}
}

// GetTrainingJobEndpoint returns the status of a training job
// @Summary Get training job status
// @Description Get the current status and progress of a training job
// @Tags training
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} schema.TrainingJob "Training job status"
// @Failure 404 {object} map[string]string "Job not found"
// @Router /api/training/jobs/{id} [get]
func GetTrainingJobEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.TrainingJobService()
		if svc == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Training service not available"})
		}

		id := c.Param("id")
		job, ok := svc.GetJob(id)
		if !ok {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Job not found: " + id})
		}

		return c.JSON(http.StatusOK, job)
	}
}

// ListTrainingJobsEndpoint lists all training jobs
// @Summary List training jobs
// @Description List all fine-tuning jobs
// @Tags training
// @Produce json
// @Success 200 {array} schema.TrainingJob "List of training jobs"
// @Router /api/training/jobs [get]
func ListTrainingJobsEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.TrainingJobService()
		if svc == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Training service not available"})
		}

		return c.JSON(http.StatusOK, svc.ListJobs())
	}
}

// CancelTrainingEndpoint cancels a running training job
// @Summary Cancel a training job
// @Description Cancel a running or pending training job
// @Tags training
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} map[string]string "Job cancelled"
// @Failure 400 {object} map[string]string "Job cannot be cancelled"
// @Failure 404 {object} map[string]string "Job not found"
// @Router /api/training/jobs/{id}/cancel [post]
func CancelTrainingEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.TrainingJobService()
		if svc == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Training service not available"})
		}

		id := c.Param("id")
		if err := svc.CancelJob(id); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Job cancelled"})
	}
}

// DeleteTrainingEndpoint deletes a training job
// @Summary Delete a training job
// @Description Delete a training job record
// @Tags training
// @Produce json
// @Param id path string true "Job ID"
// @Success 200 {object} map[string]string "Job deleted"
// @Failure 404 {object} map[string]string "Job not found"
// @Router /api/training/jobs/{id} [delete]
func DeleteTrainingEndpoint(app *application.Application) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc := app.TrainingJobService()
		if svc == nil {
			return c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "Training service not available"})
		}

		id := c.Param("id")
		if err := svc.DeleteJob(id); err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		}

		return c.JSON(http.StatusOK, map[string]string{"message": "Job deleted"})
	}
}
